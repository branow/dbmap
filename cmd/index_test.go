package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/branow/dbmap/internal/cmdutil"
	"github.com/branow/dbmap/internal/config"
	"github.com/branow/dbmap/internal/index"
	"github.com/branow/dbmap/internal/plan"
	"github.com/branow/dbmap/internal/redact"
	"github.com/branow/dbmap/llm"
)

// flags renders the index command's flag surface, which is the tool's contract
// with a script and is therefore worth pinning.
func TestIndexDeclaresItsDocumentedFlags(t *testing.T) {
	h := newHarness(t)
	cmd := newIndex(h.factory)

	for _, want := range []string{"db", "match", "limit", "samples", "out", "backend", "dry-run"} {
		if cmd.Flags().Lookup(want) == nil {
			t.Fatalf("--%s is missing", want)
		}
	}
	// --force is the root's persistent flag. Declaring it again here would
	// shadow the one the root's pre-run reads.
	if cmd.Flags().Lookup("force") != nil {
		t.Fatalf("--force is declared twice")
	}
}

func TestIndexTakesAtMostOneConnection(t *testing.T) {
	h := newHarness(t)
	cmd := newIndex(h.factory)
	if err := cmd.Args(cmd, []string{"a", "b"}); err == nil {
		t.Fatalf("two connections were accepted")
	}
	if err := cmd.Args(cmd, []string{"a"}); err != nil {
		t.Fatalf("one connection was refused: %v", err)
	}
	if err := cmd.Args(cmd, nil); err != nil {
		t.Fatalf("no connection was refused: %v", err)
	}
}

func TestResolveConnection(t *testing.T) {
	cases := []struct {
		name    string
		named   string
		current string
		want    string
		code    int
	}{
		{"named outright", "primary", "", "primary", cmdutil.ExitOK},
		{"from the active profile", "", "work", "primary", cmdutil.ExitOK},
		{"nothing named and nothing active", "", "", "", cmdutil.ExitNotFound},
		{"a connection that does not exist", "absent", "", "", cmdutil.ExitNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			h.seed(t)
			if c.current != "" {
				if err := h.factory.Config.Switch(c.current); err != nil {
					t.Fatalf("switching: %v", err)
				}
			}

			got, _, err := resolveConnection(h.factory, c.named)

			if cmdutil.ExitCode(err) != c.code {
				t.Fatalf("exit = %d, want %d (%v)", cmdutil.ExitCode(err), c.code, err)
			}
			if got != c.want {
				t.Fatalf("connection = %q, want %q", got, c.want)
			}
		})
	}
}

func TestResolveDatabase(t *testing.T) {
	cases := []struct {
		name     string
		entry    config.Connection
		override string
		want     string
		code     int
	}{
		{"the flag wins", config.Connection{Database: "AppCore"}, "SyncStore",
			"SyncStore", cmdutil.ExitOK},
		{"the connection's default", config.Connection{Database: "AppCore"}, "",
			"AppCore", cmdutil.ExitOK},
		{"neither", config.Connection{}, "", "", cmdutil.ExitValidation},
		{"a path separator", config.Connection{}, "a/b", "", cmdutil.ExitValidation},
		{"a parent reference", config.Connection{}, "..", "", cmdutil.ExitValidation},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := resolveDatabase(c.entry, c.override)
			if cmdutil.ExitCode(err) != c.code {
				t.Fatalf("exit = %d, want %d (%v)", cmdutil.ExitCode(err), c.code, err)
			}
			if got != c.want {
				t.Fatalf("database = %q, want %q", got, c.want)
			}
		})
	}
}

func TestResolveBackend(t *testing.T) {
	cases := []struct {
		name    string
		named   string
		current string
		want    string
		code    int
	}{
		{"named outright", "main", "", "main", cmdutil.ExitOK},
		{"from the active profile", "", "work", "main", cmdutil.ExitOK},
		{"nothing named and nothing active", "", "", "", cmdutil.ExitNotFound},
		{"a backend that does not exist", "absent", "", "", cmdutil.ExitNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			h.seed(t)
			if c.current != "" {
				if err := h.factory.Config.Switch(c.current); err != nil {
					t.Fatalf("switching: %v", err)
				}
			}

			got, _, err := resolveBackend(h.factory, c.named)

			if cmdutil.ExitCode(err) != c.code {
				t.Fatalf("exit = %d, want %d (%v)", cmdutil.ExitCode(err), c.code, err)
			}
			if got != c.want {
				t.Fatalf("backend = %q, want %q", got, c.want)
			}
		})
	}
}

// A dry run never needs a model, which is what lets it run before a backend is
// configured at all.
func TestADryRunNeedsNoBackend(t *testing.T) {
	h := newHarness(t)

	client, model, err := describer(h.factory, &indexOptions{dryRun: true}, t.TempDir())

	if err != nil {
		t.Fatalf("describer: %v", err)
	}
	if client != nil {
		t.Fatalf("a dry run built a model client")
	}
	if model != "" {
		t.Fatalf("model = %q, want none", model)
	}
}

