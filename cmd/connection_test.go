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
)

func TestConnectionAddFromFlags(t *testing.T) {
	h := newHarness(t)
	h.in.WriteString(password + "\n")
	err := h.run("connection", "add", "primary", "--engine", "sqlserver",
		"--host", "example.internal", "--port", "1433", "--database", "AppCore",
		"--auth", "sqllogin", "--username", "reader", "--param", "encrypt=true",
		"--password-stdin", "--no-input")
	if err != nil {
		t.Fatalf("connection add: %v", err)
	}

	entry, err := h.factory.Config.Connection("primary")
	if err != nil {
		t.Fatal(err)
	}
	if entry.Host != "example.internal" || entry.Port != 1433 {
		t.Errorf("stored connection = %+v", entry)
	}
	if entry.Params["encrypt"] != "true" {
		t.Errorf("driver params = %v", entry.Params)
	}
	if got := h.store.Values[credentials.DBKey("primary")]; got != password {
		t.Errorf("stored secret = %q", got)
	}
	if h.factory.Config.CurrentProfile != "" {
		t.Error("adding a connection activated something")
	}
	saved, err := config.LoadFrom(h.factory.Config.Path())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := saved.Connection("primary"); err != nil {
		t.Errorf("the connection was not written to the file: %v", err)
	}
}

// TestConfigFileHoldsNoSecret is the rule that keeps a password out of source
// control and out of a backup: the file may name the login, never the secret.
func TestConfigFileHoldsNoSecret(t *testing.T) {
	h := newHarness(t)
	h.seed(t)
	raw, err := read(h.factory.Config.Path())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, password) {
		t.Fatalf("the config file holds the password:\n%s", raw)
	}
}

func TestConnectionAddRefusesRatherThanHangs(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "missing host", args: []string{"connection", "add", "primary",
			"--engine", "sqlserver", "--auth", "sqllogin", "--username", "reader"}},
		{name: "missing password", args: []string{"connection", "add", "primary",
			"--engine", "sqlserver", "--host", "example.internal",
			"--auth", "sqllogin", "--username", "reader"}},
		{name: "missing username", args: []string{"connection", "add", "primary",
			"--engine", "sqlserver", "--host", "example.internal", "--auth", "sqllogin"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			err := h.run(append(tt.args, "--no-input")...)
			var invalid *cmdutil.ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("error = %v, want ValidationError", err)
			}
			if cmdutil.ExitCode(err) != cmdutil.ExitValidation {
				t.Errorf("exit code = %d", cmdutil.ExitCode(err))
			}
		})
	}
}

func TestConnectionAddRejectsBadValues(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "unknown engine", args: []string{"--engine", "oracle",
			"--host", "example.internal", "--auth", "sqllogin"}},
		{name: "auth the engine rejects", args: []string{"--engine", "postgres",
			"--host", "example.internal", "--auth", "sqllogin"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			args := append([]string{"connection", "add", "primary", "--no-input"}, tt.args...)
			err := h.run(args...)
			var invalid *config.InvalidError
			if !errors.As(err, &invalid) {
				t.Fatalf("error = %v, want InvalidError", err)
			}
		})
	}
}

// TestProbeRunsBeforeAnythingIsStored: a refusal must leave no trace behind.
func TestProbeRunsBeforeAnythingIsStored(t *testing.T) {
	refused := errors.New("login failed")
	h := newHarness(t)
	h.factory.Probes.Connection = func(context.Context, string, config.Connection,
		credentials.Secret) error {
		return refused
	}
	h.in.WriteString(password + "\n")
	err := h.run("connection", "add", "primary", "--engine", "sqlserver",
		"--host", "example.internal", "--auth", "sqllogin", "--username", "reader",
		"--password-stdin", "--no-input")
	if !errors.Is(err, refused) {
		t.Fatalf("error = %v, want the probe's refusal", err)
	}
	if _, err := h.factory.Config.Connection("primary"); err == nil {
		t.Error("a refused connection was stored anyway")
	}
	if len(h.store.Values) != 0 {
		t.Error("a refused connection's secret was stored anyway")
	}
}

func TestNoVerifySkipsTheProbe(t *testing.T) {
	h := newHarness(t)
	called := false
	h.factory.Probes.Connection = func(context.Context, string, config.Connection,
		credentials.Secret) error {
		called = true
		return errors.New("should not run")
	}
	h.in.WriteString(password + "\n")
	err := h.run("connection", "add", "primary", "--engine", "sqlserver",
		"--host", "example.internal", "--auth", "sqllogin", "--username", "reader",
		"--password-stdin", "--no-verify", "--no-input")
	if err != nil {
		t.Fatalf("connection add: %v", err)
	}
	if called {
		t.Error("--no-verify still ran the probe")
	}
}

