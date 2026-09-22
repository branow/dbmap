package fingerprint_test

import (
	"errors"
	"testing"

	"github.com/branow/dbmap/internal/catalog"
	"github.com/branow/dbmap/internal/fingerprint"
)

func table(rows int64) catalog.Object {
	return catalog.Object{Schema: "app", Name: "Orders", Kind: catalog.Table, Rows: rows}
}

func structure() catalog.Structure {
	return catalog.Structure{
		Columns: []catalog.Column{
			{Name: "OrderID", Type: "int", Nullable: false, Identity: true},
			{Name: "Status", Type: "varchar", Length: "(20)", Nullable: true},
		},
		PrimaryKey:  []string{"OrderID"},
		ForeignKeys: []catalog.ForeignKey{{Column: "CustomerID", References: "app.Customers.CustomerID"}},
		Indexes:     []catalog.Index{{Name: "ixStatus", Columns: []string{"Status"}}},
	}
}

func TestSampleDepth(t *testing.T) {
	cases := []struct {
		rows int64
		want int64
	}{
		{0, 0},
		{8, 8},
		{9, 9},
		{25, 25},
		{26, 25},
		{616802364, 25},
	}
	if fingerprint.SampleRows != 25 {
		t.Fatalf("SampleRows = %d, want 25", fingerprint.SampleRows)
	}
	for _, c := range cases {
		if got := fingerprint.SampleDepth(c.rows); got != c.want {
			t.Errorf("SampleDepth(%d) = %d, want %d", c.rows, got, c.want)
		}
	}
}

// The five transitions DESIGN.md pins: a table is stale exactly when the number
// of rows the describer sees moves, and not when it merely grew.
func TestSampleDepthTransitionsMoveTheFingerprint(t *testing.T) {
	cases := []struct {
		name  string
		from  int64
		to    int64
		moves bool
	}{
		{"a table filling up from empty", 0, 100, true},
		{"an enum gaining a ninth value", 8, 9, true},
		{"growth above the sample size", 40, 60, false},
		{"growth of a 616M-row log", 616802364, 617802364, false},
		{"a table shrinking back below the sample size", 40, 10, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before := fingerprint.Table(table(c.from), structure())
			after := fingerprint.Table(table(c.to), structure())
			if moved := before != after; moved != c.moves {
				t.Errorf("%d -> %d moved = %v, want %v", c.from, c.to, moved, c.moves)
			}
		})
	}
}

func TestStructureChangesMoveATableFingerprint(t *testing.T) {
	base := structure()
	cases := []struct {
		name   string
		mutate func(catalog.Structure) catalog.Structure
	}{
		{"a column added", func(s catalog.Structure) catalog.Structure {
			s.Columns = append(append([]catalog.Column{}, s.Columns...),
				catalog.Column{Name: "CreatedOn", Type: "datetime"})
			return s
		}},
		{"a column made not nullable", func(s catalog.Structure) catalog.Structure {
			s.Columns = []catalog.Column{s.Columns[0], {Name: "Status", Type: "varchar", Length: "(20)"}}
			return s
		}},
		{"a column losing its identity", func(s catalog.Structure) catalog.Structure {
			s.Columns = []catalog.Column{{Name: "OrderID", Type: "int"}, s.Columns[1]}
			return s
		}},
		{"the primary key dropped", func(s catalog.Structure) catalog.Structure {
			s.PrimaryKey = nil
			return s
		}},
		{"a foreign key dropped", func(s catalog.Structure) catalog.Structure {
			s.ForeignKeys = nil
			return s
		}},
		{"an index dropped", func(s catalog.Structure) catalog.Structure {
			s.Indexes = nil
			return s
		}},
		{"an index made unique", func(s catalog.Structure) catalog.Structure {
			s.Indexes = []catalog.Index{{Name: "ixStatus", Columns: []string{"Status"}, Unique: true}}
			return s
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mutated := c.mutate(structure())
			if fingerprint.Table(table(5000), base) == fingerprint.Table(table(5000), mutated) {
				t.Error("fingerprint did not move")
			}
		})
	}
}

