package postgres

import (
	"context"
	"errors"
	"strconv"

	"github.com/branow/dbmap/internal/engine"
)

// healthColumns is how wide a health row is. Anything narrower is not a
// reading.
const healthColumns = 2

// HealthQuery asks the server what it can say about its own load.
//
// Postgres exposes no equivalent of SQL Server's operating-system memory DMVs
// to an ordinary role, so this reading carries no memory at all and says so.
// That is not a gap papered over: a reading that claimed zero bytes free would
// trip the memory floor on every healthy server, so the conditions about memory
// are simply not asked of an engine that cannot see it.
//
// What is left is still worth asking. Sessions queued behind a lock are this
// engine's equivalent of queries queued for a memory grant — work the server
// has accepted and cannot start — and a catalog read that joins a contended
// table joins the queue. pg_stat_activity may show an ordinary role fewer rows
// than it shows a superuser; an undercount reads as healthier, never as worse,
// so the floor is never crossed on a reading the account could not fully take.
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
		// A refusal is this tool's own SQL being wrong, which is a bug and not
		// a reading. Everything else is a reading that could not be taken.
		var refused *engine.RefusedError
		if errors.As(err, &refused) {
			return engine.Health{}, err
		}
		return engine.Classify(nil), nil
	}
	return engine.Classify(parseHealth(rows)), nil
}
