package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/branow/dbmap/internal/cmdutil"
	"github.com/branow/dbmap/internal/config"
	"github.com/branow/dbmap/internal/credentials"
)

func TestConnectionAddFromFlags(t *testing.T) {
	h := newHarness(t)
	h.in.WriteString("s3cret\n")
	err := h.run("connection", "add", "primary", "--engine", "sqlserver",
		"--host", "example.internal", "--port", "1433", "--database", "AppCore",
		"--auth", "sqllogin", "--username", "reader", "--param", "encrypt=true",
		"--production", "--password-stdin", "--no-input")
	if err != nil {
		t.Fatalf("connection add: %v", err)
	}

	entry, err := h.factory.Config.Connection("primary")
	if err != nil {
		t.Fatal(err)
	}
	if entry.Host != "example.internal" || entry.Port != 1433 || !entry.Production {
		t.Errorf("stored connection = %+v", entry)
	}
	if entry.Params["encrypt"] != "true" {
		t.Errorf("driver params = %v", entry.Params)
	}
	if got := h.store.Values[credentials.DBKey("primary")]; got != "s3cret" {
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
	if strings.Contains(raw, "s3cret") {
		t.Fatalf("the config file holds the password:\n%s", raw)
	}
}

func TestConnectionAddPrompts(t *testing.T) {
	h := newHarness(t)
	h.interactive(strings.Join([]string{
		"postgres",         // engine
		"example.internal", // host
		"AppCore",          // database
		"scram",            // auth
		"reader",           // username
		"n",                // production
		"s3cret",           // password
	}, "\n") + "\n")

	if err := h.run("connection", "add", "primary"); err != nil {
		t.Fatalf("connection add: %v", err)
	}
	entry, err := h.factory.Config.Connection("primary")
	if err != nil {
		t.Fatal(err)
	}
	if entry.Engine != config.Postgres || entry.Auth != config.SCRAM {
		t.Errorf("prompted connection = %+v", entry)
	}
	if entry.Production {
		t.Error("the production answer was not honoured")
	}
	if got := h.store.Values[credentials.DBKey("primary")]; got != "s3cret" {
		t.Errorf("stored secret = %q", got)
	}
	if strings.Contains(h.out.String()+h.errOut.String(), "s3cret") {
		t.Error("the password was echoed")
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

// TestProbeRunsBeforeAnythingIsStored is the verify-before-store seam M2 and M3
// plug a real connection into: a refusal must leave no trace behind.
func TestProbeRunsBeforeAnythingIsStored(t *testing.T) {
	refused := errors.New("login failed")
	h := newHarness(t)
	h.factory.Probes.Connection = func(context.Context, string, config.Connection,
		credentials.Secret) error {
		return refused
	}
	h.in.WriteString("s3cret\n")
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
	h.in.WriteString("s3cret\n")
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

// TestAScriptedRunStatesEveryRequiredValue: a default is offered at a prompt,
// never assumed for a non-interactive run.
func TestAScriptedRunStatesEveryRequiredValue(t *testing.T) {
	h := newHarness(t)
	err := h.run("connection", "add", "primary", "--host", "example.internal",
		"--username", "reader", "--no-input")
	var invalid *cmdutil.ValidationError
	if !errors.As(err, &invalid) || invalid.Field != "--engine" {
		t.Fatalf("error = %v, want a ValidationError naming --engine", err)
	}
}
