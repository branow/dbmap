package sqlserver

import (
	"context"
	"strconv"

	"github.com/branow/dbmap/internal/catalog"
	"github.com/branow/dbmap/internal/engine"
)

// manifestColumns is how wide a manifest row is. A shorter row is dropped
// rather than padded, so a truncated result can never become an object with
// invented facts.
const manifestColumns = 7

// manifestQuery lists every in-scope object with the cheap facts the planner
// versions against.
//
// Rows and size come from sys.dm_db_partition_stats: it reads page totals the
// engine already maintains, so the query costs the same against a 616M-row
// table as against an 11-row lookup. The index_id < 2 filter counts heap or
// clustered rows only, so a table with four non-clustered indexes is not
// counted five times.
//
// modify_date is converted to ISO 8601 in SQL rather than in Go, because that
// is the exact string the previous build wrote into the index and the exact
// string it is compared against. Rendering it twice, in two languages, is two
// places for a format to drift.
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

// number reads a catalog count, treating anything unparseable as zero: a
// missing page total means the object has no storage, not that the build
// should fail.
func number(cell string) int64 {
	n, err := strconv.ParseInt(cell, 10, 64)
	if err != nil {
		return 0
	}
	return n
}
