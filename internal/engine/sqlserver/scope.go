package sqlserver

import (
	"strings"

	"github.com/branow/dbmap/internal/catalog"
)

// Type is a sys.objects type code.
type Type string

// Types is where a SQL Server type code becomes a catalog kind. Triggers are
// deliberately absent; the parent records a trigger count instead.
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

// ExcludedSchemas are dropped before anything is fetched. `cdc` is generated
// Change Data Capture functions, two per tracked table, carrying nothing the
// tracked table lacks.
var ExcludedSchemas = []string{"cdc"}

var kinds = func() map[Type]catalog.Kind {
	byType := make(map[Type]catalog.Kind, len(Types))
	for _, row := range Types {
		byType[row.Type] = row.Kind
	}
	return byType
}()

// kindOf maps a type code onto a catalog kind, reporting an unrecognised one
// rather than guessing.
func kindOf(code string) (catalog.Kind, bool) {
	kind, ok := kinds[Type(strings.TrimSpace(code))]
	return kind, ok
}

// typeList renders the type codes for an IN predicate.
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

// inScope is the predicate every catalog query shares.
func inScope() string {
	return "o.is_ms_shipped = 0 AND o.type IN (" + typeList() +
		") AND s.name NOT IN (" + schemaList() + ")"
}
