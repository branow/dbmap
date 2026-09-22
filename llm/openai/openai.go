// Package openai talks to any OpenAI-compatible chat completions endpoint
// through the official openai-go, constraining the answer with
// response_format: json_schema and strict: true.
//
// BaseURL is the whole local-model story: Ollama, vLLM, OpenRouter and LiteLLM
// all speak this API, so none of them needs a provider of its own.
package openai

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"

	"github.com/branow/dbmap/llm"
)

// Name is the provider's identity, used in cache keys and logs.
const Name = "openai"

// schemaName labels the response format. The API requires a name; it carries no
// meaning for a single-schema request.
const schemaName = "answer"

// Config constructs a client. The API key is already resolved by the host
// application; this package never reads a file, an environment variable or a
// keychain.
type Config struct {
	// APIKey is required unless BaseURL points at a local endpoint, which
	// typically authenticates nothing.
	APIKey string
	// BaseURL overrides the API endpoint. Empty means the hosted OpenAI API.
	BaseURL string
	// Model is the default model. Empty means the request must name one.
	Model string
	// MaxTokens is the default answer cap. 0 leaves the endpoint's own default.
	MaxTokens int
}

// New returns a client for an OpenAI-compatible endpoint. Retries are turned
// off in the SDK: retrying is llm.WithRetry's job, and two layers of it would
// multiply.
func New(cfg Config) (llm.Client, error) {
	if cfg.APIKey == "" && cfg.BaseURL == "" {
		return nil, &llm.Error{Class: llm.ClassAuth, Provider: Name, Detail: "no api key"}
	}
	options := []option.RequestOption{option.WithMaxRetries(0)}
	if cfg.APIKey != "" {
		options = append(options, option.WithAPIKey(cfg.APIKey))
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
	if model == "" {
		return nil, &llm.Error{
			Class: llm.ClassBadRequest, Provider: Name, Detail: "no model configured",
		}
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

	messages := make([]sdk.ChatCompletionMessageParamUnion, 0, 2)
	if req.System != "" {
		messages = append(messages, sdk.SystemMessage(req.System))
	}
	messages = append(messages, sdk.UserMessage(req.Prompt))

	params := sdk.ChatCompletionNewParams{
		Model:    model,
		Messages: messages,
		ResponseFormat: sdk.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &shared.ResponseFormatJSONSchemaParam{
				JSONSchema: shared.ResponseFormatJSONSchemaJSONSchemaParam{
					Name:   schemaName,
					Strict: sdk.Bool(true),
					Schema: schema,
				},
			},
		},
	}
	if maxTokens > 0 {
		params.MaxCompletionTokens = sdk.Int(int64(maxTokens))
	}

	completion, err := p.api.Chat.Completions.New(ctx, params)
	if err != nil {
		return nil, fault(err, model)
	}
	if len(completion.Choices) == 0 {
		return nil, &llm.Error{
			Class: llm.ClassSchema, Provider: Name, Model: completion.Model,
			Detail: "no choices in response",
		}
	}
	choice := completion.Choices[0]
	if choice.Message.Refusal != "" {
		return nil, &llm.Error{
			Class: llm.ClassRefused, Provider: Name, Model: completion.Model,
			Detail: choice.Message.Refusal,
		}
	}
	structured := strings.TrimSpace(choice.Message.Content)
	if !json.Valid([]byte(structured)) {
		return nil, &llm.Error{
			Class: llm.ClassSchema, Provider: Name, Model: completion.Model,
			Detail: "answer was not valid JSON",
		}
	}

	return &llm.Response{
		Structured: json.RawMessage(structured),
		Model:      completion.Model,
		Usage: llm.Usage{
			InputTokens:     int(completion.Usage.PromptTokens),
			OutputTokens:    int(completion.Usage.CompletionTokens),
			CacheReadTokens: int(completion.Usage.PromptTokensDetails.CachedTokens),
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
			Detail:     api.Type,
			Err:        err,
		}
	}
	return &llm.Error{Class: llm.ClassUnavailable, Provider: Name, Model: model, Err: err}
}
