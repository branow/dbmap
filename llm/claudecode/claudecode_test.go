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
		{"--max-turns", "1"},
		{"--model", llm.DefaultModel},
		{"--append-system-prompt", request().System},
	} {
		if !hasPair(args, pair[0], pair[1]) {
			t.Fatalf("argv %v is missing %q %q", args, pair[0], pair[1])
		}
	}

	// The schema travels as a file, never as an argv string: a batch schema
	// plus a 40,000-character prompt exceeds argv limits on some platforms.
	file := value(args, "--json-schema")
	if file == "" || strings.Contains(file, "{") {
		t.Fatalf("--json-schema = %q, want a file path", file)
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

func TestSchemaFileHoldsTheRequestSchema(t *testing.T) {
	record := stub(t, success, 0)
	// The stub copies the schema file aside, because the provider deletes it
	// as soon as the run finishes. Argv is --print --output-format json
	// --json-schema <file>, so the file is the fifth argument.
	dir := record.dir
	script := "#!/bin/sh\n" +
		"shift 4; cp \"$1\" " + filepath.Join(dir, "schema") + "\n" +
		"cat " + filepath.Join(dir, "stdout") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	if _, err := client(t).Complete(context.Background(), request()); err != nil {
		t.Fatalf("err = %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "schema"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != schema {
		t.Fatalf("schema file = %s, want %s", got, schema)
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
