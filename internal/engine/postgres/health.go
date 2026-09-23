package postgres

import (
	"context"
	"strconv"

	"github.com/branow/dbmap/internal/engine"
)

// healthColumns is how wide a health row is; anything narrower is unknown.
const healthColumns = 2

// HealthQuery asks the server what it can say about its own load.
//
// Postgres shows an ordinary role no operating-system memory, so the reading
// reports memory invisible rather than zero free bytes, which would trip the
// floor on every healthy server. Sessions queued behind a lock stand in for
// queries queued for a memory grant. An ordinary role may see fewer
// pg_stat_activity rows than a superuser, but an undercount reads as healthier,
// never worse.
func HealthQuery() string {
	return `SELECT CASE WHEN pg_catalog.pg_is_in_recovery()
    THEN 'standby' ELSE 'primary' END,
  (SELECT count(*) FROM pg_catalog.pg_stat_activity WHERE wait_event_type = 'Lock')`
}

// parseHealth reads one health row. A missing or short row returns nil, which
// Classify reports as unknown rather than as healthy.
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
		// A reading that did not arrive is unknown, never healthy.
		return engine.Classify(nil), nil
	}
	return engine.Classify(parseHealth(rows)), nil
}
