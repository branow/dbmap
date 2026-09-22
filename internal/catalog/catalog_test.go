package catalog_test

import (
	"testing"

	"github.com/branow/dbmap/internal/catalog"
)

// An absent modify signal is the Postgres case: the engine has no modify_date,
// so nothing can ever prove the object untouched and it must always refetch.
func TestSignalSame(t *testing.T) {
	cases := []struct {
		name  string
		left  catalog.Signal
		right catalog.Signal
		same  bool
	}{
		{"an unmoved date proves an object untouched", "2026-01-01T00:00:00",
			"2026-01-01T00:00:00", true},
		{"a moved date proves nothing", "2026-02-14T09:00:00", "2024-07-25T09:00:00", false},
		{"an absent signal never matches a present one", "", "2026-01-01T00:00:00", false},
		{"a present signal never matches an absent one", "2026-01-01T00:00:00", "", false},
		{"two absent signals are not the same signal", "", "", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.left.Same(c.right); got != c.same {
				t.Errorf("Same(%q, %q) = %v, want %v", c.left, c.right, got, c.same)
			}
		})
	}
}

func TestSignalPresent(t *testing.T) {
	if (catalog.Signal("")).Present() {
		t.Error("the empty signal is the engine supplying none")
	}
	if !catalog.Signal("2026-01-01T00:00:00").Present() {
		t.Error("a date is a signal")
	}
}

// The kind table is what every other package dispatches on, so every kind
// constant must have a row in it.
func TestKindsTable(t *testing.T) {
	cases := []struct {
		kind     catalog.Kind
		describe bool
		module   bool
	}{
		{catalog.Table, true, false},
		{catalog.View, true, true},
		{catalog.Procedure, true, true},
		{catalog.Function, true, true},
		{catalog.Synonym, false, false},
	}

	if len(cases) != len(catalog.Kinds) {
		t.Fatalf("kind table has %d rows, test covers %d", len(catalog.Kinds), len(cases))
	}

	for _, c := range cases {
		t.Run(string(c.kind), func(t *testing.T) {
			spec, ok := catalog.Lookup(c.kind)
			if !ok {
				t.Fatalf("kind %q missing from the table", c.kind)
			}
			if spec.Describe != c.describe {
				t.Errorf("Describe = %v, want %v", spec.Describe, c.describe)
			}
			if spec.Module != c.module {
				t.Errorf("Module = %v, want %v", spec.Module, c.module)
			}
		})
	}
}

// A trigger is deliberately not a kind: the parent table records a count.
func TestLookupRefusesAnUncoveredKind(t *testing.T) {
	if _, ok := catalog.Lookup("trigger"); ok {
		t.Error("triggers are excluded from the index, not indexed as a kind")
	}
}

// A synonym is indexed for its target, not for a generated sentence about it.
func TestDescribable(t *testing.T) {
	cases := []struct {
		kind catalog.Kind
		want bool
	}{
		{catalog.Table, true},
		{catalog.Synonym, false},
		{"trigger", false},
	}
	for _, c := range cases {
		object := catalog.Object{Schema: "app", Name: "Orders", Kind: c.kind}
		if got := object.Describable(); got != c.want {
			t.Errorf("%q describable = %v, want %v", c.kind, got, c.want)
		}
	}
}

func TestKey(t *testing.T) {
	object := catalog.Object{Schema: "app", Name: "Orders", Kind: catalog.Table}
	if got := object.Key(); got != "app.Orders" {
		t.Errorf("Key = %q, want %q", got, "app.Orders")
	}
	entry := catalog.Entry{Object: object}
	if entry.Key() != object.Key() {
		t.Errorf("an entry is keyed by its object")
	}
}
