package cmd

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/branow/dbmap/internal/cmdutil"
	"github.com/branow/dbmap/internal/config"
	"github.com/branow/dbmap/internal/connect"
	"github.com/branow/dbmap/internal/credentials"
	"github.com/branow/dbmap/internal/engine"
	"github.com/branow/dbmap/internal/output"
)

// The three outcomes a check reports. They are output, so they are strings.
const (
	statusPass    = "pass"
	statusFail    = "fail"
	statusSkipped = "skipped"
)

// probe is the live half of a preflight: everything doctor may ask a server and
// nothing else. An interface, so every check is testable with no database.
type probe interface {
	// Verify proves the login was accepted: no catalog read, no row.
	Verify(ctx context.Context) error
	// Health runs the engine's guarded read-only health statement.
	Health(ctx context.Context) (engine.Health, error)
	Close() error
}

// opener turns a connection record into a probe. Opening validates a Kerberos
// credential cache and refuses a cross-realm setup, so a failure here arrives
// already diagnosed.
type opener func(name string, entry config.Connection, secret credentials.Secret) (probe, error)

// newDoctor builds the preflight command: one login, one health statement, no
// catalog read.
func newDoctor(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor [connection]",
		Short: "Check a connection before a build depends on it",
		Long: "Reports one pass or fail line per check:\n\n" + proves() + "\n" +
			"It sends nothing heavy: one login and one health statement, no catalog " +
			"read and no sampled row.\n\n" +
			"Kerberos is the case this exists for. The pure-Go driver reads FILE: " +
			"credential caches only, while macOS defaults to the keychain-backed " +
			"API: type, so doctor names an absent or unreadable cache and prints " +
			"the kinit command that fixes it. A cross-realm setup is named as an " +
			"unsupported configuration rather than reported as an authentication " +
			"failure.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return runDoctor(c.Context(), f, arg(args), dial)
		},
	}
}

// proves renders the check table into the command's help, so what doctor
// documents and what it runs are one list.
func proves() string {
	var out strings.Builder
	for _, c := range preflights {
		fmt.Fprintf(&out, "  %-11s %s\n", c.Name, c.Doc)
	}
	return out.String()
}

// preflight is one doctor run: what it checks, and what it has opened so far.
type preflight struct {
	factory *cmdutil.Factory
	name    string
	entry   config.Connection
	open    opener
	secret  credentials.Secret
	probe   probe
	health  engine.Health
}

// check is one row of the preflight table: what it proves, and how. The checks
// are data walked in order, so adding one is a row rather than a branch.
type check struct {
	Name string
	// Doc is what this check proves, for the command's own help.
	Doc string
	// Run returns the detail line for a pass, or the error that failed it.
	Run func(context.Context, *preflight) (string, error)
}

// preflights is the check list, in the order each becomes answerable. Each
// depends on the one before, so a failure skips the rest rather than producing
// four cascading complaints about one cause.
var preflights = []check{
	{
		Name: "credential",
		Doc:  "the secret this connection needs is reachable",
		Run:  credential,
	},
	{
		Name: "connection",
		Doc:  "the connection resolves into a usable data source",
		Run:  connection,
	},
	{
		Name: "auth",
		Doc:  "the server accepted the login",
		Run:  auth,
	},
	{
		Name: "read-only",
		Doc:  "a guarded, read-only statement was accepted",
		Run:  readonly,
	},
	{
		Name: "health",
		Doc:  "the server has room for a build",
		Run:  health,
	},
}

// credential proves the secret exists before anything is dialled. An auth mode
// that carries no password is not a gap: the ticket cache is the credential.
func credential(_ context.Context, p *preflight) (string, error) {
	if !config.NeedsPassword(p.entry.Auth) {
		return string(p.entry.Auth) + " carries no stored password", nil
	}
	key := credentials.DBKey(p.name)
	secret, err := p.factory.Store.Get(key)
	if err != nil {
		return "", err
	}
	p.secret = secret
	return "found under " + key, nil
}

// connection resolves the record into a pool without dialling: this is where a
// Kerberos credential cache is read, and a
// cross-realm setup is named.
func connection(_ context.Context, p *preflight) (string, error) {
	opened, err := p.open(p.name, p.entry, p.secret)
	if err != nil {
		return "", err
	}
	p.probe = opened
	return fmt.Sprintf("%s at %s via %s", p.entry.Engine, p.entry.Host, p.entry.Auth), nil
}

