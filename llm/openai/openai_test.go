package openai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/branow/dbmap/llm"
)

const schema = `{"type":"object","properties":{"sentence":{"type":"string"}},` +
	`"required":["sentence"],"additionalProperties":false}`

func request() llm.Request {
	return llm.Request{
		System: "you are a database cartographer",
		Prompt: "describe dbo.Example",
		Schema: json.RawMessage(schema),
	}
}

// serve stands up a fake OpenAI-compatible endpoint and a client pointed at it,
// and hands back the decoded request body of the last call.
func serve(t *testing.T, handler http.HandlerFunc) (llm.Client, *map[string]any, *string) {
	t.Helper()
	body := map[string]any{}
	path := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("request body: %v", err)
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)

	client, err := New(Config{APIKey: "test-key", BaseURL: server.URL, Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	return client, &body, &path
}

func answer(w http.ResponseWriter, payload string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(payload))
}

// The golden test: a schema-constrained request round-trips, structured output
// travels as response_format json_schema with strict set, and usage comes back
// on the response.
func TestCompleteRoundTrip(t *testing.T) {
	client, body, path := serve(t, func(w http.ResponseWriter, r *http.Request) {
		answer(w, `{
			"id":"c1","object":"chat.completion","created":1,"model":"test-model",
			"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant",
				"content":"{\"sentence\":\"Reads dbo.Example.\"}"}}],
			"usage":{"prompt_tokens":90,"completion_tokens":18,"total_tokens":108,
				"prompt_tokens_details":{"cached_tokens":30}}
		}`)
	})

	resp, err := client.Complete(context.Background(), request())
	if err != nil {
		t.Fatalf("err = %v", err)
	}

	if *path != "/chat/completions" {
		t.Fatalf("path = %q", *path)
	}
	if (*body)["model"] != "test-model" {
		t.Fatalf("model = %v", (*body)["model"])
	}
	format := (*body)["response_format"].(map[string]any)
	if format["type"] != "json_schema" {
		t.Fatalf("response_format type = %v", format["type"])
	}
	jsonSchema := format["json_schema"].(map[string]any)
	if jsonSchema["strict"] != true {
		t.Fatalf("strict = %v, want true", jsonSchema["strict"])
	}
	sent, err := json.Marshal(jsonSchema["schema"])
	if err != nil {
		t.Fatal(err)
	}
	var want, got any
	_ = json.Unmarshal([]byte(schema), &want)
	_ = json.Unmarshal(sent, &got)
	if !jsonEqual(want, got) {
		t.Fatalf("schema = %s, want %s", sent, schema)
	}
	messages := (*body)["messages"].([]any)
	if len(messages) != 2 {
		t.Fatalf("messages = %v", messages)
	}
	if messages[0].(map[string]any)["role"] != "system" {
		t.Fatalf("first message = %v", messages[0])
	}

	if string(resp.Structured) != `{"sentence":"Reads dbo.Example."}` {
		t.Fatalf("structured = %s", resp.Structured)
	}
	if resp.Model != "test-model" {
		t.Fatalf("model = %q", resp.Model)
	}
	want2 := llm.Usage{InputTokens: 90, OutputTokens: 18, CacheReadTokens: 30}
	if resp.Usage != want2 {
		t.Fatalf("usage = %+v, want %+v", resp.Usage, want2)
	}
}

