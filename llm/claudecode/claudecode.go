// Package claudecode drives the headless `claude` binary as a pure model. It
// needs no API key, using the user's existing CLI session; the price is that
// usage comes back mostly zeroed, and this package never fabricates the rest.
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

// Name is the provider's identity.
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

// result is the shape of `claude --output-format json`.
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

// subtypes maps the CLI's failure vocabulary onto llm classes; anything
// unlisted is transient.
var subtypes = map[string]llm.Class{
	"error_max_turns":        llm.ClassRefused,
	"error_during_execution": llm.ClassUnavailable,
}

func (p *provider) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	model := req.Model
	if model == "" {
		model = p.cfg.Model
	}

	// The schema travels inline: --json-schema takes the schema itself and
	// rejects a path. No --max-turns: structured output arrives as a tool call,
	// so capping turns returns error_max_turns with no answer.
	args := []string{
		"--print",
		"--output-format", "json",
		"--json-schema", string(req.Schema),
		"--allowedTools", "",
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

// launchFault classifies a run that produced no parseable result. A missing or
// unexecutable binary is a configuration fault; everything else is transient.
func launchFault(err error, model, detail string) error {
	class := llm.ClassUnavailable
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) ||
		errors.Is(err, os.ErrPermission) {
		class = llm.ClassBadRequest
	}
	return &llm.Error{Class: class, Provider: Name, Model: model, Detail: detail, Err: err}
}
