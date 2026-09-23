package sqlserver

import (
	"context"
	"strconv"

	"github.com/branow/dbmap/internal/engine"
	"github.com/branow/dbmap/internal/redact"
)

// modulesQuery fetches the bodies of the named objects. Keys ride as parameters
// rather than interpolated text.
func modulesQuery(n int) string {
	return `SELECT s.name + '.' + o.name, LEFT(m.definition, ` +
		strconv.Itoa(engine.MaxDefinition) + `)
FROM sys.sql_modules m
JOIN sys.objects o ON o.object_id = m.object_id
JOIN sys.schemas s ON s.schema_id = o.schema_id
WHERE ` + inScope() + `
  AND s.name + '.' + o.name IN (` + engine.Placeholders("@p", n) + `)`
}

// parseModules reads body rows into a map keyed by object key. An empty body is
// omitted rather than stored, so a later stage can tell "no body" from "blank".
// Redaction happens here, the moment a body arrives, so nothing downstream can
// hold an unredacted one.
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

// Modules fetches module bodies in batches, asking for room between each so a
// run that began on a healthy server still stops if it stops being one.
// Procedure bodies are where credentials turn up in practice, so each body is
// redacted on arrival and carries the tally of what was found.
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
