package index

import (
	"context"
	"strings"
	"testing"
)

// A build against a real database runs for minutes. Every stage that does work
// per object says which object, so the terminal shows progress rather than
// going quiet between the manifest and the summary - and so a run that stalls
// names the object it stalled on.
func TestTheBuildReportsWhatItIsDoing(t *testing.T) {
	source, client, opts := table(), &model{}, options(t)
	client.source = source
	log := &recorder{}
	opts.Logger = log
	opts.Samples = -1

	if _, err := Build(context.Background(), source, client, opts); err != nil {
		t.Fatalf("Build: %v", err)
	}

	joined := strings.Join(log.lines, "\n")
	for _, want := range []string{
		"manifest:",  // how much there is
		"plan:",      // what is stale and what is not
		"fetch:",     // what is being read from the database
		"sample:",    // which table, before it is read
		"describe:",  // which object got which sentence
		"write:",     // what landed on disk
		"tables.tsv", // named, so a reader can go open it
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("progress never mentions %q:\n%s", want, joined)
		}
	}
	t.Logf("build progress:\n%s", joined)
}