// A real build without a backend fails naming the fix, rather than quietly
// writing an index with no descriptions in it.
func TestABuildWithoutABackendSaysSo(t *testing.T) {
	h := newHarness(t)

	_, _, err := describer(h.factory, &indexOptions{}, t.TempDir())

	var missing *cmdutil.NotConfiguredError
	if !errors.As(err, &missing) {
		t.Fatalf("err = %v, want a NotConfiguredError", err)
	}
	if !strings.Contains(err.Error(), "--backend") {
		t.Fatalf("the fix was not named: %v", err)
	}
}

func TestDescriberComposesTheModuleMiddleware(t *testing.T) {
	h := newHarness(t)
	h.seed(t)
	if err := h.factory.Config.Switch("work"); err != nil {
		t.Fatalf("switching: %v", err)
	}

	client, model, err := describer(h.factory, &indexOptions{}, t.TempDir())

	if err != nil {
		t.Fatalf("describer: %v", err)
	}
	if client == nil {
		t.Fatalf("no client was built")
	}
	// Every wrapper passes the provider's own name through, so the name is
	// proof the provider under the middleware is the configured one.
	if client.Name() != string(config.ClaudeCode) {
		t.Fatalf("Name = %q, want %q", client.Name(), config.ClaudeCode)
	}
	if model != "model-a" {
		t.Fatalf("model = %q, want the backend's", model)
	}
}

// A dry run must not render numbers that would read as a completed build.
func TestReportSaysWhenNothingWasBuilt(t *testing.T) {
	h := newHarness(t)
	if err := h.factory.Resolve(config.Overrides{Output: "json"}); err != nil {
		t.Fatalf("resolving flags: %v", err)
	}

	h.out.Reset()
	if err := report(h.factory, index.Summary{
		Environment: "primary", Database: "AppCore", Dir: "out", DryRun: true,
		Objects: 12, Fetched: 5, Reused: 7,
		Reasons: map[plan.Reason]int{plan.Modified: 5},
	}); err != nil {
		t.Fatalf("report: %v", err)
	}

	out := h.out.String()
	if !strings.Contains(out, `"dry-run": true`) {
		t.Fatalf("a dry run was not marked as one:\n%s", out)
	}
	if strings.Contains(out, "described") {
		t.Fatalf("a dry run reported describe counts:\n%s", out)
	}
	if !strings.Contains(out, "modified 5") {
		t.Fatalf("the fetch reasons are missing:\n%s", out)
	}
}

func TestReportRendersTheWholeSummary(t *testing.T) {
	h := newHarness(t)
	if err := h.factory.Resolve(config.Overrides{Output: "json"}); err != nil {
		t.Fatalf("resolving flags: %v", err)
	}

	h.out.Reset()
	if err := report(h.factory, index.Summary{
		Environment: "primary", Database: "AppCore", Dir: "out",
		Objects: 12, Fetched: 5, Reused: 7, Described: 2, Unchanged: 3, Sampled: 4,
		Dropped: []string{"dbo.Gone"},
		Usage:   llm.Usage{InputTokens: 100, OutputTokens: 20},
	}); err != nil {
		t.Fatalf("report: %v", err)
	}

	out := h.out.String()
	for _, want := range []string{"objects", "fetched", "reused", "described",
		"unchanged", "sampled", "dropped", "redactions", "input tokens"} {
		if !strings.Contains(out, want) {
			t.Fatalf("%q is missing from the summary:\n%s", want, out)
		}
	}
}

// A found credential is named in the summary, not left to a log line.
func TestReportNamesWhatWasRedacted(t *testing.T) {
	h := newHarness(t)
	if err := h.factory.Resolve(config.Overrides{}); err != nil {
		t.Fatalf("resolving flags: %v", err)
	}

	summary := index.Summary{Environment: "primary", Database: "AppCore"}
	// The class is spelled out rather than taken from the rule table: what is
	// under test is the rendering, not which rules exist.
	summary.Redactions = summary.Redactions.Add(redact.Counts{redact.Class("email"): 7})

	h.out.Reset()
	if err := report(h.factory, summary); err != nil {
		t.Fatalf("report: %v", err)
	}
	if !strings.Contains(h.out.String(), "email 7") {
		t.Fatalf("the tally is missing:\n%s", h.out.String())
	}
}

func TestReasonsRenderInThePlannersOrder(t *testing.T) {
	cases := []struct {
		name    string
		counted map[plan.Reason]int
		want    string
	}{
		{"nothing to fetch", nil, "none"},
		{"one reason", map[plan.Reason]int{plan.New: 3}, "new 3"},
		{"every reason, in the order they are asked",
			map[plan.Reason]int{plan.Sample: 1, plan.Modified: 2, plan.New: 3},
			"new 3, modified 2, sample 1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := reasons(c.counted); got != c.want {
				t.Fatalf("reasons = %q, want %q", got, c.want)
			}
		})
	}
}

func TestArg(t *testing.T) {
	if got := arg(nil); got != "" {
		t.Fatalf("arg = %q, want empty", got)
	}
	if got := arg([]string{"primary"}); got != "primary" {
		t.Fatalf("arg = %q, want primary", got)
	}
}
