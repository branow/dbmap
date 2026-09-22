package llm

import (
	"context"
	"sync"
)

// WithUsage accumulates every successful call's usage into the caller's totals,
// for the run summary. total must outlive the client; a nil total leaves the
// client unwrapped.
//
// The counter is mutex-guarded because [WithConcurrency] runs calls in
// parallel.
func WithUsage(next Client, total *Usage) Client {
	if total == nil {
		return next
	}
	return &meter{next: next, total: total}
}

type meter struct {
	next  Client
	mu    sync.Mutex
	total *Usage
}

func (m *meter) Name() string { return m.next.Name() }

func (m *meter) Complete(ctx context.Context, req Request) (*Response, error) {
	resp, err := m.next.Complete(ctx, req)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.total.Add(resp.Usage)
	m.mu.Unlock()
	return resp, nil
}
