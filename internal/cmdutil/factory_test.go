package cmdutil

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/branow/dbmap/internal/config"
	"github.com/branow/dbmap/internal/credentials"
	"github.com/branow/dbmap/internal/iostreams"
	"github.com/branow/dbmap/internal/output"
)

func factory(t *testing.T) (*Factory, *bytes.Buffer) {
	t.Helper()
	streams, _, out, _ := iostreams.Test()
	cfg := config.New()
	cfg.SetEnv(func(string) string { return "" })
	f := &Factory{IO: streams, Config: cfg, Store: credentials.NewFake()}
	if err := f.Resolve(config.Overrides{}); err != nil {
		t.Fatal(err)
	}
	return f, out
}

func TestResolveFillsFlags(t *testing.T) {
	streams, _, _, _ := iostreams.Test()
	cfg := config.New()
	cfg.SetEnv(func(name string) string {
		if name == config.EnvOutput {
			return "json"
		}
		return ""
	})
	f := &Factory{IO: streams, Config: cfg, Store: credentials.NewFake()}
	quiet := true
	if err := f.Resolve(config.Overrides{Quiet: &quiet}); err != nil {
		t.Fatal(err)
	}
	if f.Flags.Output != output.JSON {
		t.Errorf("output = %q, want json", f.Flags.Output)
	}
	if !f.Flags.Quiet {
		t.Error("quiet was not resolved")
	}
}

func TestResolveRefusesAnUnknownFormat(t *testing.T) {
	streams, _, _, _ := iostreams.Test()
	cfg := config.New()
	cfg.SetEnv(func(string) string { return "" })
	f := &Factory{IO: streams, Config: cfg}
	err := f.Resolve(config.Overrides{Output: "tsv"})
	var invalid *ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("error = %v, want ValidationError", err)
	}
}

func TestResolveDisablesPrompting(t *testing.T) {
	streams, _, _, _ := iostreams.Test()
	streams.SetStdinTTY(true)
	streams.SetStdoutTTY(true)
	cfg := config.New()
	cfg.SetEnv(func(string) string { return "" })
	f := &Factory{IO: streams, Config: cfg}
	noInput := true
	if err := f.Resolve(config.Overrides{NoInput: &noInput}); err != nil {
		t.Fatal(err)
	}
	if streams.CanPrompt() {
		t.Error("--no-input did not reach the streams")
	}
}

func TestNoteIsSilentWhenQuiet(t *testing.T) {
	f, buf := factory(t)
	if err := f.Note("done"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "done") {
		t.Error("the note was not written")
	}
	f.Flags.Quiet = true
	before := buf.String()
	if err := f.Note("second"); err != nil {
		t.Fatal(err)
	}
	if buf.String() != before {
		t.Error("a quiet run still wrote a note")
	}
}

func TestEnvSecret(t *testing.T) {
	f, _ := factory(t)
	f.Env = func(name string) string {
		if name == credentials.EnvName(credentials.DBKey("local")) {
			return "hunter2"
		}
		return ""
	}
	secret, ok := f.EnvSecret(credentials.DBKey("local"))
	if !ok || secret.Reveal() != "hunter2" {
		t.Fatalf("secret = %v, found = %v", secret, ok)
	}
	if _, ok := f.EnvSecret(credentials.DBKey("other")); ok {
		t.Error("an unset variable reported a secret")
	}
}
