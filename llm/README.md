# llm

One structured-output call, across several providers.

```go
import "github.com/branow/dbmap/llm"
```

A separate Go module with no dependency on the rest of `dbmap`, so it can be
imported on its own. It exists because no Go library covers both the API
providers and the Claude Code CLI, and that combination is the useful one: the
CLI needs no API key, which makes it the cheapest way to run a local tool
against a model you are already logged in to.

**Not** an agent framework. No chat, no tool calling, no streaming, no
conversation state. One request, one response, validated against a JSON Schema.
Anything beyond that belongs to the caller.

## Use

```go
client, err := provider.New(llm.Config{
    Provider: "claudecode",          // or "anthropic", "openai"
    Model:    "claude-haiku-4-5",
})
if err != nil {
    return err
}

resp, err := client.Complete(ctx, llm.Request{
    Prompt: "Describe this table in one sentence: ...",
    Schema: json.RawMessage(`{
      "type": "object",
      "required": ["sentence"],
      "properties": {"sentence": {"type": "string"}}
    }`),
})
if err != nil {
    return err
}

var answer struct{ Sentence string }
json.Unmarshal(resp.Structured, &answer)
```

`Structured` stays `json.RawMessage`: the answer's shape is the caller's
business, and threading a type parameter through every middleware buys nothing.

## Providers

| Provider | Transport | Structured output |
|---|---|---|
| `anthropic` | official `anthropic-sdk-go` | `output_config.format` |
| `openai` | official `openai-go`, `BaseURL` configurable | `response_format: json_schema`, `strict` |
| `claudecode` | the `claude` CLI | `--json-schema`, reading `structured_output` |

`openai` with a `BaseURL` covers local models — Ollama, vLLM, OpenRouter,
LiteLLM all speak the same API, so there is no separate provider for them, and
no API key is required when a base URL is set.

`claudecode` uses the CLI session you already have, so it needs no API key. It
reports whatever usage the CLI hands back and never invents a cost.

## Middleware

Each wraps a `Client` and returns a `Client`, so they compose in any order.

```go
client = llm.WithRetry(client, llm.RetryConfig{})
client = llm.WithConcurrency(client, 4)
client = llm.WithCache(client, cacheDir)
client = llm.WithUsage(client, &total)
```

- `WithRetry` — exponential backoff with full jitter, honouring `Retry-After`,
  retrying only the error classes that can succeed on a second attempt.
- `WithConcurrency` — a semaphore. A hundred-batch run must not open a hundred
  sockets, and providers rate-limit on concurrency.
- `WithCache` — on disk, keyed by `sha256(provider|model|system|prompt|schema)`.
  A killed run resumes free. It stores the response only, never the prompt,
  because a prompt may carry data the caller would not want on disk.
- `WithUsage` — accumulates token counts and cost into the caller's totals.

## Errors

Errors are classified, because retry and exit-code decisions depend on the class
and string-matching a provider's prose is not a decision procedure.

| Class | Meaning | Retryable |
|---|---|---|
| `ClassAuth` | credentials rejected or absent | no |
| `ClassRateLimited` | 429 or quota | yes |
| `ClassUnavailable` | 5xx, connection failure, timeout | yes |
| `ClassRefused` | the model declined to answer | no |
| `ClassSchema` | the response did not satisfy the schema | once |
| `ClassBadRequest` | prompt too long, bad model id | no |

```go
var e *llm.Error
if errors.As(err, &e) && e.Class == llm.ClassRateLimited {
    // ...
}
```

## Configuration

The module takes structs, not a config file. Reading YAML, resolving profiles
and fetching an API key from a keychain are the host application's job — a
module that insists on a file format is a module you fight.

## License

MIT, as part of [dbmap](https://github.com/branow/dbmap).
