package render_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/branow/dbmap/internal/catalog"
	"github.com/branow/dbmap/internal/render"
)

func table() catalog.Entry {
	return catalog.Entry{
		Object: catalog.Object{
			Schema:   "app",
			Name:     "Orders",
			Kind:     catalog.Table,
			Rows:     5000,
			KB:       2048,
			Triggers: 1,
			Modified: "2026-01-01T00:00:00",
		},
		Structure: catalog.Structure{
			Columns: []catalog.Column{
				{Name: "OrderID", Type: "int", Identity: true},
				{Name: "Status", Type: "varchar", Length: "(20)", Nullable: true},
			},
			PrimaryKey: []string{"OrderID"},
			Indexes:    []catalog.Index{{Name: "ixStatus", Columns: []string{"Status"}}},
		},
		Fingerprint: "abc123",
		Description: "One row per placed order.",
	}
}

func procedure() catalog.Entry {
	return catalog.Entry{
		Object: catalog.Object{
			Schema:   "app",
			Name:     "OrderGet",
			Kind:     catalog.Procedure,
			Modified: "2026-02-01T00:00:00",
		},
		Structure: catalog.Structure{
			Parameters: []catalog.Param{
				{Name: "@OrderID", Type: "int"},
				{Name: "@Total", Type: "money", Output: true},
			},
		},
		Fingerprint: "abc123",
		Description: "Returns one order by id.",
	}
}

func write(t *testing.T, entries ...catalog.Entry) (string, render.Result) {
	t.Helper()
	dir := t.TempDir()
	result, err := render.Write(dir, entries, nil)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	return dir, result
}

func read(t *testing.T, dir, name string) []string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return strings.Split(strings.TrimRight(string(content), "\n"), "\n")
}

func TestSize(t *testing.T) {
	cases := []struct {
		kb   int64
		want string
	}{
		{0, "0 KB"},
		{16, "16 KB"},
		{1023, "1023 KB"},
		{2048, "2 MB"},
		{58072064, "55 GB"},
	}
	for _, c := range cases {
		if got := render.Size(c.kb); got != c.want {
			t.Errorf("Size(%d) = %q, want %q", c.kb, got, c.want)
		}
	}
}

// A catalog is written per kind, and only for kinds the database actually has.
func TestWriteCatalogsPerKindPresent(t *testing.T) {
	dir, result := write(t, table(), procedure())

	want := []render.Written{{File: "tables.tsv", Rows: 1}, {File: "procedures.tsv", Rows: 1}}
	if len(result.Catalogs) != len(want) {
		t.Fatalf("catalogs = %v, want %v", result.Catalogs, want)
	}
	for i, w := range want {
		if result.Catalogs[i] != w {
			t.Errorf("catalog %d = %v, want %v", i, result.Catalogs[i], w)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "views.tsv")); !os.IsNotExist(err) {
		t.Error("no views, no views.tsv")
	}
}

func TestTableCatalogRow(t *testing.T) {
	dir, _ := write(t, table())
	lines := read(t, dir, "tables.tsv")

	const header = "name\trows\tsize\ttriggers\tcols\tmodified\tfingerprint\tdescription"
	const row = "app.Orders\t5000\t2 MB\t1\t2\t2026-01-01T00:00:00\tabc123\tOne row per placed order."
	if lines[0] != header {
		t.Errorf("header = %q, want %q", lines[0], header)
	}
	if lines[1] != row {
		t.Errorf("row = %q, want %q", lines[1], row)
	}
}

// Staleness lives in the catalogs themselves; a parallel file would drift.
func TestNoStateFileIsWritten(t *testing.T) {
	dir, _ := write(t, table())
	if _, err := os.Stat(filepath.Join(dir, "state.tsv")); !os.IsNotExist(err) {
		t.Error("staleness lives in the catalogs, not a sidecar")
	}
}

// Nothing derivable from a stored column is stored beside it: sample depth is
// min(rows, 25) and rows is already a column.
func TestNothingDerivableIsStored(t *testing.T) {
	for _, field := range render.Header(render.Catalogs[0]) {
		if strings.Contains(strings.ToLower(field), "depth") {
			t.Errorf("table catalog stores %q, which is derivable from rows", field)
		}
	}
}

func TestProcedureParametersRenderInline(t *testing.T) {
	dir, _ := write(t, procedure())
	lines := read(t, dir, "procedures.tsv")

	const row = "app.OrderGet\t@OrderID int, @Total money out\t" +
		"2026-02-01T00:00:00\tabc123\tReturns one order by id."
	if lines[1] != row {
		t.Errorf("row = %q, want %q", lines[1], row)
	}
}

func TestFunctionReturnTypeIsLiftedOutOfParameters(t *testing.T) {
	scalar := catalog.Entry{
		Structure: catalog.Structure{
			Parameters: []catalog.Param{
				{Name: catalog.Returns, Type: "bigint", Output: true},
				{Name: "@Input", Type: "varchar(50)"},
			},
		},
	}
	if got := render.Returns(scalar); got != "bigint" {
		t.Errorf("Returns = %q, want %q", got, "bigint")
	}
	if got := render.Params(scalar); got != "@Input varchar(50)" {
		t.Errorf("Params = %q, want %q", got, "@Input varchar(50)")
	}

	// A table-valued function has no return row and returns a table.
	if got := render.Returns(catalog.Entry{}); got != "table" {
		t.Errorf("Returns = %q, want %q", got, "table")
	}
}

