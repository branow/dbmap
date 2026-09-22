package llm

import (
	"context"
	"errors"
	"math/rand"
	"time"
)

// Retry defaults. A describe batch is worth a handful of attempts and about a
// minute of waiting; past that the batch is better skipped than stalled.
const (
	DefaultAttempts   = 4
	DefaultBackoff    = 500 * time.Millisecond
	DefaultMaxBackoff = 20 * time.Second
	DefaultElapsed    = 2 * time.Minute
)

// classAttempts tightens the attempt cap for classes where more attempts never
// help. A schema failure gets exactly one retry, then the batch fails.
var classAttempts = map[Class]int{
	ClassSchema: 2,
}

// RetryConfig configures [WithRetry]. The zero value is usable and means the
// defaults above.
type RetryConfig struct {
	// Attempts caps total attempts including the first. 0 means
	// [DefaultAttempts].
	Attempts int

	// Backoff is the first delay step; it doubles per attempt. 0 means
	// [DefaultBackoff].
	Backoff time.Duration

	// MaxBackoff caps a single delay. 0 means [DefaultMaxBackoff].
	MaxBackoff time.Duration

	// Elapsed caps the total wall clock spent across all attempts, including
	// waiting. 0 means [DefaultElapsed].
	Elapsed time.Duration

	// Jitter returns a value in [0, d). nil means full jitter over a shared
	// source. Tests inject a deterministic one to pin the bounds.
	Jitter func(d time.Duration) time.Duration

	// Sleep waits, or returns the context's error. nil means a real timer.
	// Tests inject a recorder to assert the delays without spending them.
	Sleep func(ctx context.Context, d time.Duration) error

	// Now reads the clock for the elapsed cap. nil means time.Now.
	Now func() time.Time
}

// WithRetry retries the classes marked retryable in this package, backing off
// exponentially with full jitter, honoring a provider's Retry-After, and
// stopping at whichever of the attempt and elapsed caps comes first.
func WithRetry(next Client, cfg RetryConfig) Client {
	if cfg.Attempts <= 0 {
		cfg.Attempts = DefaultAttempts
	}
	if cfg.Backoff <= 0 {
		cfg.Backoff = DefaultBackoff
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = DefaultMaxBackoff
	}
	if cfg.Elapsed <= 0 {
		cfg.Elapsed = DefaultElapsed
	}
	if cfg.Jitter == nil {
		cfg.Jitter = fullJitter
	}
	if cfg.Sleep == nil {
		cfg.Sleep = sleep
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &retrier{next: next, cfg: cfg}
}

type retrier struct {
	next Client
	cfg  RetryConfig
}

func (r *retrier) Name() string { return r.next.Name() }

func (r *retrier) Complete(ctx context.Context, req Request) (*Response, error) {
	deadline := r.cfg.Now().Add(r.cfg.Elapsed)
	var last error
	for attempt := 0; ; attempt++ {
		resp, err := r.next.Complete(ctx, req)
		if err == nil {
			return resp, nil
		}
		last = err
		class, ok := Classify(err)
		if !ok || !class.Retryable() {
			return nil, err
		}
		if attempt+1 >= r.attempts(class) {
			return nil, last
		}
		delay := r.delay(attempt, err)
		if !r.cfg.Now().Add(delay).Before(deadline) {
			return nil, last
		}
		if err := r.cfg.Sleep(ctx, delay); err != nil {
			return nil, err
		}
	}
}

// attempts is the cap for a class: the configured cap, tightened by the
// per-class table.
func (r *retrier) attempts(class Class) int {
	capped, ok := classAttempts[class]
	if ok && capped < r.cfg.Attempts {
		return capped
	}
	return r.cfg.Attempts
}

// delay is the wait before the next attempt: the provider's own Retry-After
// when it gave one, otherwise full jitter over a doubling window.
func (r *retrier) delay(attempt int, err error) time.Duration {
	var e *Error
	if errors.As(err, &e) && e.RetryAfter > 0 {
		return e.RetryAfter
	}
	window := r.cfg.Backoff << attempt
	if window > r.cfg.MaxBackoff || window <= 0 {
		window = r.cfg.MaxBackoff
	}
	return r.cfg.Jitter(window)
}

func fullJitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	return time.Duration(rand.Int63n(int64(d)))
}

func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
