package llm

import (
	"context"
	"errors"
	"testing"
	"time"
)

// recorder replaces the retrier's clock and sleeper, so delays are asserted
// rather than spent.
type recorder struct {
	now    time.Time
	delays []time.Duration
}

func (r *recorder) sleep(ctx context.Context, d time.Duration) error {
	r.delays = append(r.delays, d)
	r.now = r.now.Add(d)
	return nil
}

func (r *recorder) clock() time.Time { return r.now }

// config wires a retrier to the recorder with a deterministic jitter that
// always returns the top of the window, so bounds are checkable.
func (r *recorder) config(attempts int) RetryConfig {
	return RetryConfig{
		Attempts:   attempts,
		Backoff:    time.Second,
		MaxBackoff: 8 * time.Second,
		Elapsed:    time.Hour,
		Jitter:     func(d time.Duration) time.Duration { return d },
		Sleep:      r.sleep,
		Now:        r.clock,
	}
}

func TestRetryStopsAtTheAttemptCap(t *testing.T) {
	inner := &fake{steps: repeat(10, &Error{Class: ClassUnavailable})}
	rec := &recorder{now: time.Unix(0, 0)}
	client := WithRetry(inner, rec.config(4))

	_, err := client.Complete(context.Background(), Request{})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if inner.count() != 4 {
		t.Fatalf("calls = %d, want 4", inner.count())
	}
	if len(rec.delays) != 3 {
		t.Fatalf("delays = %v, want 3 of them", rec.delays)
	}
}

func TestRetryNeverRetriesTheNonRetryableClasses(t *testing.T) {
	for _, class := range []Class{ClassAuth, ClassRefused, ClassBadRequest} {
		inner := &fake{steps: repeat(10, &Error{Class: class})}
		rec := &recorder{now: time.Unix(0, 0)}
		client := WithRetry(inner, rec.config(4))

		if _, err := client.Complete(context.Background(), Request{}); err == nil {
			t.Fatal("expected an error")
		}
		if inner.count() != 1 {
			t.Fatalf("class %v: calls = %d, want 1", class, inner.count())
		}
	}
}

func TestRetrySucceedsAfterATransientFailure(t *testing.T) {
	inner := &fake{steps: []step{
		{err: &Error{Class: ClassUnavailable}},
		{resp: ok()},
	}}
	rec := &recorder{now: time.Unix(0, 0)}
	client := WithRetry(inner, rec.config(4))

	resp, err := client.Complete(context.Background(), Request{})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if string(resp.Structured) != `{"sentence":"ok"}` {
		t.Fatalf("structured = %s", resp.Structured)
	}
	if inner.count() != 2 {
		t.Fatalf("calls = %d, want 2", inner.count())
	}
}

func TestRetryGivesSchemaFailuresExactlyOneRetry(t *testing.T) {
	inner := &fake{steps: repeat(10, &Error{Class: ClassSchema})}
	rec := &recorder{now: time.Unix(0, 0)}
	client := WithRetry(inner, rec.config(6))

	if _, err := client.Complete(context.Background(), Request{}); !errors.Is(err, ErrSchema) {
		t.Fatalf("err = %v", err)
	}
	if inner.count() != 2 {
		t.Fatalf("calls = %d, want 2", inner.count())
	}
}

func TestRetryHonorsRetryAfter(t *testing.T) {
	inner := &fake{steps: []step{
		{err: &Error{Class: ClassRateLimited, RetryAfter: 7 * time.Second}},
		{resp: ok()},
	}}
	rec := &recorder{now: time.Unix(0, 0)}
	client := WithRetry(inner, rec.config(4))

	if _, err := client.Complete(context.Background(), Request{}); err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(rec.delays) != 1 || rec.delays[0] != 7*time.Second {
		t.Fatalf("delays = %v, want one of 7s", rec.delays)
	}
}

func TestRetryBacksOffExponentiallyWithinTheJitterWindow(t *testing.T) {
	inner := &fake{steps: repeat(10, &Error{Class: ClassUnavailable})}
	rec := &recorder{now: time.Unix(0, 0)}
	cfg := rec.config(6)
	// Full jitter draws from [0, window); pin the draw at the top so the window
	// itself is what is asserted.
	client := WithRetry(inner, cfg)

	if _, err := client.Complete(context.Background(), Request{}); err == nil {
		t.Fatal("expected an error")
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second,
		8 * time.Second}
	if len(rec.delays) != len(want) {
		t.Fatalf("delays = %v, want %v", rec.delays, want)
	}
	for i, d := range rec.delays {
		if d != want[i] {
			t.Fatalf("delay %d = %v, want %v", i, d, want[i])
		}
	}
}

func TestFullJitterStaysInsideTheWindow(t *testing.T) {
	for _, window := range []time.Duration{0, time.Millisecond, time.Second, time.Minute} {
		for i := 0; i < 200; i++ {
			d := fullJitter(window)
			if d < 0 || d >= window && window > 0 {
				t.Fatalf("fullJitter(%v) = %v, outside [0, %v)", window, d, window)
			}
			if window == 0 && d != 0 {
				t.Fatalf("fullJitter(0) = %v", d)
			}
		}
	}
}

func TestRetryStopsAtTheElapsedCap(t *testing.T) {
	inner := &fake{steps: repeat(10, &Error{Class: ClassUnavailable})}
	rec := &recorder{now: time.Unix(0, 0)}
	cfg := rec.config(10)
	cfg.Elapsed = 4 * time.Second
	client := WithRetry(inner, cfg)

	if _, err := client.Complete(context.Background(), Request{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
	// Windows are 1s then 2s; a third wait of 4s would not fit inside 4s.
	if len(rec.delays) != 2 {
		t.Fatalf("delays = %v, want 2 of them", rec.delays)
	}
	if inner.count() != 3 {
		t.Fatalf("calls = %d, want 3", inner.count())
	}
}

func TestRetryReturnsTheContextErrorWhenTheWaitIsCancelled(t *testing.T) {
	inner := &fake{steps: repeat(10, &Error{Class: ClassUnavailable})}
	rec := &recorder{now: time.Unix(0, 0)}
	cfg := rec.config(4)
	cfg.Sleep = func(ctx context.Context, d time.Duration) error { return context.Canceled }
	client := WithRetry(inner, cfg)

	if _, err := client.Complete(context.Background(), Request{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if inner.count() != 1 {
		t.Fatalf("calls = %d, want 1", inner.count())
	}
}

func TestRetryPassesTheNameThrough(t *testing.T) {
	client := WithRetry(&fake{name: "inner"}, RetryConfig{})
	if client.Name() != "inner" {
		t.Fatalf("Name = %q", client.Name())
	}
}