func TestConnectionListAndShow(t *testing.T) {
	h := newHarness(t)
	h.seed(t)

	if err := h.run("connection", "list", "-o", "json"); err != nil {
		t.Fatal(err)
	}
	var listed []map[string]any
	if err := json.Unmarshal(h.out.Bytes(), &listed); err != nil {
		t.Fatalf("list output is not json: %v\n%s", err, h.out.String())
	}
	if len(listed) != 1 || listed[0]["name"] != "primary" {
		t.Errorf("listed = %v", listed)
	}

	if err := h.run("connection", "show", "primary", "-o", "json"); err != nil {
		t.Fatal(err)
	}
	var shown map[string]any
	if err := json.Unmarshal(h.out.Bytes(), &shown); err != nil {
		t.Fatalf("show output is not json: %v", err)
	}
	if shown["host"] != "example.internal" {
		t.Errorf("shown = %v", shown)
	}
	for key := range shown {
		if strings.Contains(key, "password") || strings.Contains(key, "secret") {
			t.Errorf("show exposed a secret field %q", key)
		}
	}
}

func TestConnectionShowUnknown(t *testing.T) {
	h := newHarness(t)
	err := h.run("connection", "show", "absent")
	var missing *config.NotFoundError
	if !errors.As(err, &missing) {
		t.Fatalf("error = %v, want NotFoundError", err)
	}
	if cmdutil.ExitCode(err) != cmdutil.ExitNotFound {
		t.Errorf("exit code = %d", cmdutil.ExitCode(err))
	}
}

func TestConnectionRemove(t *testing.T) {
	h := newHarness(t)
	h.seed(t)

	err := h.run("connection", "remove", "primary")
	var inUse *config.InUseError
	if !errors.As(err, &inUse) {
		t.Fatalf("error = %v, want InUseError while a profile binds it", err)
	}
	if err := h.run("connection", "remove", "primary", "--force"); err != nil {
		t.Fatalf("forced remove: %v", err)
	}
	if _, err := h.factory.Config.Connection("primary"); err == nil {
		t.Error("the connection survived")
	}
	if _, ok := h.store.Values[credentials.DBKey("primary")]; ok {
		t.Error("the password was left behind")
	}
}

// TestConnectionAddTakesTheSecretFromTheEnvironment walks the remedy the
// refusal advertises: no terminal, no stdin, only the variable it names. The
// secret must reach the store and never the config file.
func TestConnectionAddTakesTheSecretFromTheEnvironment(t *testing.T) {
	h := newHarness(t)
	h.setenv(credentials.EnvName(credentials.DBKey("local")), password)
	err := h.run("connection", "add", "local", "--engine", "postgres", "--auth", "scram",
		"--host", "db.example.internal", "--database", "appcore", "--username", "reader",
		"--no-input")
	if err != nil {
		t.Fatalf("connection add: %v", err)
	}
	if got := h.store.Values[credentials.DBKey("local")]; got != password {
		t.Errorf("stored secret = %q, want the value the environment supplied", got)
	}
	raw, err := read(h.factory.Config.Path())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, password) {
		t.Fatalf("the config file holds the password:\n%s", raw)
	}
}

// TestStdinBeatsTheEnvironment: an explicit flag is the most deliberate thing
// the user did, so it wins over a variable that may be left over from a shell.
func TestStdinBeatsTheEnvironment(t *testing.T) {
	h := newHarness(t)
	h.setenv(credentials.EnvName(credentials.DBKey("local")), "arrived-in-env")
	h.in.WriteString("arrived-on-stdin\n")
	err := h.run("connection", "add", "local", "--engine", "postgres", "--auth", "scram",
		"--host", "db.example.internal", "--username", "reader",
		"--password-stdin", "--no-input")
	if err != nil {
		t.Fatalf("connection add: %v", err)
	}
	if got := h.store.Values[credentials.DBKey("local")]; got != "arrived-on-stdin" {
		t.Errorf("stored secret = %q, want the value from stdin", got)
	}
}

// TestKerberosStoresNothing: the ticket cache is the credential, so no password
// is asked for and no empty entry is written to the store.
func TestKerberosStoresNothing(t *testing.T) {
	engines := []string{"sqlserver", "postgres"}
	for _, engine := range engines {
		t.Run(engine, func(t *testing.T) {
			h := newHarness(t)
			// A stray variable must not be picked up either: kerberos reads
			// nothing at all.
			h.setenv(credentials.EnvName(credentials.DBKey("realm")), "not-a-password")
			err := h.run("connection", "add", "realm", "--engine", engine,
				"--auth", "kerberos", "--host", "db.example.internal", "--no-input")
			if err != nil {
				t.Fatalf("connection add: %v", err)
			}
			entry, err := h.factory.Config.Connection("realm")
			if err != nil {
				t.Fatal(err)
			}
			if entry.Auth != config.Kerberos {
				t.Errorf("auth = %q", entry.Auth)
			}
			if len(h.store.Values) != 0 {
				t.Errorf("kerberos wrote to the store: %v", h.store.Values)
			}
		})
	}
}

