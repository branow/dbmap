package postgres

import (
	"context"
	"strconv"

	"github.com/branow/dbmap/internal/engine"
)

const healthColumns = 2

// HealthQuery asks the server what it can say about its own load. An ordinary
// role sees no OS memory, so the reading reports memory invisible rather than
// zero free bytes, which would trip the floor on every healthy server.
func HealthQuery() string {
	return `SELECT CASE WHEN pg_catalog.pg_is_in_recovery()
    THEN 'standby' ELSE 'primary' END,
  (SELECT count(*) FROM pg_catalog.pg_stat_activity WHERE wait_event_type = 'Lock')`
}

// parseHealth reads one health row; a missing or short row is nil, which
// Classify reports as unknown.
func parseHealth(rows [][]string) *engine.Reading {
	if len(rows) == 0 || len(rows[0]) < healthColumns {
		return nil
	}
	row := cells(rows[0])
	waiting, err := strconv.Atoi(row[1])
	if err != nil {
		return nil
	}
	return &engine.Reading{MemoryVisible: false, State: row[0], Waiting: waiting}
}

// Health reports whether the server has room for the next batch.
func (e *Engine) Health(ctx context.Context, conn engine.Conn) (engine.Health, error) {
	rows, err := e.query(ctx, conn, HealthQuery())
	if err != nil {
		return engine.Classify(nil), nil
	}
	return engine.Classify(parseHealth(rows)), nil
}
