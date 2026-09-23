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
		// Two very different failures land here, and drivers dial lazily so the
		// first read of a run is where a bad password or an unreachable host
		// shows up. If the server answers a trivial query the health views are
		// merely invisible to this account, which is unknown-not-healthy; if it
		// does not, the connection itself failed and that error is the only
		// useful thing to report.
		if _, alive := e.query(ctx, conn, "SELECT 1"); alive != nil {
			return engine.Classify(nil), err
		}
		return engine.Classify(nil), nil
	}
	return engine.Classify(parseHealth(rows)), nil
}
