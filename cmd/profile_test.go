package cmd

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/branow/dbmap/internal/cmdutil"
	"github.com/branow/dbmap/internal/config"
)

// TestCreateDoesNotActivate is the one-command-one-effect rule: create writes
// settings, switch changes what is current, and neither does the other's job.
func TestCreateDoesNotActivate(t *testing.T) {
	h := newHarness(t)
	h.seed(t)
	if h.factory.Config.CurrentProfile != "" {
		t.Fatalf("current profile = %q after create, want none",
			h.factory.Config.CurrentProfile)
	}
	if err := h.run("profile", "switch", "work"); err != nil {
		t.Fatalf("switch: %v", err)
	}
	if h.factory.Config.CurrentProfile != "work" {
		t.Errorf("current profile = %q after switch", h.factory.Config.CurrentProfile)
	}
	saved, err := config.LoadFrom(h.factory.Config.Path())
	if err != nil {
		t.Fatal(err)
	}
	if saved.CurrentProfile != "work" {
		t.Errorf("the switch was not written to the file: %q", saved.CurrentProfile)
	}
}

func TestProfileCreateRefusesUnknownBindings(t *testing.T) {
	h := newHarness(t)
	err := h.run("profile", "create", "work", "--connection", "absent",
		"--backend", "absent", "--no-input")
	var missing *config.NotFoundError
	if !errors.As(err, &missing) {
		t.Fatalf("error = %v, want NotFoundError", err)
	}
}

func TestProfileSwitchUnknown(t *testing.T) {
	h := newHarness(t)
	err := h.run("profile", "switch", "absent")
	if cmdutil.ExitCode(err) != cmdutil.ExitNotFound {
		t.Fatalf("exit code = %d for %v", cmdutil.ExitCode(err), err)
	}
}

func TestProfileList(t *testing.T) {
	h := newHarness(t)
	h.seed(t)
	if err := h.run("profile", "switch", "work"); err != nil {
		t.Fatal(err)
	}
	if err := h.run("profile", "list", "-o", "json"); err != nil {
		t.Fatal(err)
	}
	var listed []map[string]any
	if err := json.Unmarshal(h.out.Bytes(), &listed); err != nil {
		t.Fatalf("list output is not json: %v", err)
	}
	if len(listed) != 1 || listed[0]["current"] != true {
		t.Errorf("listed = %v", listed)
	}
}

func TestProfileShowWithNothingCurrent(t *testing.T) {
	h := newHarness(t)
	h.seed(t)
	err := h.run("profile", "show")
	var missing *cmdutil.NotConfiguredError
	if !errors.As(err, &missing) {
		t.Fatalf("error = %v, want NotConfiguredError", err)
	}
	if !strings.Contains(err.Error(), "dbmap profile") {
		t.Errorf("the error does not say what to do: %v", err)
	}
}

func TestProfileShowResolvesBindings(t *testing.T) {
	h := newHarness(t)
	h.seed(t)
	if err := h.run("profile", "show", "work", "-o", "json"); err != nil {
		t.Fatal(err)
	}
	var shown map[string]any
	if err := json.Unmarshal(h.out.Bytes(), &shown); err != nil {
		t.Fatalf("show output is not json: %v", err)
	}
	if shown["host"] != "example.internal" || shown["provider"] != "claudecode" {
		t.Errorf("shown = %v", shown)
	}
}

// TestProfileFlagSelectsWithoutSwitching: --profile changes this invocation
// only, and leaves the stored current profile alone.
func TestProfileFlagSelectsWithoutSwitching(t *testing.T) {
	h := newHarness(t)
	h.seed(t)
	if err := h.run("profile", "show", "--profile", "work", "-o", "json"); err != nil {
		t.Fatal(err)
	}
	if h.factory.Config.CurrentProfile != "" {
		t.Errorf("--profile changed the stored current profile to %q",
			h.factory.Config.CurrentProfile)
	}
}
