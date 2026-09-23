// Package llm is a provider seam: one prompt plus one JSON schema in, one
// structured answer out. It is deliberately not an agent framework.
package llm

import (
	"context"
	"encoding/json"
)

// Request is a single completion: a prompt, and the schema its answer must
// satisfy. There is no free-form mode.
type Request struct {
	// System is stable across a batch, so a provider may prefix-cache it.
	System string

	Prompt string

	// Schema is the JSON Schema the answer must satisfy. Required.
	//
	// Never name an output field "description": every model measured fills it
	// with a description OF THE FIELD. Name the field for the thing produced
	// ("sentence").
	Schema json.RawMessage

	Model string

	// MaxTokens caps the answer. 0 leaves the provider default in place.
	MaxTokens int
}

// Response is one answer.
type Response struct {
	Structured json.RawMessage
	Text       string

	// Model is what actually answered, not what was asked for.
	Model string

	Usage Usage
}

// Usage is the token and money cost of a call. A field a provider cannot report
// stays zero and is never estimated.
type Usage struct {
	InputTokens     int
	OutputTokens    int
	CacheReadTokens int

	// Cost is in US dollars, and is 0 unless the provider prices the call
	// itself. This package never derives a cost from a price table.
	Cost float64
}

// Add accumulates another call's usage.
func (u *Usage) Add(other Usage) {
	u.InputTokens += other.InputTokens
	u.OutputTokens += other.OutputTokens
	u.CacheReadTokens += other.CacheReadTokens
	u.Cost += other.Cost
}

// Client is the whole contract. Middleware both takes and returns one.
type Client interface {
	// Complete answers one request. Every returned error is an [*Error].
	Complete(ctx context.Context, req Request) (*Response, error)

	// Name identifies the provider, for cache keys and logs. Middleware passes
	// the wrapped client's name through unchanged.
	Name() string
}
