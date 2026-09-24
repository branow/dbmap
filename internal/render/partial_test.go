package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/branow/dbmap/internal/catalog"
)

func tableEntry(name, fingerprint, description string) catalog.Entry {
	return catalog.Entry{
		Object:      catalog.Object{Schema: "dbo", Name: name, Kind: catalog.Table},
		Structure:   catalog.Structure{Columns: []catalog.Column{{Name: "ID", Type: "int"}}},
		Fingerprint: fingerprint,
		Description: description,
	}
}

func procEntry(name, fingerprint, description string) catalog.Entry {
	return catalog.Entry{
		Object:      catalog.Object{Schema: "dbo", Name: name, Kind: catalog.Procedure},
		Structure:   catalog.Structure{Definition: "SELECT 1"},
		Fingerprint: fingerprint,
		Description: description,
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}

// The one that costs money. A narrowed build covers only what --match and
// --limit selected, and the staleness state the NEXT build reads lives in these
// same files. Rewriting a catalog from the selection alone therefore deletes
// the fingerprint of every object this run did not look at, and the next full
// build sees them all as new and buys every description again.
func TestANarrowedBuildLeavesTheRestOfTheIndexStanding(t *testing.T) {
	dir := t.TempDir()
	full := []catalog.Entry{
		tableEntry("Orders", "aaaa", "Holds one row per placed order."),
		tableEntry("Customers", "bbbb", "Holds one row per customer."),
		procEntry("OrderGet", "cccc", "Reads one order."),
	}
	if _, err := Write(dir, full, nil); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// A second run covering only dbo.Orders, with a new description.
	narrowed := []catalog.Entry{tableEntry("Orders", "dddd", "Holds one row per order, keyed by id.")}
	if _, err := Write(dir, narrowed, nil); err != nil {
		t.Fatalf("Write: %v", err)
	}

	tables := read(t, filepath.Join(dir, "tables.tsv"))
	if !strings.Contains(tables, "dbo.Customers\t") {
		t.Fatalf("an unselected table lost its row:\n%s", tables)
	}
	if !strings.Contains(tables, "dddd") || strings.Contains(tables, "aaaa") {
		t.Fatalf("the selected table kept its old row:\n%s", tables)
	}
	if !strings.Contains(read(t, filepath.Join(dir, "procedures.tsv")), "dbo.OrderGet\t") {
		t.Fatal("a kind the narrowed build did not select lost its catalog")
	}

	state, err := ReadState(dir)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if len(state) != 3 {
		t.Fatalf("state has %d entries, want all three still tracked: %v", len(state), state)
	}
	if state["dbo.Customers"].Fingerprint != "bbbb" {
		t.Fatalf("an unselected object lost its fingerprint, so the next build redescribes it")
	}
	if state["dbo.Orders"].Fingerprint != "dddd" {
		t.Fatalf("the selected object kept a stale fingerprint")
	}
}

// Merging must not turn a dropped object into an immortal one: its row goes,
// and so do its detail files.
func TestDroppedObjectsLeaveNothingBehind(t *testing.T) {
	dir := t.TempDir()
	entries := []catalog.Entry{
		tableEntry("Orders", "aaaa", "Holds orders."),
		tableEntry("Legacy", "bbbb", "Holds nothing anyone reads."),
		procEntry("LegacyGet", "cccc", "Reads the legacy table."),
	}
	if _, err := Write(dir, entries, nil); err != nil {
		t.Fatalf("Write: %v", err)
	}

	kept := []catalog.Entry{tableEntry("Orders", "aaaa", "Holds orders.")}
	if _, err := Write(dir, kept, []string{"dbo.Legacy", "dbo.LegacyGet"}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	tables := read(t, filepath.Join(dir, "tables.tsv"))
	if strings.Contains(tables, "dbo.Legacy\t") {
		t.Fatalf("a dropped table kept its row:\n%s", tables)
	}
	for _, path := range []string{
		filepath.Join(dir, columnsDir, "dbo.Legacy.tsv"),
		filepath.Join(dir, bodiesDir, "dbo.LegacyGet.sql"),
	} {
		if _, err := os.Stat(path); err == nil {
			t.Errorf("%s survived the drop", path)
		}
	}
	// Every object of that kind is gone, so the file is too rather than being
	// left holding rows the catalog no longer has.
	if _, err := os.Stat(filepath.Join(dir, "procedures.tsv")); err == nil {
		t.Error("an emptied catalog file was left behind")
	}
}

// Rows are sorted by name rather than written in the order a run happened to
// select them, so the file does not depend on which objects were built when.
func TestCatalogRowsDoNotDependOnBuildOrder(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()

	if _, err := Write(first, []catalog.Entry{
		tableEntry("Orders", "a", "one"),
		tableEntry("Customers", "b", "two"),
	}, nil); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := Write(second, []catalog.Entry{tableEntry("Customers", "b", "two")}, nil); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := Write(second, []catalog.Entry{tableEntry("Orders", "a", "one")}, nil); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if a, b := read(t, filepath.Join(first, "tables.tsv")),
		read(t, filepath.Join(second, "tables.tsv")); a != b {
		t.Fatalf("one build and two produced different files:\n%s\n---\n%s", a, b)
	}
}
