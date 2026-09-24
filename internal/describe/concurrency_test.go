package describe

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/branow/dbmap/internal/catalog"
	"github.com/branow/dbmap/llm"
)

// counter answers every batch with a sentence per object it was asked about,
// and records the high-water mark of calls in flight.
type counter struct {
	mu     sync.Mutex
	live   int
	peak   int
	calls  int
	block  chan struct{}
	opened chan struct{}
}

func (c *counter) Name() string { return "counter" }

func (c *counter) Complete(_ context.Context, req llm.Request) (*llm.Response, error) {
	c.mu.Lock()
	c.calls++
	c.live++
	if c.live > c.peak {
		c.peak = c.live
	}
	c.mu.Unlock()

	if c.block != nil {
		c.opened <- struct{}{}
		<-c.block
	}

	var answers []answer
	for _, line := range strings.Split(req.Prompt, "\n") {
		name, ok := strings.CutPrefix(line, "=== OBJECT ")
		if !ok {
			continue
		}
		if _, key, found := strings.Cut(name, ": "); found {
			answers = append(answers, answer{
				Name:     strings.TrimSuffix(key, " ==="),
				Sentence: "Holds rows.",
			})
		}
	}
	data, _ := json.Marshal(reply{Objects: answers})

	c.mu.Lock()
	c.live--
	c.mu.Unlock()
	return &llm.Response{Structured: data, Usage: llm.Usage{InputTokens: 1}}, nil
}

func inputs(n int) []Input {
	out := make([]Input, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, input(catalog.Table, fmt.Sprintf("T%02d", i)))
	}
	return out
}

// Describing is the long pole of a build and every batch is independent, so
// batches run together rather than one at a time.
func TestBatchesRunConcurrently(t *testing.T) {
	const batches = 4
	c := &counter{block: make(chan struct{}), opened: make(chan struct{}, batches)}

	done := make(chan Result, 1)
	go func() {
		result, _ := All(context.Background(), c, inputs(batches),
			Options{Max: 1, Workers: batches})
		done <- result
	}()

	// Every worker must reach the provider before any of them is released, or
	// they were never running at the same time.
	for i := 0; i < batches; i++ {
		<-c.opened
	}
	close(c.block)
	result := <-done

	if c.peak < 2 {
		t.Fatalf("peak calls in flight = %d, want batches running together", c.peak)
	}
	if len(result.Sentences) != batches {
		t.Fatalf("described %d objects, want %d", len(result.Sentences), batches)
	}
}

// Running together must not make the answer depend on which batch finished
// first: the usage total, the failures and the missing list are all merged in
// batch order.
func TestConcurrentBatchesProduceTheSameResult(t *testing.T) {
	const objects = 12

	sequential, err := All(context.Background(), &counter{}, inputs(objects),
		Options{Max: 2, Workers: 1})
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	parallel, err := All(context.Background(), &counter{}, inputs(objects),
		Options{Max: 2, Workers: 4})
	if err != nil {
		t.Fatalf("All: %v", err)
	}

	if len(sequential.Sentences) != len(parallel.Sentences) {
		t.Fatalf("described %d objects concurrently, %d sequentially",
			len(parallel.Sentences), len(sequential.Sentences))
	}
	for key, sentence := range sequential.Sentences {
		if parallel.Sentences[key] != sentence {
			t.Fatalf("%s: %q concurrently, %q sequentially",
				key, parallel.Sentences[key], sentence)
		}
	}
	if sequential.Usage != parallel.Usage {
		t.Fatalf("usage %+v concurrently, %+v sequentially", parallel.Usage, sequential.Usage)
	}
	if len(parallel.Missing) != 0 {
		t.Fatalf("Missing = %v, want none", parallel.Missing)
	}
}

// The default is one worker: a library caller gets deterministic ordering
// unless it asks for otherwise. The command sets its own.
func TestTheDefaultIsOneWorker(t *testing.T) {
	c := &counter{}
	if _, err := All(context.Background(), c, inputs(6), Options{Max: 1}); err != nil {
		t.Fatalf("All: %v", err)
	}
	if c.peak != 1 {
		t.Fatalf("peak calls in flight = %d, want 1 by default", c.peak)
	}
}
