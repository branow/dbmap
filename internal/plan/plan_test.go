package plan_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/branow/dbmap/internal/catalog"
	"github.com/branow/dbmap/internal/fingerprint"
	"github.com/branow/dbmap/internal/plan"
)

const (
	deployed = catalog.Signal("2026-02-14T09:00:00")
	older    = catalog.Signal("2024-07-25T09:00:00")
)

func object(name string, kind catalog.Kind, modified catalog.Signal, rows int64) catalog.Object {
	return catalog.Object{Schema: "app", Name: name, Kind: kind, Modified: modified, Rows: rows}
}

func TestFetchReasons(t *testing.T) {
	cases := []struct {
		name   string
		object catalog.Object
		prior  *catalog.State
		want   plan.Reason
	}{
		{
			name:   "an object the last build never saw",
			object: object("Orders", catalog.Table, deployed, 5000),
			want:   plan.New,
		},
		{
			name:   "an unmoved signal and an unmoved depth is left alone",
			object: object("Orders", catalog.Table, deployed, 5000),
			prior:  &catalog.State{Modified: deployed, Rows: 5000},
			want:   "",
		},
		{
			name:   "a release that moved the modify signal",
			object: object("OrderGet", catalog.Procedure, deployed, 0),
			prior:  &catalog.State{Modified: older},
			want:   plan.Modified,
		},
		{
			name:   "an engine with no modify signal can never prove an object untouched",
			object: object("OrderGet", catalog.Procedure, "", 0),
			prior:  &catalog.State{Modified: ""},
			want:   plan.Modified,
		},
		{
			name:   "a table that filled up without any DDL",
			object: object("Orders", catalog.Table, deployed, 50000),
			prior:  &catalog.State{Modified: deployed, Rows: 0},
			want:   plan.Sample,
		},
		{
			name:   "an eight-row enum that gained a ninth value",
			object: object("OrderType", catalog.Table, deployed, 9),
			prior:  &catalog.State{Modified: deployed, Rows: 8},
			want:   plan.Sample,
		},
		{
			name:   "growth above the sample size",
			object: object("Orders", catalog.Table, deployed, 60),
			prior:  &catalog.State{Modified: deployed, Rows: 40},
			want:   "",
		},
		{
			name:   "growth of a 616M-row log",
			object: object("Ledger", catalog.Table, deployed, 617802364),
			prior:  &catalog.State{Modified: deployed, Rows: 616802364},
			want:   "",
		},
		{
			name:   "a table that shrank back below the sample size",
			object: object("Orders", catalog.Table, deployed, 10),
			prior:  &catalog.State{Modified: deployed, Rows: 5000},
			want:   plan.Sample,
		},
		{
			name:   "a state entry with no recorded row count refetches once",
			object: object("Orders", catalog.Table, deployed, 200),
			prior:  &catalog.State{Modified: deployed},
			want:   plan.Sample,
		},
		{
			name:   "a row count cannot make a procedure stale",
			object: object("OrderGet", catalog.Procedure, deployed, 900),
			prior:  &catalog.State{Modified: deployed, Rows: 0},
			want:   "",
		},
		{
			name:   "a row count cannot make a view stale",
			object: object("ActiveOrders", catalog.View, deployed, 900),
			prior:  &catalog.State{Modified: deployed, Rows: 0},
			want:   "",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			state := map[string]catalog.State{}
			if c.prior != nil {
				state[c.object.Key()] = *c.prior
			}

			result := plan.Fetch([]catalog.Object{c.object}, state)
			got := result.Reasons[c.object.Key()]
			if got != c.want {
				t.Fatalf("reason = %q, want %q", got, c.want)
			}
			if c.want == "" && len(result.Fetch) != 0 {
				t.Errorf("fetched %d objects, want none", len(result.Fetch))
			}
			if c.want != "" && len(result.Reuse) != 0 {
				t.Errorf("reused %d objects, want none", len(result.Reuse))
			}
		})
	}
}

func TestFetchSplitsNewFromUntouched(t *testing.T) {
	objects := []catalog.Object{
		object("Orders", catalog.Table, deployed, 5000),
		object("Customers", catalog.Table, deployed, 5000),
	}
	state := map[string]catalog.State{
		"app.Orders": {Modified: deployed, Fingerprint: "x", Rows: 5000},
	}

	result := plan.Fetch(objects, state)
	if len(result.Fetch) != 1 || result.Fetch[0].Name != "Customers" {
		t.Fatalf("fetch = %v, want only Customers", result.Fetch)
	}
	if len(result.Reuse) != 1 || result.Reuse[0].Name != "Orders" {
		t.Fatalf("reuse = %v, want only Orders", result.Reuse)
	}
}

// The writer needs to know what to remove, so an object gone from the source is
// reported rather than silently left in the index.
func TestFetchReportsDroppedObjects(t *testing.T) {
	objects := []catalog.Object{object("Orders", catalog.Table, deployed, 5)}
	state := map[string]catalog.State{
		"app.Orders":      {Modified: deployed, Fingerprint: "x", Rows: 5},
		"app.RetiredProc": {Modified: older, Fingerprint: "y"},
		"app.OldView":     {Modified: older, Fingerprint: "z"},
	}

	got := plan.Fetch(objects, state).Dropped
	want := []string{"app.OldView", "app.RetiredProc"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dropped = %v, want %v", got, want)
	}
}

