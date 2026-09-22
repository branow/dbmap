package llm

import (
	"context"
	"encoding/json"
	"sync"
)

// step is one programmed outcome of the scripted fake.
type step struct {
	resp *Response
	err  error
}

// fake is a Client returning a programmed sequence. It is how retry counts,
// jitter bounds, the concurrency limit and cache hit/miss are asserted without
// a network.
type fake struct {
	mu sync.Mutex

	name  string
	steps []step
	calls int
	seen  []Request

	// before runs inside every call, holding no lock. The concurrency test uses
	// it to observe how many calls overlap.
	before func()
}

func (f *fake) Name() string {
	if f.name == "" {
		return "fake"
	}
	return f.name
}

func (f *fake) Complete(ctx context.Context, req Request) (*Response, error) {
	if f.before != nil {
		f.before()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen = append(f.seen, req)
	index := f.calls
	f.calls++
	if index >= len(f.steps) {
		return &Response{Structured: json.RawMessage(`{}`)}, nil
	}
	s := f.steps[index]
	if s.err != nil {
		return nil, s.err
	}
	return s.resp, nil
}

func (f *fake) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// repeat builds a script of n identical failures.
func repeat(n int, err error) []step {
	steps := make([]step, n)
	for i := range steps {
		steps[i] = step{err: err}
	}
	return steps
}

// ok is the trivial successful answer.
func ok() *Response {
	return &Response{Structured: json.RawMessage(`{"sentence":"ok"}`), Model: "fake-model"}
}
