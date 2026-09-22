package cmd

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/branow/dbmap/internal/cmdutil"
	"github.com/branow/dbmap/internal/config"
	"github.com/branow/dbmap/internal/output"
)

func TestConfigSetAndGet(t *testing.T) {
	h := newHarness(t)
	if err := h.run("config", "set", "output", "json"); err != nil {
		t.Fatalf("config set: %v", err)
	}
	if err := h.run("config", "get", "output"); err != nil {
		t.Fatalf("config get: %v", err)
	}
	if strings.TrimSpace(h.out.String()) != `"json"` {
		t.Errorf("get output = %q", h.out.String())
	}
	saved, err := config.LoadFrom(h.factory.Config.Path())
	if err != nil {
		t.Fatal(err)
	}
	if saved.DefaultOutput != "json" {
		t.Errorf("stored default output = %q", saved.DefaultOutput)
	}
}

// TestConfigSetIsValidated: the file default feeds the precedence chain, so an
// unusable value is refused where it is typed, not on the next command.
func TestConfigSetIsValidated(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		check func(error) bool
	}{
		{name: "unknown format", args: []string{"config", "set", "output", "tsv"},
			check: func(err error) bool {
				var unknown *output.UnknownFormatError
				return errors.As(err, &unknown)
			}},
		{name: "unknown policy",
			args: []string{"config", "set", "secrets.fallback", "maybe"},
			check: func(err error) bool {
				var invalid *config.InvalidError
				return errors.As(err, &invalid)
			}},
		{name: "unknown key", args: []string{"config", "set", "nope", "x"},
			check: func(err error) bool {
				var missing *config.NotFoundError
				return errors.As(err, &missing)
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			err := h.run(tt.args...)
			if !tt.check(err) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestConfigList(t *testing.T) {
	h := newHarness(t)
	if err := h.run("config", "list", "-o", "json"); err != nil {
		t.Fatal(err)
	}
	var listed []map[string]any
	if err := json.Unmarshal(h.out.Bytes(), &listed); err != nil {
		t.Fatalf("list output is not json: %v", err)
	}
	if len(listed) != len(config.Settings()) {
		t.Errorf("listed %d settings, want %d", len(listed), len(config.Settings()))
	}
}

func TestConfigSetCurrentProfileIsASwitch(t *testing.T) {
	h := newHarness(t)
	h.seed(t)
	if err := h.run("config", "set", "current_profile", "work"); err != nil {
		t.Fatalf("config set: %v", err)
	}
	if h.factory.Config.CurrentProfile != "work" {
		t.Errorf("current profile = %q", h.factory.Config.CurrentProfile)
	}
	err := h.run("config", "set", "current_profile", "absent")
	if cmdutil.ExitCode(err) != cmdutil.ExitNotFound {
		t.Errorf("exit code = %d for %v", cmdutil.ExitCode(err), err)
	}
}
