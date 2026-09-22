// Package claudecode drives the headless `claude` binary as a pure model.
//
// It needs no API key: it uses the user's existing CLI session, which is the
// entire reason it exists as a provider. The price is that usage comes back
// mostly zeroed; this package reports what the CLI gives it and never
// fabricates the rest.
package claudecode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"

	"github.com/branow/dbmap/llm"
)

// Name is the provider's identity, used in cache keys and logs.
const Name = "claudecode"

// DefaultCommand is the binary looked up on PATH when the config names none.
const DefaultCommand = "claude"

// Config constructs a client.
type Config struct {
	// Command is the executable to run. Empty means DefaultCommand.
	Command string
	// Model is the default model. Empty means llm.DefaultModel.
	Model string
}

// New returns a client that shells out to the Claude Code CLI.
func New(cfg Config) (llm.Client, error) {
	if cfg.Command == "" {
		cfg.Command = DefaultCommand
	}
	if cfg.Model == "" {
		cfg.Model = llm.DefaultModel
	}
	return &provider{cfg: cfg}, nil
}

type provider struct {
	cfg Config
}

func (p *provider) Name() string { return Name }

// result is the shape of `claude --output-format json`. Fields the CLI omits
// stay zero, which is exactly what Usage should then report.
type result struct {
	Subtype          string          `json:"subtype"`
	IsError          bool            `json:"is_error"`
	Result           string          `json:"result"`
	StructuredOutput json.RawMessage `json:"structured_output"`
	TotalCostUSD     float64         `json:"total_cost_usd"`
	Usage            struct {
		InputTokens          int `json:"input_tokens"`
		OutputTokens         int `json:"output_tokens"`
		CacheReadInputTokens int `json:"cache_read_input_tokens"`
	} `json:"usage"`
}

// subtypes maps the CLI's own failure vocabulary onto the L1 classes. Anything
// unlisted is treated as transient, because the alternative is string-matching
// the CLI's prose.
var subtypes = map[string]llm.Class{
	"error_max_turns":        llm.ClassRefused,
	"error_during_execution": llm.ClassUnavailable,
}

func (p *provider) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	model := req.Model
	if model == "" {
		model = p.cfg.Model
	}

	// The schema goes in a temp file, not in argv: a batch schema plus a
	// 40,000-character prompt exceeds argv limits on some platforms.
	schema, err := os.CreateTemp("", "dbmap-schema-*.json")
	if err != nil {
		return nil, &llm.Error{
			Class: llm.ClassUnavailable, Provider: Name, Model: model,
			Detail: "could not write the schema file", Err: err,
		}
	}
	defer os.Remove(schema.Name())
	if _, err := schema.Write(req.Schema); err != nil {
		schema.Close()
		return nil, &llm.Error{
			Class: llm.ClassUnavailable, Provider: Name, Model: model,
			Detail: "could not write the schema file", Err: err,
		}
	}
	if err := schema.Close(); err != nil {
		return nil, &llm.Error{
			Class: llm.ClassUnavailable, Provider: Name, Model: model,
			Detail: "could not write the schema file", Err: err,
		}
	}

	// --allowedTools "" and --max-turns 1 keep this a pure model call rather
	// than an agent run.
	args := []string{
		"--print",
		"--output-format", "json",
		"--json-schema", schema.Name(),
		"--allowedTools", "",
		"--max-turns", "1",
		"--model", model,
	}
	if req.System != "" {
		args = append(args, "--append-system-prompt", req.System)
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, p.cfg.Command, args...)
	cmd.Stdin = strings.NewReader(req.Prompt)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	var answer result
	parsed := json.Unmarshal(stdout.Bytes(), &answer) == nil

	if runErr != nil && !parsed {
		return nil, launchFault(runErr, model, strings.TrimSpace(stderr.String()))
	}
	if answer.IsError || (runErr != nil && parsed) {
		class, ok := subtypes[answer.Subtype]
		if !ok {
			class = llm.ClassUnavailable
		}
		return nil, &llm.Error{
			Class: class, Provider: Name, Model: model,
			Detail: answer.Result, Err: runErr,
		}
	}
	if len(answer.StructuredOutput) == 0 || !json.Valid(answer.StructuredOutput) {
		return nil, &llm.Error{
			Class: llm.ClassSchema, Provider: Name, Model: model,
			Detail: "no structured output in the result",
		}
	}

	return &llm.Response{
		Structured: answer.StructuredOutput,
		Model:      model,
		Usage: llm.Usage{
			InputTokens:     answer.Usage.InputTokens,
			OutputTokens:    answer.Usage.OutputTokens,
			CacheReadTokens: answer.Usage.CacheReadInputTokens,
			Cost:            answer.TotalCostUSD,
		},
	}, nil
}

// launchFault classifies a run that produced no parseable result. A binary that
// is missing or not executable is a configuration fault and never retryable;
// everything else is transient.
func launchFault(err error, model, detail string) error {
	class := llm.ClassUnavailable
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) ||
		errors.Is(err, os.ErrPermission) {
		class = llm.ClassBadRequest
	}
	return &llm.Error{Class: class, Provider: Name, Model: model, Detail: detail, Err: err}
}
