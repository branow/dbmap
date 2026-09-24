package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/branow/dbmap/internal/cmdutil"
	"github.com/branow/dbmap/internal/index"
	"github.com/branow/dbmap/internal/iostreams"
	"github.com/branow/dbmap/llm"
)

// priced answers every call with the same body and the same bill.
type priced struct{ calls int }

func (p *priced) Name() string { return "priced" }

func (p *priced) Complete(context.Context, llm.Request) (*llm.Response, error) {
	p.calls++
	return &llm.Response{
		Structured: json.RawMessage(`{"objects":[]}`),
		Usage:      llm.Usage{InputTokens: 100, OutputTokens: 10, Cost: 0.25},
	}, nil
}

// The usage on a response is replayed verbatim from the disk cache, so a build
// that reports what its responses carried reports what an EARLIER run paid. A
// resumed build then invoices itself twice for work it did not redo. The meter
// therefore sits under the cache, where it sees only calls that reached the
// provider.
func TestACachedAnswerIsNotBilledAgain(t *testing.T) {
	dir := t.TempDir()
	provider := &priced{}

	spend := &llm.Usage{}
	metered := llm.WithUsage(provider, spend)
	client := llm.WithCache(metered, dir)

	request := llm.Request{Prompt: "describe dbo.Orders", Schema: json.RawMessage(`{}`)}
	first, err := client.Complete(context.Background(), request)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	second, err := client.Complete(context.Background(), request)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}

	if provider.calls != 1 {
		t.Fatalf("the provider was called %d times, want the second served from cache",
			provider.calls)
	}
	// Both responses carry the same bill, which is exactly why summing them is
	// the wrong number to report.
	if first.Usage.Cost != second.Usage.Cost {
		t.Fatalf("a replayed response carried a different bill: %v then %v",
			first.Usage.Cost, second.Usage.Cost)
	}
	if spend.Cost != 0.25 {
		t.Fatalf("spend = %v, want one call's cost", spend.Cost)
	}
	if spend.InputTokens != 100 {
		t.Fatalf("input tokens = %d, want one call's", spend.InputTokens)
	}
}

// A retried call is billed for the attempts that failed too, so the meter sits
// under the retrier as well: it counts what reached the provider, not what the
// build asked for.
func TestARetriedCallIsBilledForEveryAttempt(t *testing.T) {
	provider := &flaky{fail: 2}
	spend := &llm.Usage{}
	client := llm.WithRetry(llm.WithUsage(provider, spend), llm.RetryConfig{
		Attempts: 4,
		Sleep:    func(context.Context, time.Duration) error { return nil },
	})

	if _, err := client.Complete(context.Background(), llm.Request{}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if provider.calls != 3 {
		t.Fatalf("the provider saw %d attempts, want two failures and one answer",
			provider.calls)
	}
	// Only the attempt that answered reports usage; a failed attempt returns no
	// response to meter, which is the provider's own accounting, not ours.
	if spend.InputTokens != 7 {
		t.Fatalf("input tokens = %d, want the answering attempt's", spend.InputTokens)
	}
}

type flaky struct {
	fail  int
	calls int
}

func (f *flaky) Name() string { return "flaky" }

func (f *flaky) Complete(context.Context, llm.Request) (*llm.Response, error) {
	f.calls++
	if f.fail > 0 {
		f.fail--
		return nil, &llm.Error{Class: llm.ClassUnavailable, Provider: "flaky"}
	}
	return &llm.Response{
		Structured: json.RawMessage(`{}`),
		Usage:      llm.Usage{InputTokens: 7},
	}, nil
}

// A backend that answers every call while naming nothing it was asked about
// leaves the description column empty with no failed batch to show for it. That
// is not a successful build.
func TestABuildThatDescribedNothingIsNotASuccess(t *testing.T) {
	cases := []struct {
		name    string
		summary index.Summary
		wantErr bool
	}{
		{"clean", index.Summary{Described: 3}, false},
		{"failed batches", index.Summary{Failed: []error{errors.New("boom")}}, true},
		{"answers for nothing asked", index.Summary{Missing: []string{"dbo.Orders"}}, true},
		{"dry run", index.Summary{DryRun: true, Missing: []string{"dbo.Orders"}}, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := describeOutcome(c.summary)
			if c.wantErr != (err != nil) {
				t.Fatalf("err = %v, want error: %v", err, c.wantErr)
			}
			if err != nil && cmdutil.ExitCode(err) != cmdutil.ExitUnavailable {
				t.Fatalf("exit code = %d, want unavailable", cmdutil.ExitCode(err))
			}
		})
	}
}

// Describe batches run concurrently, so the progress logger is written from
// several goroutines at once. Without a lock this interleaves half-lines on a
// terminal, and races on the buffer underneath. Run under -race.
func TestProgressIsSafeUnderConcurrentWriters(t *testing.T) {
	streams, _, _, errOut := iostreams.Test()
	log := newProgress(streams, false)

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				log.Info(fmt.Sprintf("describe: dbo.T%02d_%02d  Holds rows.", w, i))
			}
		}(w)
	}
	wg.Wait()

	lines := strings.Split(strings.TrimRight(errOut.String(), "\n"), "\n")
	if len(lines) != 8*50 {
		t.Fatalf("wrote %d lines, want %d", len(lines), 8*50)
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "describe: dbo.T") || !strings.HasSuffix(line, "Holds rows.") {
			t.Fatalf("a line was interleaved with another: %q", line)
		}
	}
}

// --quiet means no progress at all, because a pipeline reading stdout should
// not have to filter a commentary it never asked for.
func TestQuietWritesNoProgress(t *testing.T) {
	streams, _, _, errOut := iostreams.Test()
	newProgress(streams, true).Info("describe: dbo.Orders  Holds orders.")

	if errOut.Len() != 0 {
		t.Fatalf("a quiet build wrote progress: %q", errOut.String())
	}
}
