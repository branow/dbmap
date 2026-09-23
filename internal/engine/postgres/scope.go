package postgres

import (
	"strings"

	"github.com/branow/dbmap/internal/catalog"
)

// Code is a pg_catalog kind code, prefixed by its catalog: relkind and prokind
// both spell a code 'p'.
type Code string

// Codes is where a Postgres catalog code becomes a catalog kind. Triggers are
// absent as on SQL Server; aggregate and window functions are absent because
// pg_get_functiondef cannot render them.
var Codes = []struct {
	Code Code
	Kind catalog.Kind
}{
	{"rel:r", catalog.Table}, // ordinary table
	{"rel:p", catalog.Table}, // partitioned table
	{"rel:v", catalog.View},  // view
	// A materialized view holds rows, but what it IS is its query.
	{"rel:m", catalog.View},
	{"pro:f", catalog.Function},  // function
	{"pro:p", catalog.Procedure}, // stored procedure
}

// RelKinds are the pg_class relkind codes the index covers, derived from Codes.
func RelKinds() string { return codesOf("rel:") }

// ProKinds are the pg_proc prokind codes the index covers.
func ProKinds() string { return codesOf("pro:") }

func codesOf(prefix string) string {
	var quoted []string
	for _, row := range Codes {
		if strings.HasPrefix(string(row.Code), prefix) {
			quoted = append(quoted, "'"+strings.TrimPrefix(string(row.Code), prefix)+"'")
		}
	}
	return strings.Join(quoted, ",")
}

// ExcludedSchemas hold Postgres' own catalog.
var ExcludedSchemas = []string{"pg_catalog", "information_schema", "pg_toast"}

var kinds = func() map[Code]catalog.Kind {
	byCode := make(map[Code]catalog.Kind, len(Codes))
	for _, row := range Codes {
		byCode[row.Code] = row.Kind
	}
	return byCode
}()

// kindOf maps a tagged catalog code onto a catalog kind, reporting rather than
// guessing.
func kindOf(code string) (catalog.Kind, bool) {
	kind, ok := kinds[Code(strings.TrimSpace(code))]
	return kind, ok
}

// schemaScope is the predicate every catalog query shares. The LIKE clauses
// cover per-session temporary schemas, which are named at runtime.
func schemaScope(column string) string {
	quoted := make([]string, len(ExcludedSchemas))
	for i, name := range ExcludedSchemas {
		quoted[i] = "'" + name + "'"
	}
	return column + " NOT IN (" + strings.Join(quoted, ",") + ")\n" +
		"  AND " + column + " NOT LIKE 'pg\\_temp\\_%'\n" +
		"  AND " + column + " NOT LIKE 'pg\\_toast\\_temp\\_%'"
}

// routineCTE names one row per routine name, not per overload: the index is
// keyed by name, so the lowest oid stands for a group of overloads and every
// routine query starts here to agree on which. The cast through bigint is
// load-bearing — there is no min() over oid.
func routineCTE() string {
	return `WITH routine AS (
  SELECT n.nspname AS schema, p.proname AS name, MIN(p.oid::bigint)::oid AS oid
  FROM pg_catalog.pg_proc p
  JOIN pg_catalog.pg_namespace n ON n.oid = p.pronamespace
  WHERE p.prokind IN (` + ProKinds() + `)
  AND ` + schemaScope("n.nspname") + `
  GROUP BY n.nspname, p.proname
)`
}
