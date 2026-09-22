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

func TestBackendAddFromFlags(t *testing.T) {
	h := newHarness(t)
	h.in.WriteString(apiKey + "\n")
	err := h.run("backend", "add", "main", "--provider", "anthropic",
		"--model", "model-a", "--base-url", "https://api.example.internal",
		"--api-key-stdin", "--no-input")
	if err != nil {
		t.Fatalf("backend add: %v", err)
	}
	entry, err := h.factory.Config.Backend("main")
	if err != nil {
		t.Fatal(err)
	}
	if entry.Model != "model-a" || entry.BaseURL != "https://api.example.internal" {
		t.Errorf("stored backend = %+v", entry)
	}
	if got := h.store.Values[credentials.LLMKey("main")]; got != apiKey {
		t.Errorf("stored api key = %q", got)
	}
	if h.factory.Config.CurrentProfile != "" {
		t.Error("adding a backend activated something")
	}
	raw, err := read(h.factory.Config.Path())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, apiKey) {
		t.Fatalf("the config file holds the api key:\n%s", raw)
	}
}

// TestBackendWithoutAKeyNeedsNoSecret: headless claude code authenticates on
// its own, so nothing is asked for and nothing is stored.
func TestBackendWithoutAKeyNeedsNoSecret(t *testing.T) {
	h := newHarness(t)
	if err := h.run("backend", "add", "local", "--provider", "claudecode",
		"--no-input"); err != nil {
		t.Fatalf("backend add: %v", err)
	}
	if len(h.store.Values) != 0 {
		t.Errorf("stored a secret for a keyless provider: %v", h.store.Values)
	}
}

func TestBackendAddRefusesWithoutAKey(t *testing.T) {
	h := newHarness(t)
	err := h.run("backend", "add", "main", "--provider", "openai", "--no-input")
	var invalid *cmdutil.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("error = %v, want ValidationError", err)
	}
	if !strings.Contains(invalid.Reason, "DBMAP_SECRET_LLM_MAIN") {
		t.Errorf("the refusal does not name the environment variable: %s", invalid.Reason)
	}
}

func TestBackendAddPrompts(t *testing.T) {
	h := newHarness(t)
	h.interactive("anthropic\nmodel-a\n\n" + apiKey + "\n")
	if err := h.run("backend", "add", "main"); err != nil {
		t.Fatalf("backend add: %v", err)
	}
	entry, err := h.factory.Config.Backend("main")
	if err != nil {
		t.Fatal(err)
	}
	if entry.Provider != config.Anthropic || entry.Model != "model-a" {
		t.Errorf("prompted backend = %+v", entry)
	}
	if strings.Contains(h.out.String()+h.errOut.String(), apiKey) {
		t.Error("the api key was echoed")
	}
}

func TestBackendProbeRunsBeforeStoring(t *testing.T) {
	refused := errors.New("model rejected the key")
	h := newHarness(t)
	h.factory.Probes.Backend = func(context.Context, string, config.Backend,
		credentials.Secret) error {
		return refused
	}
	h.in.WriteString(apiKey + "\n")
	err := h.run("backend", "add", "main", "--provider", "anthropic",
		"--api-key-stdin", "--no-input")
	if !errors.Is(err, refused) {
		t.Fatalf("error = %v, want the probe's refusal", err)
	}
	if _, err := h.factory.Config.Backend("main"); err == nil {
		t.Error("a refused backend was stored anyway")
	}
	if len(h.store.Values) != 0 {
		t.Error("a refused backend's key was stored anyway")
	}
}

func TestBackendListShowRemove(t *testing.T) {
	h := newHarness(t)
	h.seed(t)

	if err := h.run("backend", "list", "-o", "json"); err != nil {
		t.Fatal(err)
	}
	var listed []map[string]any
	if err := json.Unmarshal(h.out.Bytes(), &listed); err != nil {
		t.Fatalf("list output is not json: %v", err)
	}
	if len(listed) != 1 || listed[0]["provider"] != "claudecode" {
		t.Errorf("listed = %v", listed)
	}

	if err := h.run("backend", "show", "main"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.out.String(), "model-a") {
		t.Errorf("show output = %q", h.out.String())
	}

	if err := h.run("backend", "remove", "main", "--force"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := h.factory.Config.Backend("main"); err == nil {
		t.Error("the backend survived")
	}
}

// TestBackendAddTakesTheKeyFromTheEnvironment is the same promise the refusal
// makes for a backend: the variable it names is enough on its own.
func TestBackendAddTakesTheKeyFromTheEnvironment(t *testing.T) {
	h := newHarness(t)
	h.setenv(credentials.EnvName(credentials.LLMKey("main")), apiKey)
	if err := h.run("backend", "add", "main", "--provider", "anthropic",
		"--model", "model-a", "--no-input"); err != nil {
		t.Fatalf("backend add: %v", err)
	}
	if got := h.store.Values[credentials.LLMKey("main")]; got != apiKey {
		t.Errorf("stored api key = %q, want the value the environment supplied", got)
	}
	raw, err := read(h.factory.Config.Path())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, apiKey) {
		t.Fatalf("the config file holds the api key:\n%s", raw)
	}
}
