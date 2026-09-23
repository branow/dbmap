package sample

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/branow/dbmap/internal/catalog"
	"github.com/branow/dbmap/internal/engine"
)

// fetcher records every table that actually reached the engine, which is the
// only way to assert that a table was never queried rather than queried and
// discarded.
type fetcher struct {
	asked   []engine.Table
	health  engine.Health
	healthE error
	rows    [][]string
	err     error
}

func (f *fetcher) Sample(_ context.Context, _ engine.Conn, table engine.Table, n int) (catalog.Sample, error) {
	f.asked = append(f.asked, table)
	if f.err != nil {
		return catalog.Sample{}, f.err
	}
	return catalog.Sample{Columns: table.Columns, Rows: f.rows}, nil
}

func (f *fetcher) Health(context.Context, engine.Conn) (engine.Health, error) {
	if f.healthE != nil {
		return engine.Health{}, f.healthE
	}
	return f.health, nil
}

func ok() engine.Health {
	return engine.Classify(&engine.Reading{MemoryVisible: true, AvailableGB: 64, State: "available"})
}

func table(name string, rows int64, columns ...catalog.Column) catalog.Entry {
	return catalog.Entry{
		Object:    catalog.Object{Schema: "dbo", Name: name, Kind: catalog.Table, Rows: rows},
		Structure: catalog.Structure{Columns: columns},
	}
}

func col(name, typ string) catalog.Column { return catalog.Column{Name: name, Type: typ} }

// A column whose name says it holds a person's data is never read. The name is
// the only signal: a type cannot tell a product code from a surname.
func TestPIIColumnsAreNeverProjected(t *testing.T) {
	entry := table("Customers", 100,
		col("CustomerID", "int"),
		col("Email", "nvarchar"),
		col("StatusID", "int"),
	)

	plan := Project(entry)

	for _, c := range plan.Table.Columns {
		if strings.EqualFold(c, "Email") {
			t.Fatal("a PII column was projected")
		}
	}
	var withheld bool
	for _, w := range plan.Withheld {
		if w.Column == "Email" && w.Reason == catalog.WithheldPII {
			withheld = true
		}
	}
	if !withheld {
		t.Fatalf("PII column not recorded as withheld: %+v", plan.Withheld)
	}
}

// The strongest rule in this package: a table with nothing readable is NEVER
// SENT. Not queried and discarded — never queried.
func TestATableOfOnlyPIIIsNeverQueried(t *testing.T) {
	entries := []catalog.Entry{
		table("People", 50, col("Email", "nvarchar"), col("Phone", "nvarchar"), col("FirstName", "nvarchar")),
	}
	f := &fetcher{health: ok()}

	samples, err := All(context.Background(), f, nil, entries, Options{})
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(f.asked) != 0 {
		t.Fatalf("a table of only PII reached the engine: %+v", f.asked)
	}
	if len(samples) != 0 {
		t.Fatalf("got samples for a table that should never be read: %+v", samples)
	}
}

func TestOpaqueColumnsAreExcluded(t *testing.T) {
	cases := []catalog.Column{
		{Name: "Body", Type: "varchar", Length: "(max)"},
		{Name: "Blob", Type: "varbinary"},
		{Name: "Payload", Type: "bytea"},
		{Name: "Picture", Type: "image"},
		{Name: "Shape", Type: "geography"},
		{Name: "Version", Type: "rowversion"},
	}

	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			plan := Project(table("T", 10, col("ID", "int"), c))

			for _, projected := range plan.Table.Columns {
				if projected == c.Name {
					t.Fatalf("%s %s was projected", c.Name, c.Type)
				}
			}
			if len(plan.Withheld) != 1 || plan.Withheld[0].Reason != catalog.WithheldUnsampleable {
				t.Fatalf("not recorded as unsampleable: %+v", plan.Withheld)
			}
		})
	}
}