// Scoping the sample rule to tables is a stated rule, not a no-op that happens
// never to fire because procedures report zero rows.
func TestSampleRuleIsScopedToTables(t *testing.T) {
	for _, rule := range plan.FetchReasons {
		if rule.Reason != plan.Sample {
			continue
		}
		if !reflect.DeepEqual(rule.Kinds, []catalog.Kind{catalog.Table}) {
			t.Fatalf("sample rule kinds = %v, want [table]", rule.Kinds)
		}
		return
	}
	t.Fatal("no sample rule in the fetch table")
}

func entry(kind catalog.Kind, definition string) catalog.Entry {
	return catalog.Entry{
		Object:    object("OrderGet", kind, deployed, 0),
		Structure: catalog.Structure{Definition: definition},
	}
}

// The whole point of the second tier: a release ALTERs 58 procedures, and the
// ones whose bytes did not move cost no LLM call.
func TestDescribeReusesARedeployedIdenticalBody(t *testing.T) {
	const body = "CREATE PROCEDURE app.OrderGet AS SELECT 1"
	state := map[string]catalog.State{
		"app.OrderGet": {
			Modified:    older,
			Fingerprint: fingerprint.Module(body),
			Description: "Returns one order by id.",
		},
	}

	result, err := plan.Describe([]catalog.Entry{entry(catalog.Procedure, body)}, state)
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if len(result.Describe) != 0 {
		t.Errorf("describe = %d, want 0", len(result.Describe))
	}
	if len(result.Unchanged) != 1 {
		t.Fatalf("unchanged = %d, want 1", len(result.Unchanged))
	}
	if got := result.Unchanged[0].Description; got != "Returns one order by id." {
		t.Errorf("description = %q, want the one already written", got)
	}
}

func TestDescribeSendsChangedAndUndescribedBodies(t *testing.T) {
	const body = "CREATE PROCEDURE app.OrderGet AS SELECT 1"
	cases := []struct {
		name  string
		prior catalog.State
	}{
		{
			name: "a body that actually changed",
			prior: catalog.State{
				Fingerprint: fingerprint.Module("CREATE PROCEDURE app.OrderGet AS SELECT 2"),
				Description: "Returns one order by id.",
			},
		},
		{
			name:  "a matching fingerprint that never got a description",
			prior: catalog.State{Fingerprint: fingerprint.Module(body)},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			state := map[string]catalog.State{"app.OrderGet": c.prior}
			result, err := plan.Describe([]catalog.Entry{entry(catalog.Procedure, body)}, state)
			if err != nil {
				t.Fatalf("Describe: %v", err)
			}
			if len(result.Describe) != 1 {
				t.Errorf("describe = %d, want 1", len(result.Describe))
			}
		})
	}
}

// Every entry carries the fingerprint just computed for it, so the writer never
// recomputes one.
func TestDescribeStampsTheFingerprint(t *testing.T) {
	const body = "CREATE PROCEDURE app.OrderGet AS SELECT 1"
	result, err := plan.Describe([]catalog.Entry{entry(catalog.Procedure, body)}, nil)
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if got := result.Describe[0].Fingerprint; got != fingerprint.Module(body) {
		t.Errorf("fingerprint = %q, want %q", got, fingerprint.Module(body))
	}
}

func TestDescribeRefusesAnUnknownKind(t *testing.T) {
	_, err := plan.Describe([]catalog.Entry{entry("trigger", "")}, nil)
	var unknown fingerprint.UnknownKindError
	if !errors.As(err, &unknown) {
		t.Fatalf("err = %v, want UnknownKindError", err)
	}
}

func TestDescribableDropsKindsNotWorthASentence(t *testing.T) {
	entries := []catalog.Entry{
		{Object: object("Orders", catalog.Table, deployed, 5)},
		{Object: object("LegacyOrders", catalog.Synonym, deployed, 0)},
	}

	kept := plan.Describable(entries)
	if len(kept) != 1 || kept[0].Object.Name != "Orders" {
		t.Errorf("describable = %v, want only Orders", kept)
	}
}

func TestSummarize(t *testing.T) {
	fetch := plan.FetchPlan{
		Fetch:   []catalog.Object{object("Orders", catalog.Table, deployed, 5)},
		Reuse:   []catalog.Object{object("Customers", catalog.Table, deployed, 5)},
		Dropped: []string{"app.RetiredProc"},
	}
	describe := plan.DescribePlan{
		Describe:  []catalog.Entry{{}},
		Unchanged: []catalog.Entry{{}, {}},
	}

	want := plan.Summary{Fetch: 1, Reuse: 1, Dropped: 1, Describe: 1, Unchanged: 2}
	if got := plan.Summarize(fetch, describe); got != want {
		t.Errorf("summary = %+v, want %+v", got, want)
	}
}
