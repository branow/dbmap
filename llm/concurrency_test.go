package llm

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestConcurrencyNeverExceedsTheLimit(t *testing.T) {
	const limit = 3
	const calls = 30

	var inFlight, peak atomic.Int64
	inner := &fake{before: func() {
		now := inFlight.Add(1)
		for {
			high := peak.Load()
			if now <= high || peak.CompareAndSwap(high, now) {
				break
			}
		}
		time.Sleep(time.Millisecond)
		inFlight.Add(-1)
	}}
	client := WithConcurrency(inner, limit)

	var wg sync.WaitGroup
	for i := 0; i < calls; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := client.Complete(context.Background(), Request{}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()

	if peak.Load() > limit {
		t.Fatalf("peak in flight = %d, limit %d", peak.Load(), limit)
	}
	if inner.count() != calls {
		t.Fatalf("calls = %d, want %d", inner.count(), calls)
	}
}

func TestConcurrencyLeavesTheClientAloneWhenUnbounded(t *testing.T) {
	inner := &fake{}
	if client := WithConcurrency(inner, 0); client != Client(inner) {
		t.Fatal("a zero limit wrapped the client")
	}
}

func TestConcurrencyReportsACancelledWait(t *testing.T) {
	var once sync.Once
	entered := make(chan struct{})
	release := make(chan struct{})
	inner := &fake{before: func() {
		once.Do(func() { close(entered) })
		<-release
	}}
	client := WithConcurrency(inner, 1)

	go func() { _, _ = client.Complete(context.Background(), Request{}) }()
	// Block until the first call actually holds the only slot.
	<-entered

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.Complete(ctx, Request{})
	if !errors.Is(err, ErrUnavailable) || !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	close(release)
}

func TestConcurrencyPassesTheNameThrough(t *testing.T) {
	if got := WithConcurrency(&fake{name: "inner"}, 2).Name(); got != "inner" {
		t.Fatalf("Name = %q", got)
	}
}