// The regression that matters. A lookup table's meaning lives in its name
// column, not its integer key: excluding long string types once made such a
// table project nothing but an id. Every cell is capped anyway, so
// unboundedness is not a reason to withhold one.
func TestOrdinaryStringColumnsAreSampled(t *testing.T) {
	cases := []catalog.Column{
		{Name: "Name", Type: "text"},
		{Name: "Label", Type: "ntext"},
		{Name: "Code", Type: "varchar", Length: "(50)"},
		{Name: "Title", Type: "nvarchar", Length: "(200)"},
		{Name: "Doc", Type: "xml"},
		{Name: "Config", Type: "jsonb"},
	}

	for _, c := range cases {
		t.Run(c.Name+" "+c.Type, func(t *testing.T) {
			plan := Project(table("T", 10, col("ID", "int"), c))

			var projected bool
			for _, p := range plan.Table.Columns {
				if p == c.Name {
					projected = true
				}
			}
			if !projected {
				t.Fatalf("%s %s was withheld; the value domain lives in columns like this", c.Name, c.Type)
			}
		})
	}
}

func TestCellsAreCapped(t *testing.T) {
	wide := strings.Repeat("x", CellChars+250)
	rendered := Render(catalog.Sample{
		Columns: []string{"Note"},
		Rows:    [][]string{{wide}},
	})

	for _, line := range strings.Split(rendered, "\n")[1:] {
		if n := len([]rune(line)); n > CellChars {
			t.Fatalf("cell rendered at %d runes, cap is %d", n, CellChars)
		}
	}
}

// A tab or newline inside a value must not be able to forge a column or a row
// in the block the describer reads.
func TestCellsCannotForgeStructure(t *testing.T) {
	rendered := Render(catalog.Sample{
		Columns: []string{"A", "B"},
		Rows:    [][]string{{"one\ttwo", "three\nfour"}},
	})

	lines := strings.Split(rendered, "\n")
	if len(lines) != 2 {
		t.Fatalf("value forged a row: %q", rendered)
	}
	if n := strings.Count(lines[1], "\t"); n != 1 {
		t.Fatalf("value forged a column: %q", lines[1])
	}
}

// Withheld columns are named, not omitted. A describer told nothing about the
// five columns it cannot see is being misled about the table's shape.
func TestWithheldColumnsSurfaceInTheOutput(t *testing.T) {
	rendered := Render(catalog.Sample{
		Columns: []string{"CustomerID"},
		Withheld: []catalog.Withheld{
			{Column: "Email", Reason: catalog.WithheldPII},
			{Column: "Photo", Reason: catalog.WithheldUnsampleable},
		},
	})

	for _, want := range []string{"Email", "Photo", string(catalog.WithheldPII), string(catalog.WithheldUnsampleable)} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered output does not mention %q:\n%s", want, rendered)
		}
	}
}

// A capped run must spend its budget on the tables whose rows ARE their value
// domain, not on the front rows of a huge log.
func TestCompleteTablesAreSampledFirst(t *testing.T) {
	entries := []catalog.Entry{
		table("HugeLog", 616802364, col("ID", "int")),
		table("OrderStatuses", 11, col("ID", "int")),
		table("BigAudit", 33490305, col("ID", "int")),
		table("OrderTypes", 15, col("ID", "int")),
	}

	rows := map[string]int64{}
	for _, e := range entries {
		rows[e.Key()] = e.Object.Rows
	}
	ordered := Order(Plans(entries), rows)

	if !ordered[0].Complete || !ordered[1].Complete {
		t.Fatalf("incomplete table sorted ahead of a value domain: %v", keys(ordered))
	}
	// Smaller first within the group keeps a run reproducible.
	if ordered[0].Table.Name != "OrderStatuses" {
		t.Fatalf("order = %v, want the smallest complete table first", keys(ordered))
	}
	if ordered[2].Complete || ordered[3].Complete {
		t.Fatalf("a complete table sorted after an incomplete one: %v", keys(ordered))
	}
}

