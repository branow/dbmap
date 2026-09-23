package postgres

import (
	"context"

	"github.com/branow/dbmap/internal/catalog"
	"github.com/branow/dbmap/internal/engine"
)

// manifestColumns is how wide a manifest row is. A shorter row is dropped
// rather than padded.
const manifestColumns = 6

// manifestQuery lists every in-scope object with the cheap facts the planner
// versions against.
//
// pg_class.reltuples is the planner's ESTIMATE, maintained by ANALYZE and
// VACUUM. It is not exact and does not need to be: it decides sample depth and
// nothing else, and the alternative — count(*) — reads every page of every
// table, which is the query shape this whole tool is built to never send. A
// table that has never been analysed reports -1, which GREATEST folds to 0.
//
// pg_total_relation_size reads stored page counts, including indexes and
// TOAST, and is asked only of relations that have storage: a view has none and
// older servers raise rather than answer.
//
// There is no modify signal to select. Postgres keeps none, so every object
// here leaves with catalog.Signal absent by construction.
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
// table does not cover rather than guessing a kind for it.
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

// Manifest fetches the manifest for the database the connection is open on.
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