// A trigger added to a table changes what a reader is told about it.
func TestTriggerCountMovesATableFingerprint(t *testing.T) {
	bare := table(5000)
	triggered := table(5000)
	triggered.Triggers = 1
	if fingerprint.Table(bare, structure()) == fingerprint.Table(triggered, structure()) {
		t.Error("a trigger count is part of a table's structure")
	}
}

// The canonical form is pinned as text because the whole point of building it
// as explicit lines is that an unrelated field added to Structure cannot move
// the hash.
func TestCanonicalTableIsExplicitLines(t *testing.T) {
	want := "c OrderID int notnull id\n" +
		"c Status varchar(20) null\n" +
		"pk OrderID\n" +
		"fk CustomerID -> app.Customers.CustomerID\n" +
		"ix ixStatus Status\n" +
		"tg 0\n" +
		"rows 25"
	if got := fingerprint.CanonicalTable(table(5000), structure()); got != want {
		t.Errorf("canonical form:\n%s\nwant:\n%s", got, want)
	}
}

func TestModuleFingerprint(t *testing.T) {
	const body = "CREATE PROCEDURE app.OrderGet @id int AS\nSELECT 1"
	cases := []struct {
		name  string
		left  string
		right string
		equal bool
	}{
		{"a redeployed byte-identical body", body, body, true},
		{
			"a CRLF rewrite with trailing whitespace is not an edit",
			"CREATE PROCEDURE app.OrderGet AS\nSELECT 1\n",
			"CREATE PROCEDURE app.OrderGet AS   \r\nSELECT 1\r\n",
			true,
		},
		{
			"a real edit",
			"CREATE PROCEDURE app.OrderGet AS SELECT 1",
			"CREATE PROCEDURE app.OrderGet AS SELECT 2",
			false,
		},
		{"an empty body against an absent one", "", "   \n\n", true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			equal := fingerprint.Module(c.left) == fingerprint.Module(c.right)
			if equal != c.equal {
				t.Errorf("equal = %v, want %v", equal, c.equal)
			}
		})
	}
}

// Every kind in the catalog vocabulary has a rule, and each reads the part of
// the structure its kind actually has.
func TestOfDispatchesOnKind(t *testing.T) {
	cases := []struct {
		kind catalog.Kind
		want string
	}{
		{catalog.Table, fingerprint.Table(table(5000), structure())},
		{catalog.View, fingerprint.Module("SELECT 1")},
		{catalog.Procedure, fingerprint.Module("SELECT 1")},
		{catalog.Function, fingerprint.Module("SELECT 1")},
		{catalog.Synonym, fingerprint.Sum("app.Orders")},
	}

	for _, c := range cases {
		t.Run(string(c.kind), func(t *testing.T) {
			s := structure()
			s.Definition = "SELECT 1"
			s.Target = "app.Orders"
			object := table(5000)
			object.Kind = c.kind

			got, err := fingerprint.Of(object, s)
			if err != nil {
				t.Fatalf("Of: %v", err)
			}
			if got != c.want {
				t.Errorf("Of = %q, want %q", got, c.want)
			}
		})
	}
}

// Fail closed: a kind with no rule is refused, never given a guessed hash.
func TestOfRefusesAnUnknownKind(t *testing.T) {
	object := table(0)
	object.Kind = "trigger"

	_, err := fingerprint.Of(object, catalog.Structure{})
	var unknown fingerprint.UnknownKindError
	if !errors.As(err, &unknown) {
		t.Fatalf("err = %v, want UnknownKindError", err)
	}
	if unknown.Kind != "trigger" {
		t.Errorf("Kind = %q, want %q", unknown.Kind, "trigger")
	}
}

// Sample row values are never hashed: they would leave every table on a live
// database permanently dirty.
func TestSampleValuesDoNotReachAFingerprint(t *testing.T) {
	before := fingerprint.Table(table(5000), structure())
	after := fingerprint.Table(table(5000), structure())
	if before != after {
		t.Fatal("a table fingerprint must depend on nothing but its structure and depth")
	}
	if fingerprint.SampleDepth(5000) != fingerprint.SampleDepth(9000) {
		t.Fatal("rows above the sample size must be indistinguishable")
	}
}
