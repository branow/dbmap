package anthropic

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

const schema = `{"type":"object","properties":{"sentence":{"type":"string"}}}`

func request() llm.Request {
	return llm.Request{
		System: "you are a database cartographer",
		Prompt: "describe dbo.Example",
		Schema: json.RawMessage(schema),
	}
}

// serve stands up a fake Claude API and a client pointed at it, and hands back
// the decoded request body of the last call.
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

	client, err := New(Config{APIKey: "test-key", BaseURL: server.URL})
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
// travels as output_config.format, and usage comes back on the response.
func TestCompleteRoundTrip(t *testing.T) {
	client, body, path := serve(t, func(w http.ResponseWriter, r *http.Request) {
		answer(w, `{
			"id":"msg_1","type":"message","role":"assistant","model":"claude-haiku-4-5",
			"content":[{"type":"text","text":"{\"sentence\":\"Reads dbo.Example.\"}"}],
			"stop_reason":"end_turn",
			"usage":{"input_tokens":120,"output_tokens":24,"cache_read_input_tokens":40}
		}`)
	})

	resp, err := client.Complete(context.Background(), request())
	if err != nil {
		t.Fatalf("err = %v", err)
	}

	if *path != "/v1/messages" {
		t.Fatalf("path = %q", *path)
	}
	if got := (*body)["model"]; got != llm.DefaultModel {
		t.Fatalf("model = %v, want %v", got, llm.DefaultModel)
	}
	format := (*body)["output_config"].(map[string]any)["format"].(map[string]any)
	if format["type"] != "json_schema" {
		t.Fatalf("format type = %v", format["type"])
	}
	sent, err := json.Marshal(format["schema"])
	if err != nil {
		t.Fatal(err)
	}
	var want, got any
	_ = json.Unmarshal([]byte(schema), &want)
	_ = json.Unmarshal(sent, &got)
	if !jsonEqual(want, got) {
		t.Fatalf("schema = %s, want %s", sent, schema)
	}
	system := (*body)["system"].([]any)[0].(map[string]any)
	if system["text"] != request().System {
		t.Fatalf("system = %v", system["text"])
	}

	if string(resp.Structured) != `{"sentence":"Reads dbo.Example."}` {
		t.Fatalf("structured = %s", resp.Structured)
	}
	if resp.Model != "claude-haiku-4-5" {
		t.Fatalf("model = %q", resp.Model)
	}
	want2 := llm.Usage{InputTokens: 120, OutputTokens: 24, CacheReadTokens: 40}
	if resp.Usage != want2 {
		t.Fatalf("usage = %+v, want %+v", resp.Usage, want2)
	}
}

func TestRequestModelOverridesTheDefault(t *testing.T) {
	client, body, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {
		answer(w, `{"id":"m","type":"message","role":"assistant","model":"other",
			"content":[{"type":"text","text":"{}"}],"stop_reason":"end_turn",
			"usage":{"input_tokens":1,"output_tokens":1}}`)
	})
	req := request()
	req.Model = "other"
	req.MaxTokens = 77

	if _, err := client.Complete(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if (*body)["model"] != "other" {
		t.Fatalf("model = %v", (*body)["model"])
	}
	if (*body)["max_tokens"] != float64(77) {
		t.Fatalf("max_tokens = %v", (*body)["max_tokens"])
	}
}

func TestErrorClassMapping(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		header   string
		payload  string
		sentinel error
		after    time.Duration
	}{
		{
			name: "auth", status: http.StatusUnauthorized, sentinel: llm.ErrAuth,
			payload: `{"type":"error","error":{"type":"authentication_error","message":"no"}}`,
		},
		{
			name: "rate limited", status: http.StatusTooManyRequests,
			header: "12", sentinel: llm.ErrRateLimited, after: 12 * time.Second,
			payload: `{"type":"error","error":{"type":"rate_limit_error","message":"slow"}}`,
		},
		{
			name: "unavailable", status: http.StatusInternalServerError,
			sentinel: llm.ErrUnavailable,
			payload:  `{"type":"error","error":{"type":"api_error","message":"boom"}}`,
		},
		{
			name: "overloaded", status: 529, sentinel: llm.ErrUnavailable,
			payload: `{"type":"error","error":{"type":"overloaded_error","message":"busy"}}`,
		},
		{
			name: "bad request", status: http.StatusBadRequest, sentinel: llm.ErrBadRequest,
			payload: `{"type":"error","error":{"type":"invalid_request_error","message":"long"}}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			client, _, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {
				if c.header != "" {
					w.Header().Set("Retry-After", c.header)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(c.payload))
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
		answer(w, `{"id":"m","type":"message","role":"assistant","model":"claude-haiku-4-5",
			"content":[],"stop_reason":"refusal",
			"stop_details":{"type":"refusal","category":"general_harms","explanation":"declined"},
			"usage":{"input_tokens":1,"output_tokens":0}}`)
	})

	_, err := client.Complete(context.Background(), request())
	if !errors.Is(err, llm.ErrRefused) {
		t.Fatalf("err = %v", err)
	}
	if llm.Retryable(err) {
		t.Fatal("a refusal was called retryable")
	}
	var e *llm.Error
	_ = errors.As(err, &e)
	if e.Detail != "declined" {
		t.Fatalf("detail = %q", e.Detail)
	}
}

func TestNonJSONAnswerIsASchemaFailure(t *testing.T) {
	client, _, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {
		answer(w, `{"id":"m","type":"message","role":"assistant","model":"claude-haiku-4-5",
			"content":[{"type":"text","text":"I cannot produce that."}],
			"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	})

	if _, err := client.Complete(context.Background(), request()); !errors.Is(err, llm.ErrSchema) {
		t.Fatalf("err = %v", err)
	}
}

func TestTransportFailureIsUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := server.URL
	server.Close()

	client, err := New(Config{APIKey: "test-key", BaseURL: url})
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

func TestMissingKeyIsAnAuthFailure(t *testing.T) {
	_, err := New(Config{})
	if !errors.Is(err, llm.ErrAuth) {
		t.Fatalf("err = %v", err)
	}
}

func TestName(t *testing.T) {
	client, err := New(Config{APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	if client.Name() != "anthropic" {
		t.Fatalf("Name = %q", client.Name())
	}
}

func jsonEqual(a, b any) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right)
}
