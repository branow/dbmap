package llm

import "context"

// WithConcurrency bounds how many calls are in flight at once. n <= 0 leaves
// the client unwrapped.
func WithConcurrency(next Client, n int) Client {
	if n <= 0 {
		return next
	}
	return &limiter{next: next, slots: make(chan struct{}, n)}
}

type limiter struct {
	next  Client
	slots chan struct{}
}

func (l *limiter) Name() string { return l.next.Name() }

func (l *limiter) Complete(ctx context.Context, req Request) (*Response, error) {
	select {
	case l.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, &Error{Class: ClassUnavailable, Provider: l.Name(), Err: ctx.Err()}
	}
	defer func() { <-l.slots }()
	return l.next.Complete(ctx, req)
}
