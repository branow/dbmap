package postgres

import (
	"context"
	"strconv"

	"github.com/branow/dbmap/internal/engine"
	"github.com/branow/dbmap/internal/redact"
)

// modulesQuery fetches the bodies of the named objects.
//
// Postgres stores no definition text for a view or a routine — it stores the
// parsed tree and renders the text back on demand — so the body comes from
// pg_get_viewdef and pg_get_functiondef rather than from a column. What comes
// back is therefore normalised rather than as-typed, which suits the
// fingerprint: a deployment tool that reformats a body does not read as an edit
// here, which is the same property the CRLF normalisation buys on SQL Server.
func modulesQuery(n int) string {
	list := engine.Placeholders("$", n)
	limit := strconv.Itoa(engine.MaxDefinition)
	return routineCTE() + `
SELECT n.nspname || '.' || c.relname,
  left(pg_catalog.pg_get_viewdef(c.oid, true), ` + limit + `)
FROM pg_catalog.pg_class c
JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind IN ('v', 'm')
  AND n.nspname || '.' || c.relname IN (` + list + `)
UNION ALL
SELECT r.schema || '.' || r.name,
  left(pg_catalog.pg_get_functiondef(r.oid), ` + limit + `)
FROM routine r
WHERE r.schema || '.' || r.name IN (` + list + `)`
}

// parseModules reads body rows into a map keyed by object key. A body that came
// back empty is omitted rather than recorded as empty.
//
// Redaction happens here, on the row, for the same reason it does on the other
// engine: this is the moment a body arrives, and redact.Body is the only thing
// this function can return.
func parseModules(rows [][]string) map[string]redact.Body {
	bodies := make(map[string]redact.Body, len(rows))
	for _, row := range rows {
		if len(row) < 2 || row[1] == "" {
			continue
		}
		bodies[row[0]] = redact.Text(row[1])
	}
	return bodies
}

// Modules fetches module bodies in batches, asking for room between each. A
// body is redacted the moment it arrives, before it can be cached and long
// before it can reach a prompt.
func (e *Engine) Modules(
	ctx context.Context,
	conn engine.Conn,
	keys []string,
) (map[string]redact.Body, error) {
	bodies := map[string]redact.Body{}
	batches := engine.Batch(keys, engine.ModuleBatch)

	for i, batch := range batches {
		stage := "modules batch " + strconv.Itoa(i+1) + "/" + strconv.Itoa(len(batches))
		if err := e.halt(ctx, conn, stage); err != nil {
			return nil, err
		}
		rows, err := e.query(ctx, conn, modulesQuery(len(batch)), engine.Args(batch)...)
		if err != nil {
			return nil, err
		}
		for key, body := range parseModules(rows) {
			bodies[key] = body
		}
	}
	return bodies, nil
}
