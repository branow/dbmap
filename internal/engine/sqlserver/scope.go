package sqlserver

import (
	"strings"

	"github.com/branow/dbmap/internal/catalog"
)

// Type is a sys.objects type code. Nothing above the engine boundary sees one.
type Type string

// Types is the one place a SQL Server type code becomes a catalog kind, as data
// rather than a switch so a new code is a new row.
//
// Triggers are deliberately absent: in practice they are generated changelog
// writers carrying nothing a reader of the tracked table lacks, so the index
// records a trigger count on the parent and describes none of them.
var Types = []struct {
	Type Type
	Kind catalog.Kind
}{
	{"U", catalog.Table},     // user table
	{"V", catalog.View},      // view
	{"P", catalog.Procedure}, // stored procedure
	{"PC", catalog.Procedure},
	{"FN", catalog.Function}, // scalar function
	{"IF", catalog.Function}, // inline table-valued function
	{"TF", catalog.Function}, // table-valued function
	{"FT", catalog.Function}, // CLR table-valued function
	{"SN", catalog.Synonym},  // synonym
}

// ExcludedSchemas are dropped before anything is fetched. `cdc` is almost
// entirely auto-generated Change Data Capture functions, two per tracked table
// and carrying nothing a reader of that table lacks, so excluding it removes a
// large share of the describe workload at almost no cost.
var ExcludedSchemas = []string{"cdc"}

var kinds = func() map[Type]catalog.Kind {
	byType := make(map[Type]catalog.Kind, len(Types))
	for _, row := range Types {
		byType[row.Type] = row.Kind
	}
	return byType
}()

// kindOf maps a type code onto a catalog kind. An unrecognised code is reported
// rather than guessed at, so it cannot slip into the index unclassified.
func kindOf(code string) (catalog.Kind, bool) {
	kind, ok := kinds[Type(strings.TrimSpace(code))]
	return kind, ok
}

// typeList renders the type codes for an IN predicate, from this package's own
// constants rather than user input.
func typeList() string {
	quoted := make([]string, len(Types))
	for i, row := range Types {
		quoted[i] = "'" + string(row.Type) + "'"
	}
	return strings.Join(quoted, ",")
}

// schemaList renders the excluded schemas for a NOT IN predicate.
func schemaList() string {
	quoted := make([]string, len(ExcludedSchemas))
	for i, name := range ExcludedSchemas {
		quoted[i] = "'" + name + "'"
	}
	return strings.Join(quoted, ",")
}

// inScope is the predicate every catalog query shares: nothing Microsoft
// shipped, nothing outside the type table, nothing in an excluded schema.
func inScope() string {
	return "o.is_ms_shipped = 0 AND o.type IN (" + typeList() +
		") AND s.name NOT IN (" + schemaList() + ")"
}
