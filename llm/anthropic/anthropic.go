// Package anthropic talks to the Claude API through the official
// anthropic-sdk-go, constraining the answer with output_config.format.
package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/branow/dbmap/llm"
)

// Name is the provider's identity, used in cache keys and logs.
const Name = "anthropic"

// DefaultMaxTokens caps an answer when neither the request nor the config says
// otherwise. A describe batch answers in a few hundred tokens per object.
const DefaultMaxTokens = 4096

// Config constructs a client. The API key is already resolved by the host
// application; this package never reads a file, an environment variable or a
// keychain.
type Config struct {
	// APIKey is required.
	APIKey string
	// BaseURL overrides the API endpoint. Empty means the SDK's default.
	BaseURL string
	// Model is the default model. Empty means llm.DefaultModel.
	Model string
	// MaxTokens is the default answer cap. 0 means DefaultMaxTokens.
	MaxTokens int
}

// New returns a client for the Claude API. Retries are turned off in the SDK:
// retrying is llm.WithRetry's job, and two layers of it would multiply.
func New(cfg Config) (llm.Client, error) {
	if cfg.APIKey == "" {
		return nil, &llm.Error{Class: llm.ClassAuth, Provider: Name, Detail: "no api key"}
	}
	if cfg.Model == "" {
		cfg.Model = llm.DefaultModel
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = DefaultMaxTokens
	}
	options := []option.RequestOption{
		option.WithAPIKey(cfg.APIKey),
		option.WithMaxRetries(0),
	}
	if cfg.BaseURL != "" {
		options = append(options, option.WithBaseURL(cfg.BaseURL))
	}
	client := sdk.NewClient(options...)
	return &provider{api: &client, cfg: cfg}, nil
}

type provider struct {
	api *sdk.Client
	cfg Config
}

func (p *provider) Name() string { return Name }

func (p *provider) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	model := req.Model
	if model == "" {
		model = p.cfg.Model
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = p.cfg.MaxTokens
	}

	var schema map[string]any
	if err := json.Unmarshal(req.Schema, &schema); err != nil {
		return nil, &llm.Error{
			Class: llm.ClassBadRequest, Provider: Name, Model: model,
			Detail: "schema is not a JSON object", Err: err,
		}
	}

	params := sdk.MessageNewParams{
		Model:     model,
		MaxTokens: int64(maxTokens),
		Messages: []sdk.MessageParam{
			sdk.NewUserMessage(sdk.NewTextBlock(req.Prompt)),
		},
		OutputConfig: sdk.OutputConfigParam{
			Format: sdk.JSONOutputFormatParam{Schema: schema},
		},
	}
	if req.System != "" {
		params.System = []sdk.TextBlockParam{{Text: req.System}}
	}

	message, err := p.api.Messages.New(ctx, params)
	if err != nil {
		return nil, fault(err, model)
	}
	if message.StopReason == sdk.StopReasonRefusal {
		return nil, &llm.Error{
			Class: llm.ClassRefused, Provider: Name, Model: string(message.Model),
			Detail: message.StopDetails.Explanation,
		}
	}

	var text strings.Builder
	for _, block := range message.Content {
		if b, ok := block.AsAny().(sdk.TextBlock); ok {
			text.WriteString(b.Text)
		}
	}
	structured := strings.TrimSpace(text.String())
	if !json.Valid([]byte(structured)) {
		return nil, &llm.Error{
			Class: llm.ClassSchema, Provider: Name, Model: string(message.Model),
			Detail: "answer was not valid JSON",
		}
	}

	return &llm.Response{
		Structured: json.RawMessage(structured),
		Model:      string(message.Model),
		Usage: llm.Usage{
			InputTokens:     int(message.Usage.InputTokens),
			OutputTokens:    int(message.Usage.OutputTokens),
			CacheReadTokens: int(message.Usage.CacheReadInputTokens),
		},
	}, nil
}

// fault maps an SDK failure onto the L1 classes. An HTTP status decides the
// class; anything without one is a transport failure, which is transient.
func fault(err error, model string) error {
	var api *sdk.Error
	if errors.As(err, &api) {
		after := ""
		if api.Response != nil {
			after = api.Response.Header.Get("Retry-After")
		}
		return &llm.Error{
			Class:      llm.ClassifyStatus(api.StatusCode),
			Provider:   Name,
			Model:      model,
			Status:     api.StatusCode,
			RetryAfter: llm.ParseRetryAfter(after),
			Detail:     string(api.Type()),
			Err:        err,
		}
	}
	return &llm.Error{Class: llm.ClassUnavailable, Provider: Name, Model: model, Err: err}
}