func TestRequestModelOverridesTheDefault(t *testing.T) {
	client, body, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {
		answer(w, `{"id":"c","object":"chat.completion","created":1,"model":"other",
			"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant",
				"content":"{}"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	})
	req := request()
	req.Model = "other"
	req.MaxTokens = 64

	if _, err := client.Complete(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if (*body)["model"] != "other" {
		t.Fatalf("model = %v", (*body)["model"])
	}
	if (*body)["max_completion_tokens"] != float64(64) {
		t.Fatalf("max_completion_tokens = %v", (*body)["max_completion_tokens"])
	}
}

func TestErrorClassMapping(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		header   string
		sentinel error
		after    time.Duration
	}{
		{name: "auth", status: http.StatusUnauthorized, sentinel: llm.ErrAuth},
		{name: "forbidden", status: http.StatusForbidden, sentinel: llm.ErrAuth},
		{
			name: "rate limited", status: http.StatusTooManyRequests, header: "5",
			sentinel: llm.ErrRateLimited, after: 5 * time.Second,
		},
		{name: "unavailable", status: http.StatusBadGateway, sentinel: llm.ErrUnavailable},
		{name: "bad request", status: http.StatusBadRequest, sentinel: llm.ErrBadRequest},
		{name: "not found", status: http.StatusNotFound, sentinel: llm.ErrBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			client, _, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {
				if c.header != "" {
					w.Header().Set("Retry-After", c.header)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(
					`{"error":{"type":"invalid_request_error","message":"no","code":"x"}}`))
			})

			_, err := client.Complete(context.Background(), request())
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
			if e.Status != c.status {
				t.Fatalf("status = %d, want %d", e.Status, c.status)
			}
			if e.RetryAfter != c.after {
				t.Fatalf("retry after = %v, want %v", e.RetryAfter, c.after)
			}
		})
	}
}

func TestRefusalIsReportedNotRetried(t *testing.T) {
	client, _, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {
		answer(w, `{"id":"c","object":"chat.completion","created":1,"model":"test-model",
			"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant",
				"content":"","refusal":"declined"}}],
			"usage":{"prompt_tokens":1,"completion_tokens":0}}`)
	})

	_, err := client.Complete(context.Background(), request())
	if !errors.Is(err, llm.ErrRefused) {
		t.Fatalf("err = %v", err)
	}
	if llm.Retryable(err) {
		t.Fatal("a refusal was called retryable")
	}
}

func TestNonJSONAnswerIsASchemaFailure(t *testing.T) {
	client, _, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {
		answer(w, `{"id":"c","object":"chat.completion","created":1,"model":"test-model",
			"choices":[{"index":0,"finish_reason":"length","message":{"role":"assistant",
				"content":"{\"sentence\":\"truncat"}}],
			"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	})

	if _, err := client.Complete(context.Background(), request()); !errors.Is(err, llm.ErrSchema) {
		t.Fatalf("err = %v", err)
	}
}

func TestTransportFailureIsUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := server.URL
	server.Close()

	client, err := New(Config{APIKey: "k", BaseURL: url, Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Complete(context.Background(), request())
	if !errors.Is(err, llm.ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func TestMalformedSchemaIsABadRequest(t *testing.T) {
	client, _, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("the provider was called with a malformed schema")
	})
	req := request()
	req.Schema = json.RawMessage(`not json`)

	if _, err := client.Complete(context.Background(), req); !errors.Is(err, llm.ErrBadRequest) {
		t.Fatalf("err = %v", err)
	}
}

func TestMissingModelIsABadRequest(t *testing.T) {
	client, err := New(Config{APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Complete(context.Background(), request()); !errors.Is(err, llm.ErrBadRequest) {
		t.Fatalf("err = %v", err)
	}
}

// A local model behind BaseURL authenticates nothing, which is the whole
// point of covering Ollama, vLLM and LiteLLM through this provider.
func TestLocalEndpointNeedsNoKey(t *testing.T) {
	if _, err := New(Config{BaseURL: "http://localhost:11434/v1", Model: "m"}); err != nil {
		t.Fatalf("err = %v", err)
	}
	if _, err := New(Config{Model: "m"}); !errors.Is(err, llm.ErrAuth) {
		t.Fatalf("err = %v, want an auth failure for the hosted API", err)
	}
}

func TestName(t *testing.T) {
	client, err := New(Config{APIKey: "k", Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if client.Name() != "openai" {
		t.Fatalf("Name = %q", client.Name())
	}
}

func jsonEqual(a, b any) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right)
}
