package index

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/branow/dbmap/internal/catalog"
	"github.com/branow/dbmap/internal/engine"
	"github.com/branow/dbmap/internal/plan"
)

// options is a build wired entirely into the test's temporary directory: no
// database, no model, no keychain, nothing outside it.
func options(t *testing.T) Options {
	t.Helper()
	root := t.TempDir()
	return Options{
		Environment: "stage",
		Database:    "AppCore",
		Out:         filepath.Join(root, "index"),
		Cache:       filepath.Join(root, "cache"),
		Samples:     -1,
	}
}

// The two-tier claim, and the reason the whole design exists: a second build
// over input nothing has touched costs nothing at all.
func TestASecondBuildOverUnchangedInputMakesNoModelCall(t *testing.T) {
	source, client, opts := table(), &model{}, options(t)
	client.source = source

	first := mustBuild(t, source, client, opts)
	if first.Described == 0 {
		t.Fatalf("the first build described nothing")
	}
	if client.calls == 0 {
		t.Fatalf("the first build made no model call")
	}

	before := client.calls
	source.reset()
	second := mustBuild(t, source, client, opts)

	if client.calls != before {
		t.Fatalf("the second build made %d model calls, want 0", client.calls-before)
	}
	if second.Described != 0 {
		t.Fatalf("Described = %d, want 0", second.Described)
	}
	// Every object but the synonym, which has no sentence to keep and so is
	// never counted as unchanged.
	if second.Unchanged != first.Objects-1 {
		t.Fatalf("Unchanged = %d, want %d", second.Unchanged, first.Objects-1)
	}
}

// The second tier is content, not timestamps: an ALTER back to the deployed
// bytes moves the modify signal, so the body is pulled again and the hash then
// says there is nothing to describe.
func TestAByteIdenticalRedeployIsFetchedButNotDescribed(t *testing.T) {
	source, client, opts := table(), &model{}, options(t)
	client.source = source
	mustBuild(t, source, client, opts)

	before := client.calls
	source.reset()
	source.signal("dbo.OrderGet", "2025-02-14")

	summary := mustBuild(t, source, client, opts)

	if summary.Fetched != 1 {
		t.Fatalf("Fetched = %d, want 1", summary.Fetched)
	}
	if got := summary.Reasons[plan.Modified]; got != 1 {
		t.Fatalf("Reasons[modified] = %d, want 1", got)
	}
	if !contains(source.modules, "dbo.OrderGet") {
		t.Fatalf("the body was not refetched: %v", source.modules)
	}
	if client.calls != before {
		t.Fatalf("made %d model calls, want 0", client.calls-before)
	}
	if summary.Described != 0 {
		t.Fatalf("Described = %d, want 0", summary.Described)
	}
}

// A changed body is the case both tiers agree on.
func TestAChangedBodyIsFetchedAndDescribed(t *testing.T) {
	source, client, opts := table(), &model{}, options(t)
	client.source = source
	mustBuild(t, source, client, opts)

	source.reset()
	source.signal("dbo.OrderGet", "2025-02-14")
	source.bodies["dbo.OrderGet"] = "SELECT 1 FROM dbo.Orders"

	summary := mustBuild(t, source, client, opts)

	if summary.Described != 1 {
		t.Fatalf("Described = %d, want 1", summary.Described)
	}
	// Everything but the changed procedure and the synonym.
	if summary.Unchanged != summary.Objects-2 {
		t.Fatalf("Unchanged = %d, want %d", summary.Unchanged, summary.Objects-2)
	}
}

func TestForceRebuildsEverything(t *testing.T) {
	source, client, opts := table(), &model{}, options(t)
	client.source = source
	first := mustBuild(t, source, client, opts)

	source.reset()
	opts.Force = true
	second := mustBuild(t, source, client, opts)

	if second.Unchanged != 0 {
		t.Fatalf("Unchanged = %d, want 0", second.Unchanged)
	}
	if second.Described != first.Described {
		t.Fatalf("Described = %d, want %d", second.Described, first.Described)
	}
	if second.Fetched != second.Objects {
		t.Fatalf("Fetched = %d, want %d", second.Fetched, second.Objects)
	}
}

