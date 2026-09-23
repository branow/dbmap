package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/branow/dbmap/llm"
)

const schema = `{"type":"object","properties":{"sentence":{"type":"string"}}}`

func request() llm.Request {
	return llm.Request{
		System: "you are a database cartographer",
		Prompt: "describe dbo.Example",
		Schema: json.RawMessage(schema),
	}
}

// stubbed is where a stub run left its record: the argv it was given and the
// prompt it read on stdin.
type stubbed struct {
	dir string
}

// argv is the flags the stub was called with, one per element.
func (s stubbed) argv(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(s.dir, "args"))
	if err != nil {
		t.Fatalf("the stub did not run: %v", err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

// stdin is what the stub read on standard input.
func (s stubbed) stdin(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(s.dir, "stdin"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// stub puts an executable named `claude` on PATH that records its invocation,
// prints the given stdout and exits with the given code. No live model call
// happens in any test.
func stub(t *testing.T, stdout string, exit int) stubbed {
	t.Helper()
	dir := t.TempDir()
	payload := filepath.Join(dir, "stdout")
	if err := os.WriteFile(payload, []byte(stdout), 0o600); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$@\" > " + filepath.Join(dir, "args") + "\n" +
		"cat > " + filepath.Join(dir, "stdin") + "\n" +
		"cat " + payload + "\n" +
		"exit " + strconv.Itoa(exit) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	// The stub directory goes first, so it shadows any real `claude`; the rest
	// of PATH stays so the script itself can reach the standard tools.
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return stubbed{dir: dir}
}

func client(t *testing.T) llm.Client {
	t.Helper()
	c, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

const success = `{"type":"result","subtype":"success","is_error":false,
	"result":"done","structured_output":{"sentence":"Reads dbo.Example."},
	"total_cost_usd":0.0031,
	"usage":{"input_tokens":120,"output_tokens":24,"cache_read_input_tokens":40},
	"session_id":"s1"}`

// The golden test: the prompt goes on stdin, the schema goes in a temp file,
// the run is pinned to a single tool-less turn, and .structured_output comes
// back as the answer.
func TestCompleteRoundTrip(t *testing.T) {
	record := stub(t, success, 0)

	resp, err := client(t).Complete(context.Background(), request())
	if err != nil {
		t.Fatalf("err = %v", err)
	}

	args := record.argv(t)
	for _, pair := range [][2]string{
		{"--output-format", "json"},
		{"--allowedTools", ""},
		{"--model", llm.DefaultModel},
		{"--append-system-prompt", request().System},
	} {
		if !hasPair(args, pair[0], pair[1]) {
			t.Fatalf("argv %v is missing %q %q", args, pair[0], pair[1])
		}
	}

	// Structured output arrives as a tool call, so capping turns at one cuts
	// the answer off: the run returns error_max_turns with no structured output
	// at all. This flag must never come back.
	if value(args, "--max-turns") != "" {
		t.Fatalf("argv carries --max-turns; it truncates the structured answer: %v", args)
	}
	if got := record.stdin(t); got != request().Prompt {
		t.Fatalf("stdin = %q, want the prompt", got)
	}

	if string(resp.Structured) != `{"sentence":"Reads dbo.Example."}` {
		t.Fatalf("structured = %s", resp.Structured)
	}
	if resp.Model != llm.DefaultModel {
		t.Fatalf("model = %q", resp.Model)
	}
	want := llm.Usage{InputTokens: 120, OutputTokens: 24, CacheReadTokens: 40, Cost: 0.0031}
	if resp.Usage != want {
		t.Fatalf("usage = %+v, want %+v", resp.Usage, want)
	}
}

// The CLI reports what it knows and nothing more; the missing fields stay zero
// rather than being invented.
func TestUsageComesBackMostlyZeroed(t *testing.T) {
	stub(t, `{"type":"result","subtype":"success","is_error":false,
		"structured_output":{"sentence":"ok"}}`, 0)

	resp, err := client(t).Complete(context.Background(), request())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if (resp.Usage != llm.Usage{}) {
		t.Fatalf("usage = %+v, want zero", resp.Usage)
	}
}

// The CLI takes the schema itself on --json-schema and rejects a path with
// "--json-schema is not valid JSON". Passing a filename was a real bug: every
// batch failed, the index came back fully described-as-missing, and nothing in
// the suite noticed because the stub accepted any argument at all.
func TestSchemaTravelsInlineAsJSON(t *testing.T) {
	record := stub(t, success, 0)

	if _, err := client(t).Complete(context.Background(), request()); err != nil {
		t.Fatalf("err = %v", err)
	}

	got := value(record.argv(t), "--json-schema")
	if got == "" {
		t.Fatal("argv carries no --json-schema")
	}
	if !json.Valid([]byte(got)) {
		t.Fatalf("--json-schema = %q, which is not JSON; the CLI refuses a path", got)
	}
	if got != schema {
		t.Fatalf("--json-schema = %s, want the request schema %s", got, schema)
	}
}

func TestErrorClassMapping(t *testing.T) {
	cases := []struct {
		name     string
		stdout   string
		exit     int
		sentinel error
	}{
		{
			name:   "max turns is a refusal",
			stdout: `{"type":"result","subtype":"error_max_turns","is_error":true}`,
			exit:   1, sentinel: llm.ErrRefused,
		},
		{
			name:   "execution failure is transient",
			stdout: `{"type":"result","subtype":"error_during_execution","is_error":true}`,
			exit:   1, sentinel: llm.ErrUnavailable,
		},
		{
			name:   "an unknown failure is transient",
			stdout: `{"type":"result","subtype":"error_other","is_error":true}`,
			exit:   1, sentinel: llm.ErrUnavailable,
		},
		{
			name:   "no structured output is a schema failure",
			stdout: `{"type":"result","subtype":"success","is_error":false,"result":"sorry"}`,
			exit:   0, sentinel: llm.ErrSchema,
		},
		{
			name: "unparseable output is transient", stdout: "segmentation fault",
			exit: 2, sentinel: llm.ErrUnavailable,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stub(t, c.stdout, c.exit)
			_, err := client(t).Complete(context.Background(), request())
			if !errors.Is(err, c.sentinel) {
				t.Fatalf("err = %v, want %v", err, c.sentinel)
			}
			var e *llm.Error
			if !errors.As(err, &e) {
				t.Fatalf("err = %v is not an *llm.Error", err)
			}
			if e.Provider != Name {
				t.Fatalf("provider = %q", e.Provider)
			}
		})
	}
}

// A missing binary is configuration, not weather: retrying it forever would
// hide the real problem.
func TestMissingBinaryIsABadRequest(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	c, err := New(Config{Command: "dbmap-absent-binary"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Complete(context.Background(), request())
	if !errors.Is(err, llm.ErrBadRequest) {
		t.Fatalf("err = %v", err)
	}
	if llm.Retryable(err) {
		t.Fatal("a missing binary was called retryable")
	}
}

func TestCancelledContextIsUnavailable(t *testing.T) {
	stub(t, success, 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client(t).Complete(ctx, request())
	if !errors.Is(err, llm.ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("the cancellation was lost: %v", err)
	}
}

func TestName(t *testing.T) {
	if client(t).Name() != "claudecode" {
		t.Fatalf("Name = %q", client(t).Name())
	}
}

func hasPair(args []string, flag, value string) bool {
	for i, arg := range args {
		if arg == flag && i+1 < len(args) && args[i+1] == value {
			return true
		}
	}
	return false
}

func value(args []string, flag string) string {
	for i, arg := range args {
		if arg == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}
