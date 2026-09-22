# llm — provider seam

`github.com/branow/dbmap/llm`, a nested Go module. One call shape: a prompt plus a JSON
schema in, a structured answer out. Importable standalone, so a second project can depend on
it without pulling in dbmap.

NOT an agent framework, NOT a chat abstraction, NOT tool-calling, NOT streaming, NOT
conversation state. One request, one response. Anything beyond that belongs to the caller.

why ours: no Go library covers both API providers and headless Claude Code, and the unified
wrappers that exist are immature or unmaintained (blueprint G2).

## L1 — the contract

```go
type Request struct {
    System    string          // optional; stable across a batch -> cacheable prefix
    Prompt    string
    Schema    json.RawMessage // JSON Schema; required. structured output IS the product
    Model     string          // empty -> the client's configured default
    MaxTokens int             // 0 -> provider default
}

type Response struct {
    Structured json.RawMessage // raw, caller-decoded. this package never knows the shape
    Text       string          // whatever prose came alongside; usually empty
    Model      string          // what actually answered, not what was asked for
    Usage      Usage
}

type Usage struct {
    InputTokens, OutputTokens, CacheReadTokens int
    Cost                                       float64 // 0 when the provider cannot price it
}

type Client interface {
    Complete(ctx context.Context, req Request) (*Response, error)
    Name() string
}
```

`Structured` stays `json.RawMessage`: the whole point is that dbmap's answer shape is
dbmap's business. Generics were considered and rejected — they would push the schema type
into every middleware signature for no gain.

**Errors are typed and classified**, because retry and exit-code decisions depend on the
class, and string-matching a provider's prose is not a decision procedure:

```
ErrAuth          credentials rejected or absent      -> never retry
ErrRateLimited   429 / quota                         -> retry, honor Retry-After
ErrUnavailable   5xx, connection failure, timeout    -> retry with backoff
ErrRefused       model declined to answer            -> never retry, report the object
ErrSchema        response did not satisfy the schema -> retry once, then fail the batch
ErrBadRequest    prompt too long, bad model id       -> never retry
```

## L2 — providers

One package each, one file each, ~150 lines. Each constructor takes its own config struct
and returns a `Client`; nothing is registered globally.

| package | transport | structured output via |
|---|---|---|
| `llm/anthropic` | official `anthropic-sdk-go` | `output_config.format` |
| `llm/openai` | official `openai-go/v3`, `BaseURL` configurable | `response_format: json_schema`, `strict: true` |
| `llm/claudecode` | `os/exec` the `claude` binary | `--output-format json --json-schema <file>`, read `.structured_output` |

Provider notes that are decisions, not trivia:

- **Default model is a small one.** `DESIGN.md` measured that the cheapest tier produces
  correct output for this task; the describe stage must not silently default to a large
  model. The default is `claude-haiku-4-5` for both Claude-backed providers.
- **`claudecode` passes the schema as a temp file, not an argv string.** The batch schema
  plus a 40,000-character prompt exceeds argv limits on some platforms. Also pass
  `--allowedTools ""` and `--max-turns 1`: it is being used as a pure model, not an agent.
- **`claudecode` needs no API key** — it uses the user's existing CLI session. That is the
  entire reason it exists as a provider, and it means `Usage` comes back mostly zeroed.
  Report what is available, never fabricate a cost.
- **`openai` covers local models.** Ollama, vLLM, OpenRouter and LiteLLM all speak the same
  API, so `BaseURL` is the whole local-model story — no separate provider.
- Exact SDK bindings must be read from the SDK's own documentation at implementation time,
  not recalled. Anthropic's Go surface for `output_config.format` in particular.

## L3 — middleware

The reusable half, and the reason this is a module rather than a package. Each wraps a
`Client` and returns a `Client`, so they compose in any order the caller likes.

```
WithRetry(c, RetryConfig)        exponential backoff + full jitter; retries only the
                                 classes marked retryable in L1; honors Retry-After;
                                 caps attempts and total elapsed, both configurable
WithConcurrency(c, n)            semaphore. a 100-batch describe run must not open 100
                                 sockets, and providers rate-limit on concurrency
WithCache(c, dir)                on-disk, keyed sha256(provider|model|system|prompt|schema).
                                 makes a killed run resume free, and makes tests
                                 deterministic without a fake
WithUsage(c, *Usage)             accumulates into the caller's totals for the build summary
```

`WithCache` stores the response only — never the prompt, because a prompt may carry sampled
rows. Cache entries are content-addressed, so a changed prompt simply misses.

## L4 — configuration

The module takes **structs, not a config file**. Reading YAML, resolving profiles and
fetching an API key from a keychain are the *host application's* job — dbmap does that in
`internal/config` and `internal/credentials`. A `llm.Config` carrying a provider name, model,
base URL and an already-resolved key is the seam; a helper maps it to the right constructor.

This is deliberate: the second consumer has its own config system, and a module that insists
on a file format is a module you fight.

## L5 — testing

- Every provider is tested against an `httptest.Server` (API providers) or a stub executable
  on `PATH` (`claudecode`). No live model call in any test, ever.
- Middleware is tested against a scripted fake `Client` that returns a programmed sequence of
  responses and errors — that is how retry, jitter bounds, concurrency limits and cache
  hit/miss are asserted without a network.
- One golden test per provider pinning that a schema-constrained request round-trips and that
  each error class maps correctly.