func TestACappedRunSpendsItsBudgetOnValueDomains(t *testing.T) {
	entries := []catalog.Entry{
		table("HugeLog", 616802364, col("ID", "int")),
		table("OrderStatuses", 11, col("ID", "int")),
		table("BigAudit", 33490305, col("ID", "int")),
		table("OrderTypes", 15, col("ID", "int")),
	}
	f := &fetcher{health: ok()}

	if _, err := All(context.Background(), f, nil, entries, Options{Limit: 2}); err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(f.asked) != 2 {
		t.Fatalf("sampled %d tables, want 2", len(f.asked))
	}
	for _, asked := range f.asked {
		if asked.Name == "HugeLog" || asked.Name == "BigAudit" {
			t.Fatalf("a capped run spent budget on %s instead of a value domain", asked.Name)
		}
	}
}

// Health is asked before every table, not once at startup, because headroom is
// what changes during a run.
func TestSamplingHaltsWhenTheServerHasNoRoom(t *testing.T) {
	entries := []catalog.Entry{table("Orders", 10, col("ID", "int"))}
	reading := engine.Reading{MemoryVisible: true, AvailableGB: engine.MinAvailableGB - 1}
	f := &fetcher{health: engine.Classify(&reading)}

	_, err := All(context.Background(), f, nil, entries, Options{})

	var unhealthy *engine.UnhealthyError
	if !errors.As(err, &unhealthy) {
		t.Fatalf("error is %T, want *engine.UnhealthyError", err)
	}
	if len(f.asked) != 0 {
		t.Fatal("sampled a table after the server said it has no room")
	}
}

// Not being able to see the floor is not the same as being above it.
func TestSamplingHaltsWhenHealthIsUnreadable(t *testing.T) {
	entries := []catalog.Entry{table("Orders", 10, col("ID", "int"))}
	f := &fetcher{healthE: errors.New("permission denied on the health views")}

	_, err := All(context.Background(), f, nil, entries, Options{})

	var unhealthy *engine.UnhealthyError
	if !errors.As(err, &unhealthy) {
		t.Fatalf("error is %T, want *engine.UnhealthyError", err)
	}
	if unhealthy.Known {
		t.Fatal("an unreadable reading was reported as the server's own decision")
	}
	if len(f.asked) != 0 {
		t.Fatal("sampled a table while health was unknown")
	}
}

// One unreadable table is not a reason to abandon a build, but it is never
// silent either.
func TestAFailedTableIsLoggedAndSkipped(t *testing.T) {
	entries := []catalog.Entry{table("Orders", 10, col("ID", "int"))}
	log := &recorder{}
	f := &fetcher{health: ok(), err: errors.New("timeout")}

	samples, err := All(context.Background(), f, nil, entries, Options{Logger: log})
	if err != nil {
		t.Fatalf("a single failed table aborted the run: %v", err)
	}
	if len(samples) != 0 {
		t.Fatal("a failed sample produced a result")
	}
	if len(log.messages) == 0 {
		t.Fatal("a failed sample was skipped silently")
	}
}

func TestOnlyTablesAreSampled(t *testing.T) {
	entries := []catalog.Entry{
		{Object: catalog.Object{Schema: "dbo", Name: "V", Kind: catalog.View},
			Structure: catalog.Structure{Columns: []catalog.Column{col("ID", "int")}}},
		{Object: catalog.Object{Schema: "dbo", Name: "P", Kind: catalog.Procedure}},
	}
	f := &fetcher{health: ok()}

	if _, err := All(context.Background(), f, nil, entries, Options{}); err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(f.asked) != 0 {
		t.Fatalf("sampled a non-table: %+v", f.asked)
	}
}

type recorder struct{ messages []string }

func (r *recorder) Warn(message string) { r.messages = append(r.messages, message) }

func keys(plans []Plan) []string {
	out := make([]string, len(plans))
	for i, p := range plans {
		out[i] = p.Table.Name
	}
	return out
}