// TestAnEnvironmentSecretSurvivesAnUnusableKeychain: the environment supplies
// it again next run, so a keychain that cannot hold it only warns. A secret
// with no second source still fails the command.
func TestAnEnvironmentSecretSurvivesAnUnusableKeychain(t *testing.T) {
	broken := &credentials.KeychainError{Op: "write", Key: credentials.DBKey("local"),
		Err: errors.New("dbus is not running"), Remedy: "use the environment"}

	t.Run("from the environment", func(t *testing.T) {
		h := newHarness(t)
		h.store.Err = broken
		h.setenv(credentials.EnvName(credentials.DBKey("local")), password)
		err := h.run("connection", "add", "local", "--engine", "postgres", "--auth", "scram",
			"--host", "db.example.internal", "--username", "reader", "--no-input")
		if err != nil {
			t.Fatalf("connection add: %v", err)
		}
		if _, err := h.factory.Config.Connection("local"); err != nil {
			t.Errorf("the connection was not defined: %v", err)
		}
		if !strings.Contains(h.errOut.String(), "was not stored") {
			t.Errorf("nothing warned about the unstored secret: %q", h.errOut.String())
		}
		if strings.Contains(h.errOut.String(), password) {
			t.Error("the warning leaked the secret")
		}
	})

	t.Run("from stdin", func(t *testing.T) {
		h := newHarness(t)
		h.store.Err = broken
		h.in.WriteString(password + "\n")
		err := h.run("connection", "add", "local", "--engine", "postgres", "--auth", "scram",
			"--host", "db.example.internal", "--username", "reader",
			"--password-stdin", "--no-input")
		if !errors.Is(err, broken) {
			t.Fatalf("error = %v, want the keychain refusal", err)
		}
		if _, err := h.factory.Config.Connection("local"); err == nil {
			t.Error("the connection was defined although its secret was lost")
		}
	})
}

// TestAnUnreachableHostStoresWithAWarning is the other half of verify before
// store: an unreachable dependency is ambiguous - the machine may be off its
// network - so the entry is recorded and the user told it was not proved.
func TestAnUnreachableHostStoresWithAWarning(t *testing.T) {
	h := newHarness(t)
	h.factory.Probes.Connection = func(context.Context, string, config.Connection,
		credentials.Secret) error {
		return &connect.ConnectError{Name: "local", Engine: config.Postgres,
			Err: errors.New("dial tcp: connection refused")}
	}
	h.in.WriteString(password + "\n")
	err := h.run("connection", "add", "local", "--engine", "postgres", "--auth", "scram",
		"--host", "db.example.internal", "--username", "reader",
		"--password-stdin", "--no-input")
	if err != nil {
		t.Fatalf("connection add: %v", err)
	}
	if _, err := h.factory.Config.Connection("local"); err != nil {
		t.Errorf("an unreachable host was not recorded: %v", err)
	}
	if !strings.Contains(h.errOut.String(), "stored without verifying") {
		t.Errorf("nothing warned that it was not verified: %q", h.errOut.String())
	}
}

// TestADefinitiveRejectionRefuses covers the classes that mean the settings
// themselves are wrong: storing them would only defer the same failure.
func TestADefinitiveRejectionRefuses(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "unsupported setup",
			err: &connect.UnsupportedError{Configuration: "cross-realm kerberos"}},
		{name: "credential cache",
			err: &connect.CredentialCacheError{Path: "/tmp/krb5cc", Type: "API"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.factory.Probes.Connection = func(context.Context, string, config.Connection,
				credentials.Secret) error {
				return tt.err
			}
			h.in.WriteString(password + "\n")
			err := h.run("connection", "add", "local", "--engine", "postgres",
				"--auth", "scram", "--host", "db.example.internal",
				"--username", "reader", "--password-stdin", "--no-input")
			if !errors.Is(err, tt.err) {
				t.Fatalf("error = %v, want the probe's refusal", err)
			}
			if _, err := h.factory.Config.Connection("local"); err == nil {
				t.Error("a refused connection was stored anyway")
			}
			if len(h.store.Values) != 0 {
				t.Error("a refused connection's secret was stored anyway")
			}
		})
	}
}
