package sqlserver

import (
	"context"
	"strconv"

	"github.com/branow/dbmap/internal/engine"
)

// modulesQuery fetches the bodies of the named objects.
//
// A definition holds tabs and newlines, which is why the reference
// implementation had to introduce a sentinel line carrying an object id and
// split the batch on it: it was reading sqlcmd's tab-separated stdout, where a
// procedure containing a tab was a bug waiting to be filed. A driver returns a
// body as an ordinary column value, so the sentinel, the raw output mode and
// the flag conflict that forced them are all gone. Nothing replaces them.
//
// Keys ride as parameters rather than as interpolated text. They come from this
// tool's own manifest, but a catalog query built by concatenation is a habit
// worth not having.
func modulesQuery(n int) string {
	return `SELECT s.name + '.' + o.name, LEFT(m.definition, ` +
		strconv.Itoa(engine.MaxDefinition) + `)
FROM sys.sql_modules m
JOIN sys.objects o ON o.object_id = m.object_id
JOIN sys.schemas s ON s.schema_id = o.schema_id
WHERE ` + inScope() + `
  AND s.name + '.' + o.name IN (` + engine.Placeholders("@p", n) + `)`
}

// parseModules reads body rows into a map keyed by object key. A body that came
// back empty is omitted rather than recorded as empty, so a later stage can
// tell "no body" from "a body that is blank".
func parseModules(rows [][]string) map[string]string {
	bodies := make(map[string]string, len(rows))
	for _, row := range rows {
		if len(row) < 2 || row[1] == "" {
			continue
		}
		bodies[row[0]] = row[1]
	}
	return bodies
}

// Modules fetches module bodies in batches, asking for room between each: a run
// that began on a healthy server still stops if it stops being one.
//
// Bodies come back exactly as the catalog holds them. Redaction happens on
// arrival above this boundary, before the cache is written — the cache will not
// accept anything the redactor has not seen.
func (e *Engine) Modules(
	ctx context.Context,
	conn engine.Conn,
	keys []string,
) (map[string]string, error) {
	bodies := map[string]string{}
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
