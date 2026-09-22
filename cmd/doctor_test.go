package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/branow/dbmap/internal/cmdutil"
	"github.com/branow/dbmap/internal/config"
	"github.com/branow/dbmap/internal/connect"
	"github.com/branow/dbmap/internal/credentials"
	"github.com/branow/dbmap/internal/engine"
)

// server is a database that answers a preflight and nothing else. No socket is
// opened anywhere in this file.
type server struct {
	verify error
	probe  error
	health engine.Health
	closed bool
}

func (s *server) Verify(context.Context) error { return s.verify }

func (s *server) Health(context.Context) (engine.Health, error) {
	if s.probe != nil {
		return engine.Health{}, s.probe
	}
	return s.health, nil
}

func (s *server) Close() error {
	s.closed = true
	return nil
}

// reachable is a server with room for a build.
func reachable() *server {
	return &server{health: engine.Classify(&engine.Reading{MemoryVisible: true, AvailableGB: 9})}
}

// answers builds an opener that hands back one server, or refuses to open at
// all, which is the shape of every Kerberos failure.
func answers(s *server, err error) opener {
	return func(string, config.Connection, credentials.Secret) (probe, error) {
		if err != nil {
			return nil, err
		}
		return s, nil
	}
}

// line is one check's row as a caller parses it.
type line struct {
	Check  string `json:"check"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// doctored runs the preflight against a seeded harness and returns the rows it
// produced, everything it printed, and how it ended. The rows are read back as
// json so a test asserts on a status column rather than on a substring.
func doctored(t *testing.T, h *harness, open opener, named string) ([]line, string, error) {
	t.Helper()
	if err := h.factory.Resolve(config.Overrides{Output: "json"}); err != nil {
		t.Fatalf("resolving flags: %v", err)
	}
	h.out.Reset()
	h.errOut.Reset()
	failure := runDoctor(context.Background(), h.factory, named, open)

	var rows []line
	if h.out.Len() > 0 {
		if err := json.Unmarshal(h.out.Bytes(), &rows); err != nil {
			t.Fatalf("reading the report: %v\n%s", err, h.out.String())
		}
	}
	return rows, h.out.String() + h.errOut.String(), failure
}

// status is one check's outcome, or "" when it was never reported.
func status(rows []line, check string) string {
	for _, row := range rows {
		if row.Check == check {
			return row.Status
		}
	}
	return ""
}

// counted is how many rows ended in one status.
func counted(rows []line, want string) int {
	n := 0
	for _, row := range rows {
		if row.Status == want {
			n++
		}
	}
	return n
}

func TestDoctorReportsEveryCheckSeparately(t *testing.T) {
	h := newHarness(t)
	h.seed(t)

	rows, out, err := doctored(t, h, answers(reachable(), nil), "primary")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}

	if len(rows) != len(preflights) {
		t.Fatalf("reported %d checks, want %d:\n%s", len(rows), len(preflights), out)
	}
	for i, c := range preflights {
		if rows[i].Check != c.Name {
			t.Fatalf("row %d is %q, want %q", i, rows[i].Check, c.Name)
		}
		if rows[i].Status != statusPass {
			t.Fatalf("check %q = %q, want pass", c.Name, rows[i].Status)
		}
		if rows[i].Detail == "" {
			t.Fatalf("check %q reported no detail", c.Name)
		}
	}
}

// The failure everyone hits first on macOS. The remedy has to survive from the
// typed error all the way to what the user reads, verbatim.
func TestDoctorPrintsTheKerberosCredentialCacheRemedy(t *testing.T) {
	h := newHarness(t)
	h.seed(t)
	refused := &connect.CredentialCacheError{Path: "/tmp/krb5cc_501", Type: "API"}

	rows, out, err := doctored(t, h, answers(nil, refused), "primary")

	if status(rows, "connection") != statusFail {
		t.Fatalf("the connection check did not fail:\n%s", out)
	}
	if !errors.Is(err, error(refused)) {
		t.Fatalf("err = %v, want the credential cache error", err)
	}
	if !strings.Contains(out, "kinit -c FILE:/tmp/krb5cc_501") {
		t.Fatalf("the remedy did not reach the output:\n%s", out)
	}
	if !strings.Contains(out, connect.CredCacheParam) {
		t.Fatalf("the parameter to set was not named:\n%s", out)
	}
	if !strings.Contains(out, "API") {
		t.Fatalf("the cache type was not named:\n%s", out)
	}
	if cmdutil.ExitCode(err) != cmdutil.ExitAuth {
		t.Fatalf("exit = %d, want %d", cmdutil.ExitCode(err), cmdutil.ExitAuth)
	}
}

// Cross-realm is named as an unsupported configuration, never reported as a
// generic authentication failure.
func TestDoctorNamesCrossRealmAsUnsupported(t *testing.T) {
	h := newHarness(t)
	h.seed(t)
	refused := &connect.UnsupportedError{
		Configuration: "cross-realm Kerberos",
		Reason:        "the pure-Go driver follows no realm referral",
		Remedy:        "authenticate in the host's own realm, or use a SQL login",
	}

	_, out, err := doctored(t, h, answers(nil, refused), "primary")

	if err == nil {
		t.Fatalf("doctor passed a cross-realm setup")
	}
	if !strings.Contains(out, "cross-realm") {
		t.Fatalf("cross-realm was not named:\n%s", out)
	}
	if !strings.Contains(out, refused.Remedy) {
		t.Fatalf("the remedy did not reach the output:\n%s", out)
	}
	if cmdutil.ExitCode(err) != cmdutil.ExitValidation {
		t.Fatalf("exit = %d, want %d", cmdutil.ExitCode(err), cmdutil.ExitValidation)
	}
}

// A failure stops the run, but every check is still reported, so the output has
// one shape and a reader can see how far it got.
func TestDoctorSkipsTheChecksAFailureMadeUnanswerable(t *testing.T) {
	cases := []struct {
		name   string
		server *server
		open   error
		failed string
	}{
		{"the connection cannot be opened", nil,
			&connect.ProductionError{Name: "primary"}, "connection"},
		{"the login is refused", &server{verify: errors.New("login failed")}, nil, "auth"},
		{"the read path is refused", &server{probe: errors.New("permission denied")},
			nil, "read-only"},
		{"the server has no room", &server{health: engine.Health{
			OK: false, Known: true, Reason: "the server reports low"}}, nil, "health"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			h.seed(t)

			rows, out, err := doctored(t, h, answers(c.server, c.open), "primary")

			if err == nil {
				t.Fatalf("doctor passed:\n%s", out)
			}
			if len(rows) != len(preflights) {
				t.Fatalf("reported %d checks, want all %d:\n%s", len(rows), len(preflights), out)
			}
			if status(rows, c.failed) != statusFail {
				t.Fatalf("check %q did not fail:\n%s", c.failed, out)
			}
			if counted(rows, statusFail) != 1 {
				t.Fatalf("want exactly one failed check:\n%s", out)
			}
			after := len(preflights) - 1 - at(preflights, c.failed)
			if got := counted(rows, statusSkipped); got != after {
				t.Fatalf("%d checks skipped, want %d:\n%s", got, after, out)
			}
		})
	}
}

// An unreadable health reading is unknown, never healthy.
func TestDoctorRefusesAnUnreadableHealthReading(t *testing.T) {
	h := newHarness(t)
	h.seed(t)

	_, out, err := doctored(t, h, answers(&server{health: engine.Classify(nil)}, nil), "primary")

	if err == nil {
		t.Fatalf("doctor passed an unreadable reading:\n%s", out)
	}
	if !strings.Contains(out, engine.Unreadable) {
		t.Fatalf("the reading was not reported as unreadable:\n%s", out)
	}
}

// Kerberos stores no password, so the credential check must not report a
// missing secret for it.
func TestDoctorAsksForNoPasswordUnderKerberos(t *testing.T) {
	h := newHarness(t)
	h.seed(t)
	if err := h.factory.Config.SetConnection("realm", config.Connection{
		Engine: config.SQLServer, Host: "example.internal", Auth: config.Kerberos,
	}); err != nil {
		t.Fatalf("defining the connection: %v", err)
	}

	rows, out, err := doctored(t, h, answers(reachable(), nil), "realm")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if status(rows, "credential") != statusPass {
		t.Fatalf("the credential check failed under kerberos:\n%s", out)
	}
	if !strings.Contains(out, string(config.Kerberos)) {
		t.Fatalf("the auth mode was not named:\n%s", out)
	}
}

// A missing stored password is a not-found condition, distinct from a rejected
// one, and the run stops there rather than dialling.
func TestDoctorReportsAMissingPassword(t *testing.T) {
	h := newHarness(t)
	h.seed(t)
	if err := h.store.Delete(credentials.DBKey("primary")); err != nil {
		t.Fatalf("clearing the secret: %v", err)
	}
	opened := false
	open := func(string, config.Connection, credentials.Secret) (probe, error) {
		opened = true
		return reachable(), nil
	}

	_, _, err := doctored(t, h, open, "primary")

	if cmdutil.ExitCode(err) != cmdutil.ExitNotFound {
		t.Fatalf("exit = %d, want %d", cmdutil.ExitCode(err), cmdutil.ExitNotFound)
	}
	if opened {
		t.Fatalf("doctor dialled without a credential")
	}
}

func TestDoctorUsesTheActiveProfileWhenNoConnectionIsNamed(t *testing.T) {
	h := newHarness(t)
	h.seed(t)
	if err := h.factory.Config.Switch("work"); err != nil {
		t.Fatalf("switching profile: %v", err)
	}

	_, out, err := doctored(t, h, answers(reachable(), nil), "")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if !strings.Contains(out, "example.internal") {
		t.Fatalf("the profile's connection was not used:\n%s", out)
	}
}

func TestDoctorNeedsAConnection(t *testing.T) {
	h := newHarness(t)

	_, _, err := doctored(t, h, answers(reachable(), nil), "")

	var missing *cmdutil.NotConfiguredError
	if !errors.As(err, &missing) {
		t.Fatalf("err = %v, want a NotConfiguredError", err)
	}
}

func TestDoctorReleasesWhatItOpened(t *testing.T) {
	h := newHarness(t)
	h.seed(t)
	live := reachable()

	if _, _, err := doctored(t, h, answers(live, nil), "primary"); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if !live.closed {
		t.Fatalf("the connection was left open")
	}
}

// at is where a named check sits in the preflight table.
func at(list []check, name string) int {
	for i, c := range list {
		if c.Name == name {
			return i
		}
	}
	return -1
}