func TestAnObjectDroppedFromTheSourceLeavesTheIndex(t *testing.T) {
	source, client, opts := table(), &model{}, options(t)
	client.source = source
	mustBuild(t, source, client, opts)

	detail := filepath.Join(opts.Out, opts.Environment, opts.Database,
		"columns", "dbo.Orders.tsv")
	if _, err := os.Stat(detail); err != nil {
		t.Fatalf("the first build wrote no detail file: %v", err)
	}

	source.reset()
	source.drop("dbo.Orders")
	summary := mustBuild(t, source, client, opts)

	if len(summary.Dropped) != 1 || summary.Dropped[0] != "dbo.Orders" {
		t.Fatalf("Dropped = %v, want [dbo.Orders]", summary.Dropped)
	}
	if _, err := os.Stat(detail); !os.IsNotExist(err) {
		t.Fatalf("the detail file survived the drop: %v", err)
	}
	tables := read(t, filepath.Join(opts.Out, opts.Environment, opts.Database, "tables.tsv"))
	if strings.Contains(tables, "dbo.Orders\t") {
		t.Fatalf("the catalog still lists the dropped table:\n%s", tables)
	}
}

func TestDryRunSendsNothingBeyondTheManifest(t *testing.T) {
	source, client, opts := table(), &model{}, options(t)
	client.source = source
	opts.DryRun = true

	summary := mustBuild(t, source, client, opts)

	if source.queries() != 1 {
		t.Fatalf("sent %d queries, want the manifest alone", source.queries())
	}
	if client.calls != 0 {
		t.Fatalf("made %d model calls, want 0", client.calls)
	}
	if summary.Objects != len(source.objects) {
		t.Fatalf("Objects = %d, want %d", summary.Objects, len(source.objects))
	}
	if summary.Fetched != len(source.objects) {
		t.Fatalf("Fetched = %d, want every object planned", summary.Fetched)
	}
	if _, err := os.Stat(filepath.Join(opts.Out, opts.Environment)); !os.IsNotExist(err) {
		t.Fatalf("a dry run wrote an index tree: %v", err)
	}
	if _, err := os.Stat(opts.Cache); !os.IsNotExist(err) {
		t.Fatalf("a dry run wrote a cache: %v", err)
	}
}

// The describe stage never holds a database connection. It is asserted from the
// model's side, because that is the moment the claim is about.
func TestTheDescribeStageRunsWithNoLiveConnection(t *testing.T) {
	source, client, opts := table(), &model{}, options(t)
	client.source = source

	mustBuild(t, source, client, opts)

	if client.calls == 0 {
		t.Fatalf("the model was never called")
	}
	if client.live {
		t.Fatalf("a model call arrived while the database was still open")
	}
	if !source.closed {
		t.Fatalf("the source was never closed")
	}
}

func TestMatchAndLimitNarrowTheBuild(t *testing.T) {
	cases := []struct {
		name  string
		match string
		limit int
		want  []string
	}{
		{"whole database", "", 0, []string{
			"dbo.Orders", "dbo.OrderStatuses", "dbo.OrderGet",
			"dbo.OpenOrders", "dbo.OrderAlias",
		}},
		{"match is a substring of the key", "status", 0, []string{"dbo.OrderStatuses"}},
		{"match ignores case", "ORDERGET", 0, []string{"dbo.OrderGet"}},
		{"limit caps the count", "", 2, []string{"dbo.Orders", "dbo.OrderStatuses"}},
		{"match then limit", "order", 3, []string{
			"dbo.Orders", "dbo.OrderStatuses", "dbo.OrderGet",
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			source, client, opts := table(), &model{}, options(t)
			client.source = source
			opts.Match, opts.Limit, opts.DryRun = c.match, c.limit, true

			summary := mustBuild(t, source, client, opts)

			if summary.Objects != len(c.want) {
				t.Fatalf("Objects = %d, want %d", summary.Objects, len(c.want))
			}
		})
	}
}

func TestMatchWritesOnlyWhatItSelected(t *testing.T) {
	source, client, opts := table(), &model{}, options(t)
	client.source = source
	opts.Match = "orderstatuses"

	mustBuild(t, source, client, opts)

	tables := read(t, filepath.Join(opts.Out, opts.Environment, opts.Database, "tables.tsv"))
	if !strings.Contains(tables, "dbo.OrderStatuses") {
		t.Fatalf("the matched table is missing:\n%s", tables)
	}
	if strings.Contains(tables, "dbo.Orders\t") {
		t.Fatalf("an unmatched table was written:\n%s", tables)
	}
}

// The cache is asked first and the database only for what it cannot prove
// current, which is what keeps a redeploy cheap.
func TestTheCacheServesWhatTheModifySignalProvesCurrent(t *testing.T) {
	source, client, opts := table(), &model{}, options(t)
	client.source = source
	mustBuild(t, source, client, opts)

	source.reset()
	mustBuild(t, source, client, opts)

	if source.fetches != 0 {
		t.Fatalf("the structure was refetched %d times, want 0", source.fetches)
	}
	if len(source.modules) != 0 {
		t.Fatalf("bodies were refetched: %v", source.modules)
	}
}

