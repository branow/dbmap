package cmd

import (
	"strings"
	"testing"

	"github.com/branow/dbmap/internal/config"
)

// A profile that binds a removed connection must not be able to brick the CLI.
// Validation once refused to load such a config at all, so every command failed
// — including the ones that would have repaired it.
func TestADanglingProfileDoesNotBreakEveryCommand(t *testing.T) {
	// Built by hand: a profile whose bindings were removed after it was made.
	cfg := config.New()
	cfg.Profiles["dev"] = config.Profile{Connection: "gone", Backend: "also-gone"}
	cfg.CurrentProfile = "dev"

	if err := cfg.Validate(); err != nil {
		t.Fatalf("a dangling binding made the whole config unloadable: %v", err)
	}
}

func TestProfileRemoveClearsTheCurrentOne(t *testing.T) {
	cfg := config.New()
	cfg.Profiles["dev"] = config.Profile{Connection: "c", Backend: "b"}
	if err := cfg.Switch("dev"); err != nil {
		t.Fatal(err)
	}

	if err := cfg.RemoveProfile("dev"); err != nil {
		t.Fatalf("RemoveProfile: %v", err)
	}
	if cfg.CurrentProfile != "" {
		t.Errorf("current profile = %q, want it cleared", cfg.CurrentProfile)
	}
	if err := cfg.RemoveProfile("dev"); err == nil ||
		!strings.Contains(err.Error(), "not defined") {
		t.Errorf("removing it twice returned %v, want a not-defined error", err)
	}
}