func TestColumnFilesForDetailedKindsOnly(t *testing.T) {
	view := table()
	view.Object.Name = "ActiveOrders"
	view.Object.Kind = catalog.View

	dir, result := write(t, table(), view, procedure())
	if result.ColumnFiles != 2 {
		t.Fatalf("column files = %d, want 2", result.ColumnFiles)
	}

	names, err := os.ReadDir(filepath.Join(dir, "columns"))
	if err != nil {
		t.Fatalf("read columns dir: %v", err)
	}
	var got []string
	for _, e := range names {
		got = append(got, e.Name())
	}
	sort.Strings(got)
	want := []string{"app.ActiveOrders.tsv", "app.Orders.tsv"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("column files = %v, want %v", got, want)
	}
}

func TestColumnFileLeadsWithTheObjectAndEndsWithItsKeys(t *testing.T) {
	rendered := render.Columns(table())
	for _, pattern := range []string{
		`\A# app\.Orders @abc123\n`,
		`# rows 5000 {2}size 2 MB {2}triggers 1`,
		`# One row per placed order\.`,
		`OrderID\tint\tnot null\tidentity`,
		`Status\tvarchar\(20\)\tnull`,
		`# pk OrderID`,
		`# index ixStatus \(Status\)`,
	} {
		if !regexp.MustCompile(pattern).MatchString(rendered) {
			t.Errorf("missing %s in:\n%s", pattern, rendered)
		}
	}
}

func TestViewColumnFileOmitsRowCountsItDoesNotHave(t *testing.T) {
	view := table()
	view.Object.Kind = catalog.View
	if strings.Contains(render.Columns(view), "# rows") {
		t.Error("a view has no row count to report")
	}
}

func TestUniqueIndexAndDeclaredForeignKeyAreKept(t *testing.T) {
	entry := table()
	entry.Structure.Indexes = []catalog.Index{
		{Name: "ixRef", Unique: true, Columns: []string{"CustomerID"}},
	}
	entry.Structure.ForeignKeys = []catalog.ForeignKey{
		{Column: "CustomerID", References: "app.Customers.CustomerID"},
	}

	rendered := render.Columns(entry)
	if !strings.Contains(rendered, "# index ixRef (CustomerID) unique") {
		t.Errorf("unique index not marked:\n%s", rendered)
	}
	if !strings.Contains(rendered, "# fk CustomerID -> app.Customers.CustomerID") {
		t.Errorf("declared foreign key dropped:\n%s", rendered)
	}
}

// The next build reads its staleness state straight out of the files this one
// wrote, so the round trip is the contract.
func TestStateSurvivesAWriteReadRoundTrip(t *testing.T) {
	dir, _ := write(t, table(), procedure())

	state, err := render.ReadState(dir)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}

	orders, ok := state["app.Orders"]
	if !ok {
		t.Fatal("app.Orders missing from the state")
	}
	want := catalog.State{
		Modified:    "2026-01-01T00:00:00",
		Fingerprint: "abc123",
		Description: "One row per placed order.",
		Rows:        5000,
	}
	if orders != want {
		t.Errorf("state = %+v, want %+v", orders, want)
	}
	if !orders.Modified.Same(table().Object.Modified) {
		t.Error("the modify signal must come back able to prove an object untouched")
	}

	proc, ok := state["app.OrderGet"]
	if !ok {
		t.Fatal("app.OrderGet missing from the state")
	}
	if proc.Fingerprint != "abc123" {
		t.Errorf("fingerprint = %q, want abc123", proc.Fingerprint)
	}
	if proc.Rows != 0 {
		t.Errorf("rows = %d, want 0: a procedure has no rows to compare", proc.Rows)
	}
}

// An engine with no modify signal writes an empty cell, and it must read back
// as absent so the object refetches rather than looking untouched.
func TestAnAbsentModifySignalRoundTripsAsAbsent(t *testing.T) {
	entry := table()
	entry.Object.Modified = ""

	dir, _ := write(t, entry)
	state, err := render.ReadState(dir)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if got := state["app.Orders"].Modified; got.Present() {
		t.Errorf("modified = %q, want absent", got)
	}
}

func TestReadStateTreatsAMissingCatalogAsNothingToRead(t *testing.T) {
	state, err := render.ReadState(t.TempDir())
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if len(state) != 0 {
		t.Errorf("state = %v, want empty", state)
	}
}

func TestParseCatalogIgnoresTheHeaderAndComments(t *testing.T) {
	tables := render.Catalogs[0]
	content := strings.Join(render.Header(tables), "\t") + "\n\n# comment\n"
	if state := render.ParseCatalog(content, tables); len(state) != 0 {
		t.Errorf("state = %v, want empty", state)
	}
}

// A description is model output: it can hold anything, and must not be able to
// end a cell or a row early.
func TestATabOrNewlineInADescriptionCannotBreakARow(t *testing.T) {
	entry := table()
	entry.Description = "One row\tper order\nalways"

	dir, _ := write(t, entry)
	lines := read(t, dir, "tables.tsv")
	if len(lines) != 2 {
		t.Fatalf("file has %d lines, want a header and one row:\n%v", len(lines), lines)
	}
	if !strings.HasSuffix(lines[1], "One row per order always") {
		t.Errorf("row = %q, want the description flattened", lines[1])
	}
	if strings.Count(lines[1], "\t") != len(render.Header(render.Catalogs[0]))-1 {
		t.Errorf("row = %q, wrong cell count", lines[1])
	}
}

// Two declared foreign keys exist across three measured databases, so the index
// does not pretend there is a join graph.
func TestNoForeignKeyCatalogIsWritten(t *testing.T) {
	entry := table()
	entry.Structure.ForeignKeys = []catalog.ForeignKey{
		{Column: "CustomerID", References: "app.Customers.CustomerID"},
	}

	dir, _ := write(t, entry)
	names, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	fk := regexp.MustCompile(`(?i)foreign|fk`)
	for _, e := range names {
		if fk.MatchString(e.Name()) {
			t.Errorf("wrote %q", e.Name())
		}
	}
}
