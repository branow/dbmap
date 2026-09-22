package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/branow/dbmap/internal/output"
)

func TestRootResolvesFlagsBeforeACommandRuns(t *testing.T) {
	h := newHarness(t)
	if err := h.run("version", "-o", "json", "--quiet", "--force"); err != nil {
		t.Fatal(err)
	}
	if h.factory.Flags.Output != output.JSON {
		t.Errorf("output = %q", h.factory.Flags.Output)
	}
	if !h.factory.Flags.Quiet || !h.factory.Flags.Force {
		t.Errorf("flags = %+v", h.factory.Flags)
	}
}

func TestRootRejectsAnUnknownFormat(t *testing.T) {
	h := newHarness(t)
	if err := h.run("version", "-o", "yaml"); err == nil {
		t.Fatal("an unknown format was accepted")
	}
}

func TestVersion(t *testing.T) {
	h := newHarness(t)
	if err := h.run("version", "-o", "json"); err != nil {
		t.Fatal(err)
	}
	var shown map[string]any
	if err := json.Unmarshal(h.out.Bytes(), &shown); err != nil {
		t.Fatalf("version output is not json: %v", err)
	}
	if shown["version"] != "test" {
		t.Errorf("version = %v", shown["version"])
	}
}

func TestCompletion(t *testing.T) {
	for _, shell := range shellNames() {
		t.Run(shell, func(t *testing.T) {
			h := newHarness(t)
			if err := h.run("completion", shell); err != nil {
				t.Fatalf("completion %s: %v", shell, err)
			}
			if !strings.Contains(h.out.String(), "dbmap") {
				t.Errorf("the %s script does not mention dbmap", shell)
			}
		})
	}
}

func TestCompletionRejectsAnUnknownShell(t *testing.T) {
	h := newHarness(t)
	if err := h.run("completion", "csh"); err == nil {
		t.Fatal("an unknown shell was accepted")
	}
}

// TestQuietDropsNotes: a quiet run still does the work, it just says nothing.
func TestQuietDropsNotes(t *testing.T) {
	h := newHarness(t)
	if err := h.run("backend", "add", "main", "--provider", "claudecode",
		"--quiet", "--no-input"); err != nil {
		t.Fatal(err)
	}
	if h.out.Len() != 0 {
		t.Errorf("a quiet run wrote %q", h.out.String())
	}
	if _, err := h.factory.Config.Backend("main"); err != nil {
		t.Errorf("the quiet run did not do the work: %v", err)
	}
}

func TestUsageIsNotPrintedOnAnError(t *testing.T) {
	h := newHarness(t)
	if err := h.run("connection", "show", "absent"); err == nil {
		t.Fatal("want an error")
	}
	if strings.Contains(h.out.String()+h.errOut.String(), "Usage:") {
		t.Error("cobra printed usage for a runtime error")
	}
}