// A cold cache is a reason to read cheaply, never a reason to index an object
// incompletely: the data is refetched, and the content hash still spares the
// model call.
func TestAColdCacheRefetchesWithoutRedescribing(t *testing.T) {
	source, client, opts := table(), &model{}, options(t)
	client.source = source
	mustBuild(t, source, client, opts)

	before := client.calls
	source.reset()
	if err := os.RemoveAll(opts.Cache); err != nil {
		t.Fatalf("clearing the cache: %v", err)
	}

	summary := mustBuild(t, source, client, opts)

	if summary.Fetched != summary.Objects {
		t.Fatalf("Fetched = %d, want %d", summary.Fetched, summary.Objects)
	}
	if client.calls != before {
		t.Fatalf("made %d model calls, want 0", client.calls-before)
	}
}

// Sampling reads user data, so it is the one stage a build can turn off
// outright.
func TestSampleCapsControlWhatIsRead(t *testing.T) {
	cases := []struct {
		name    string
		samples int
		want    int
	}{
		{"none", 0, 0},
		{"one table", 1, 1},
		{"every table", -1, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			source, client, opts := table(), &model{}, options(t)
			client.source = source
			opts.Samples = c.samples

			summary := mustBuild(t, source, client, opts)

			if summary.Sampled != c.want {
				t.Fatalf("Sampled = %d, want %d", summary.Sampled, c.want)
			}
			if len(source.sampled) != c.want {
				t.Fatalf("sampled %v, want %d tables", source.sampled, c.want)
			}
		})
	}
}

// A capped run spends its budget on the value domains, which is the whole
// reason sampling is ordered at all.
func TestACappedSampleRunTakesTheCompleteTableFirst(t *testing.T) {
	source, client, opts := table(), &model{}, options(t)
	client.source = source
	opts.Samples = 1

	mustBuild(t, source, client, opts)

	if len(source.sampled) != 1 || source.sampled[0] != "dbo.OrderStatuses" {
		t.Fatalf("sampled %v, want the complete table first", source.sampled)
	}
}

// Health is asked before every source, not once at startup.
func TestAnUnhealthyServerHaltsBeforeTheManifest(t *testing.T) {
	source, client, opts := table(), &model{}, options(t)
	client.source = source
	source.health = &engine.Health{OK: false, Known: true, Reason: "the server reports low"}

	_, err := Build(context.Background(), source, client, opts)

	var unhealthy *engine.UnhealthyError
	if !errors.As(err, &unhealthy) {
		t.Fatalf("err = %v, want an UnhealthyError", err)
	}
	if unhealthy.Stage != "the manifest" {
		t.Fatalf("Stage = %q, want the manifest", unhealthy.Stage)
	}
	if source.manifests != 0 {
		t.Fatalf("the manifest query was sent anyway")
	}
}

func TestAnUnreadableHealthReadingIsNeverHealthy(t *testing.T) {
	source, client, opts := table(), &model{}, options(t)
	client.source = source
	source.health = &engine.Health{OK: false, Known: false, Reason: engine.Unreadable}

	_, err := Build(context.Background(), source, client, opts)

	var unhealthy *engine.UnhealthyError
	if !errors.As(err, &unhealthy) || unhealthy.Known {
		t.Fatalf("err = %v, want an unknown reading to halt the build", err)
	}
}

func TestHealthIsAskedBeforeEveryStage(t *testing.T) {
	source, client, opts := table(), &model{}, options(t)
	client.source = source

	mustBuild(t, source, client, opts)

	// manifest, structure, modules, then one per table sampled - and exactly
	// one each: a second reading before the same read is a wasted round trip.
	if len(source.probes) != 3+len(source.sampled) {
		t.Fatalf("probed %d times, want one before every stage and every table (%d)",
			len(source.probes), 3+len(source.sampled))
	}
}

// A module body is redacted on arrival, and what was stripped is reported
// rather than swallowed.
func TestRedactionsAreReported(t *testing.T) {
	source, client, opts := table(), &model{}, options(t)
	client.source = source
	source.bodies["dbo.OrderGet"] = "EXEC sp_send_dbmail @recipients = 'ops@example.internal'"

	summary := mustBuild(t, source, client, opts)

	if summary.Redactions.Total() == 0 {
		t.Fatalf("Redactions = %v, want the address reported", summary.Redactions)
	}
}

// The tally survives a build that refetched nothing: a summary that went quiet
// on the second run would be worse than no summary.
func TestRedactionsAreReplayedFromTheCache(t *testing.T) {
	source, client, opts := table(), &model{}, options(t)
	client.source = source
	source.bodies["dbo.OrderGet"] = "EXEC sp_send_dbmail @recipients = 'ops@example.internal'"
	first := mustBuild(t, source, client, opts)

	source.reset()
	second := mustBuild(t, source, client, opts)

	if second.Redactions.Total() != first.Redactions.Total() {
		t.Fatalf("Redactions = %v, want %v", second.Redactions, first.Redactions)
	}
}

