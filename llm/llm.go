// Package llm is a provider seam: one prompt plus one JSON schema in, one
// structured answer out. It is deliberately not an agent framework — no chat,
// tool calling, streaming or conversation state.
//
// The layers are this package (the contract), the provider packages under it,
// the middleware here that wraps a [Client] and returns a [Client], and
// [Config] plus llm/provider, which maps a config onto a provider.
package llm

import (
	"context"
	"encoding/json"
)

// Request is a single completion: a prompt, and the schema its answer must
// satisfy. Structured output is the product; there is no free-form mode.
type Request struct {
	// System is optional. It is stable across a batch, so providers that
	// support prefix caching can cache it.
	System string

	// Prompt is the per-request text.
	Prompt string

	// Schema is the JSON Schema the answer must satisfy. Required.
	//
	// Never name an output field "description": every model measured fills it
	// with a description OF THE FIELD rather than of the object, and better
	// instruction-following makes it worse. Name the field for the thing
	// produced ("sentence"). See DESIGN.md, "Two hard-won prompt lessons".
	Schema json.RawMessage

	// Model overrides the client's configured default when non-empty.
	Model string

	// MaxTokens caps the answer. 0 leaves the provider default in place.
	MaxTokens int
}

// Response is one answer. The structured payload stays raw, because the answer
// shape is the caller's business.
type Response struct {
	// Structured is the schema-constrained answer, exactly as the provider
	// returned it.
	Structured json.RawMessage

	// Text is whatever prose came alongside the structured answer. Usually
	// empty, because the whole response is the structured answer.
	Text string

	// Model is what actually answered, not what was asked for.
	Model string

	// Usage is what the call consumed, as far as the provider reports it.
	Usage Usage
}

// Usage is the token and money cost of a call. A field a provider cannot report
// stays zero and is never estimated.
type Usage struct {
	InputTokens     int
	OutputTokens    int
	CacheReadTokens int

	// Cost is in US dollars, and is 0 whenever the provider does not price the
	// call itself. This package never derives a cost from a price table.
	Cost float64
}

// Add accumulates another call's usage. Used by [WithUsage] and by callers
// summing a batch.
func (u *Usage) Add(other Usage) {
	u.InputTokens += other.InputTokens
	u.OutputTokens += other.OutputTokens
	u.CacheReadTokens += other.CacheReadTokens
	u.Cost += other.Cost
}

// Client is the whole contract. Middleware both takes and returns one, so it
// composes in any order.
type Client interface {
	// Complete answers one request. Every returned error is an [*Error].
	Complete(ctx context.Context, req Request) (*Response, error)

	// Name identifies the provider behind this client, for cache keys and logs.
	// Middleware passes the wrapped client's name through unchanged.
	Name() string
}