// auth is the cheapest real call there is: a login and nothing else.
func auth(ctx context.Context, p *preflight) (string, error) {
	if err := p.probe.Verify(ctx); err != nil {
		return "", err
	}
	return "the server accepted the login", nil
}

// readonly proves the read path, not a setting: the health statement goes
// through the same gate and resource guard as every other statement, so an
// answer at all is the proof. The next check judges the reading.
func readonly(ctx context.Context, p *preflight) (string, error) {
	reading, err := p.probe.Health(ctx)
	if err != nil {
		return "", err
	}
	p.health = reading
	return "a guarded read-only statement was accepted", nil
}

// health judges the reading the previous check took. An unreadable reading is
// unknown, never healthy.
func health(_ context.Context, p *preflight) (string, error) {
	if err := engine.Assert(p.health, "a build"); err != nil {
		return "", err
	}
	if !p.health.Reading.MemoryVisible {
		return "the server reports room for a build", nil
	}
	return "room for a build, " +
		strconv.FormatFloat(p.health.Reading.AvailableGB, 'f', 1, 64) +
		" GB of OS memory free", nil
}

// runDoctor walks the checks and reports every one. A failure stops the run,
// but the remaining rows still print as skipped, so the output keeps one shape
// and a reader can see how far it got.
func runDoctor(ctx context.Context, f *cmdutil.Factory, named string, open opener) error {
	name, entry, err := resolveConnection(f, named)
	if err != nil {
		return err
	}
	state := &preflight{factory: f, name: name, entry: entry, open: open}
	defer state.close()

	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	var failure error
	records := make([]output.Record, 0, len(preflights))
	for _, c := range preflights {
		if failure != nil {
			records = append(records, row(c, statusSkipped, "not reached"))
			continue
		}
		detail, err := c.Run(ctx, state)
		if err != nil {
			failure = err
			records = append(records, row(c, statusFail, err.Error()))
			continue
		}
		records = append(records, row(c, statusPass, detail))
	}

	if err := f.Writer().List(records); err != nil {
		return err
	}
	if failure != nil {
		advise(f, failure)
	}
	return failure
}

func row(c check, status, detail string) output.Record {
	return output.Record{
		{Name: "check", Value: c.Name},
		{Name: "status", Value: status},
		{Name: "detail", Value: detail},
	}
}

// close releases whatever the run opened.
func (p *preflight) close() {
	if p.probe != nil {
		_ = p.probe.Close()
	}
}

// advise prints the one-line fix for the two failures a user cannot diagnose
// from the driver's own words: macOS defaults to an API: credential cache that
// the pure-Go driver, which reads FILE: only, cannot see; and a cross-realm
// setup surfaces as "Cannot generate SSPI context", which never mentions realms.
func advise(f *cmdutil.Factory, err error) {
	var cache *connect.CredentialCacheError
	if errors.As(err, &cache) {
		fmt.Fprintf(f.IO.ErrOut, "remedy: %s\n", cache.Remedy())
		return
	}
	var cert *connect.CertificateError
	if errors.As(err, &cert) {
		fmt.Fprintf(f.IO.ErrOut, "remedy: %s\n", cert.Remedy())
		return
	}
	var cross *connect.CrossRealmError
	if errors.As(err, &cross) {
		fmt.Fprintf(f.IO.ErrOut, "remedy: %s\n", cross.Remedy())
		return
	}
	var unsupported *connect.UnsupportedError
	if errors.As(err, &unsupported) && unsupported.Remedy != "" {
		fmt.Fprintf(f.IO.ErrOut, "remedy: %s\n", unsupported.Remedy)
	}
}

// dial is the real opener: a pool and the engine that reads through it.
func dial(name string, entry config.Connection, secret credentials.Secret) (probe, error) {
	pool, err := connect.Open(name, entry, secret.Reveal())
	if err != nil {
		return nil, err
	}
	reader, err := pool.Engine()
	if err != nil {
		_ = pool.Close()
		return nil, err
	}
	return live{pool: pool, engine: reader}, nil
}

// live is a probe backed by a real pool.
type live struct {
	pool   *connect.Pool
	engine engine.Engine
}

func (l live) Verify(ctx context.Context) error { return l.pool.Verify(ctx) }

func (l live) Health(ctx context.Context) (engine.Health, error) {
	return l.engine.Health(ctx, l.pool.Conn())
}

func (l live) Close() error { return l.pool.Close() }
