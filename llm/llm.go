// Package llm is a provider seam: one prompt plus one JSON schema in, one
// structured answer out.
//
// It is deliberately not an agent framework, not a chat abstraction, not tool
// calling, not streaming and not conversation state. One request, one response.
// Anything beyond that belongs to the caller.
//
// The layers are: this package (the contract), the provider packages under it
// (anthropic, openai, claudecode), the middleware in this package that wraps a
// [Client] and returns a [Client], and [Config] plus the constructor in
// llm/provider that maps a config struct onto one of the providers.
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
	// Never name a field in this schema "description". A field called
	// "description" whose schema text asks for "one sentence saying what this
	// object does" gets filled with a description *of the field* rather than of
	// the object, on every model measured, and better instruction-following
	// makes it worse rather than better. Name the field for the thing being
	// produced ("sentence") and tell the model to put the finished text there
	// verbatim. See DESIGN.md, "Two hard-won prompt lessons".
	Schema json.RawMessage

	// Model overrides the client's configured default when non-empty.
	Model string

	// MaxTokens caps the answer. 0 leaves the provider default in place.
	MaxTokens int
}

// Response is one answer. The structured payload stays raw: the answer shape is
// the caller's business, and this package never decodes it.
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

// Usage is the token and money cost of a call. Fields a provider cannot report
// stay zero; none of them is ever estimated.
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

// Client is the whole contract. Providers implement it; middleware wraps it and
// returns it, so middleware composes in any order.
type Client interface {
	// Complete answers one request. Every returned error is an [*Error].
	Complete(ctx context.Context, req Request) (*Response, error)

	// Name identifies the provider behind this client, for cache keys and logs.
	// Middleware passes the wrapped client's name through unchanged.
	Name() string
}
