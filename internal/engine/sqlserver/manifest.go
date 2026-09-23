package sqlserver

import (
	"context"
	"strconv"

	"github.com/branow/dbmap/internal/catalog"
	"github.com/branow/dbmap/internal/engine"
)

// manifestColumns is how wide a manifest row is. A shorter row is dropped
// rather than padded, so a truncated result cannot invent facts.
const manifestColumns = 7

// manifestQuery lists every in-scope object with the cheap facts the planner
// versions against.
//
// Rows and size come from sys.dm_db_partition_stats, which reads maintained
// page totals and so costs the same at any table size; index_id < 2 counts heap
// or clustered rows only, so an indexed table is not counted once per index.
// modify_date is rendered as ISO 8601 in SQL rather than in Go because it is
// compared against the string the previous build wrote, and formatting it in
// two languages is two places to drift.
func manifestQuery() string {
	return `SELECT s.name, o.name, o.type,
  CONVERT(varchar(19), o.modify_date, 126),
  ISNULL(st.row_total, 0), ISNULL(st.kb_total, 0), ISNULL(tg.n, 0)
FROM sys.objects o
JOIN sys.schemas s ON s.schema_id = o.schema_id
OUTER APPLY (
  SELECT SUM(CASE WHEN p.index_id < 2 THEN p.row_count ELSE 0 END) AS row_total,
         SUM(p.used_page_count) * 8 AS kb_total
  FROM sys.dm_db_partition_stats p WHERE p.object_id = o.object_id
) st
OUTER APPLY (
  SELECT COUNT(*) AS n FROM sys.triggers t WHERE t.parent_id = o.object_id
) tg
WHERE ` + inScope() + `
ORDER BY s.name, o.name`
}

// parseManifest turns catalog rows into objects, dropping any type the table
// does not cover rather than guessing a kind for it.
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
			Modified: catalog.Signal(row[3]),
			Rows:     number(row[4]),
			KB:       number(row[5]),
			Triggers: int(number(row[6])),
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

// number reads a catalog count. Unparseable is zero: a missing page total means
// no storage, not a failed build.
func number(cell string) int64 {
	n, err := strconv.ParseInt(cell, 10, 64)
	if err != nil {
		return 0
	}
	return n
}
