package postgres

import (
	"context"

	"github.com/branow/dbmap/internal/catalog"
	"github.com/branow/dbmap/internal/engine"
)

// manifestColumns is how wide a manifest row is; a shorter row is dropped.
const manifestColumns = 6

// manifestQuery lists every in-scope object with the facts the planner versions
// against. reltuples is the planner's estimate; a never-analysed table reports
// -1, which GREATEST folds to 0. pg_total_relation_size is asked only of
// relations that have storage, since older servers raise for a view.
func manifestQuery() string {
	return routineCTE() + `
SELECT n.nspname, c.relname, 'rel:' || c.relkind::text,
  CASE WHEN c.relkind IN ('r','p','m') THEN GREATEST(c.reltuples, 0) ELSE 0 END::bigint,
  CASE WHEN c.relkind IN ('r','p','m')
       THEN pg_catalog.pg_total_relation_size(c.oid) / 1024 ELSE 0 END,
  (SELECT count(*) FROM pg_catalog.pg_trigger g
   WHERE g.tgrelid = c.oid AND NOT g.tgisinternal)
FROM pg_catalog.pg_class c
JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind IN (` + RelKinds() + `)
  AND ` + schemaScope("n.nspname") + `
UNION ALL
SELECT r.schema, r.name, 'pro:' || p.prokind::text, 0, 0, 0
FROM routine r
JOIN pg_catalog.pg_proc p ON p.oid = r.oid
ORDER BY 1, 2`
}

// parseManifest turns catalog rows into objects, dropping any code the kind
// table does not cover.
func parseManifest(rows [][]string) []catalog.Object {
	objects := make([]catalog.Object, 0, len(rows))
	for _, raw := range rows {
		if len(raw) < manifestColumns {
			continue
		}
		row := cells(raw)
		kind, ok := kindOf(row[2])
		if !ok {
			continue
		}
		objects = append(objects, catalog.Object{
			Schema:   row[0],
			Name:     row[1],
			Kind:     kind,
			Rows:     number(row[3]),
			KB:       number(row[4]),
			Triggers: int(number(row[5])),
		})
	}
	return objects
}

func (e *Engine) Manifest(ctx context.Context, conn engine.Conn) ([]catalog.Object, error) {
	if err := e.halt(ctx, conn, "the manifest"); err != nil {
		return nil, err
	}
	rows, err := e.query(ctx, conn, manifestQuery())
	if err != nil {
		return nil, err
	}
	return parseManifest(rows), nil
}
