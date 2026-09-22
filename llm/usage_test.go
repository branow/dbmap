package llm

import (
	"context"
	"sync"
	"testing"
)

func TestUsageAccumulates(t *testing.T) {
	inner := &fake{steps: []step{
		{resp: &Response{Usage: Usage{InputTokens: 10, OutputTokens: 4, CacheReadTokens: 1}}},
		{resp: &Response{Usage: Usage{InputTokens: 5, OutputTokens: 2, Cost: 0.25}}},
	}}
	var total Usage
	client := WithUsage(inner, &total)

	for i := 0; i < 2; i++ {
		if _, err := client.Complete(context.Background(), Request{}); err != nil {
			t.Fatal(err)
		}
	}
	want := Usage{InputTokens: 15, OutputTokens: 6, CacheReadTokens: 1, Cost: 0.25}
	if total != want {
		t.Fatalf("total = %+v, want %+v", total, want)
	}
}

func TestUsageIgnoresFailures(t *testing.T) {
	inner := &fake{steps: []step{{err: &Error{Class: ClassAuth}}}}
	var total Usage
	client := WithUsage(inner, &total)

	if _, err := client.Complete(context.Background(), Request{}); err == nil {
		t.Fatal("expected an error")
	}
	if (total != Usage{}) {
		t.Fatalf("total = %+v, want zero", total)
	}
}

func TestUsageIsSafeUnderConcurrency(t *testing.T) {
	const calls = 100
	inner := &fake{}
	inner.steps = make([]step, calls)
	for i := range inner.steps {
		inner.steps[i] = step{resp: &Response{Usage: Usage{InputTokens: 1}}}
	}
	var total Usage
	client := WithConcurrency(WithUsage(inner, &total), 8)

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

	if total.InputTokens != calls {
		t.Fatalf("input tokens = %d, want %d", total.InputTokens, calls)
	}
}

func TestUsageLeavesTheClientAloneWithoutATotal(t *testing.T) {
	inner := &fake{}
	if client := WithUsage(inner, nil); client != Client(inner) {
		t.Fatal("a nil total wrapped the client")
	}
}

func TestUsagePassesTheNameThrough(t *testing.T) {
	var total Usage
	if got := WithUsage(&fake{name: "inner"}, &total).Name(); got != "inner" {
		t.Fatalf("Name = %q", got)
	}
}