// A failed batch is skipped, never fatal: a partial index beats none, and the
// object keeps the sentence it already had.
func TestAFailedBatchKeepsThePreviousDescription(t *testing.T) {
	source, client, opts := table(), &model{}, options(t)
	client.source = source
	mustBuild(t, source, client, opts)

	source.reset()
	source.signal("dbo.OrderGet", "2025-02-14")
	source.bodies["dbo.OrderGet"] = "SELECT 2 FROM dbo.Orders"
	client.fail = errModel

	summary := mustBuild(t, source, client, opts)

	if len(summary.Failed) == 0 {
		t.Fatalf("Failed = %v, want the batch reported", summary.Failed)
	}
	procedures := read(t, filepath.Join(opts.Out, opts.Environment, opts.Database,
		"procedures.tsv"))
	if !strings.Contains(procedures, "Describes dbo.OrderGet.") {
		t.Fatalf("the previous sentence was lost:\n%s", procedures)
	}
}

// An object the model skipped is reported, not silently blank.
func TestASkippedObjectIsReported(t *testing.T) {
	source, client, opts := table(), &model{}, options(t)
	client.source, client.blank = source, true

	summary := mustBuild(t, source, client, opts)

	if len(summary.Missing) == 0 {
		t.Fatalf("Missing = %v, want the skipped objects named", summary.Missing)
	}
}

// A synonym is indexed for what it points at, never described.
func TestASynonymIsIndexedButNotDescribed(t *testing.T) {
	source, client, opts := table(), &model{}, options(t)
	client.source = source

	mustBuild(t, source, client, opts)

	synonyms := read(t, filepath.Join(opts.Out, opts.Environment, opts.Database, "synonyms.tsv"))
	if !strings.Contains(synonyms, "dbo.OrderAlias") {
		t.Fatalf("the synonym is missing:\n%s", synonyms)
	}
	for _, key := range source.modules {
		if key == "dbo.OrderAlias" {
			t.Fatalf("a synonym body was fetched")
		}
	}
}

func TestSummaryCountsTheWholeRun(t *testing.T) {
	source, client, opts := table(), &model{}, options(t)
	client.source = source

	summary := mustBuild(t, source, client, opts)

	if summary.Objects != 5 {
		t.Fatalf("Objects = %d, want 5", summary.Objects)
	}
	if summary.Usage.InputTokens == 0 {
		t.Fatalf("Usage = %+v, want the model's tokens counted", summary.Usage)
	}
	if summary.ColumnFiles != 3 {
		t.Fatalf("ColumnFiles = %d, want 3", summary.ColumnFiles)
	}
	// One file per kind the database actually has: there are no functions, so
	// there is no functions.tsv rather than an empty one.
	if len(summary.Catalogs) != 4 {
		t.Fatalf("Catalogs = %v, want one file per kind present", summary.Catalogs)
	}
	if summary.Dir == "" {
		t.Fatalf("Dir is empty")
	}
}

func TestABuildWithNoClientDescribesNothing(t *testing.T) {
	source, opts := table(), options(t)

	summary := mustBuild(t, source, nil, opts)

	if summary.Described != 0 {
		t.Fatalf("Described = %d, want 0", summary.Described)
	}
	if summary.Objects != 5 {
		t.Fatalf("Objects = %d, want the index written anyway", summary.Objects)
	}
}

func TestNarrow(t *testing.T) {
	objects := []catalog.Object{
		{Schema: "dbo", Name: "Orders"},
		{Schema: "sales", Name: "Invoice"},
		{Schema: "dbo", Name: "OrderStatuses"},
	}
	cases := []struct {
		name  string
		match string
		limit int
		want  []string
	}{
		{"everything", "", 0, []string{"dbo.Orders", "sales.Invoice", "dbo.OrderStatuses"}},
		{"by schema", "sales.", 0, []string{"sales.Invoice"}},
		{"by name", "order", 0, []string{"dbo.Orders", "dbo.OrderStatuses"}},
		{"limit alone", "", 1, []string{"dbo.Orders"}},
		{"no match at all", "missing", 0, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := keys(narrow(objects, c.match, c.limit))
			if strings.Join(got, ",") != strings.Join(c.want, ",") {
				t.Fatalf("narrow = %v, want %v", got, c.want)
			}
		})
	}
}

func keys(objects []catalog.Object) []string {
	var out []string
	for _, object := range objects {
		out = append(out, object.Key())
	}
	return out
}

func contains(list []string, want string) bool {
	for _, value := range list {
		if value == want {
			return true
		}
	}
	return false
}

func read(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(content)
}
