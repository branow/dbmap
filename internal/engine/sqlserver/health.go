package sqlserver

import (
	"context"
	"strconv"

	"github.com/branow/dbmap/internal/engine"
)

const healthColumns = 4

// HealthQuery reads four DMVs and nothing else, so asking about memory pressure
// cannot itself cause any.
func HealthQuery() string {
	return `SELECT available_physical_memory_kb / 1048576,
  system_memory_state_desc,
  (SELECT COUNT(*) FROM sys.dm_exec_query_memory_grants WHERE grant_time IS NULL),
  (SELECT CONVERT(int, process_physical_memory_low) FROM sys.dm_os_process_memory)
FROM sys.dm_os_sys_memory`
}

// parseHealth reads one health row; a missing or short row is nil, which
// Classify reports as unknown.
func parseHealth(rows [][]string) *engine.Reading {
	if len(rows) == 0 || len(rows[0]) < healthColumns {
		return nil
	}
	row := cells(rows[0])
	available, err := strconv.ParseFloat(row[0], 64)
	if err != nil {
		return nil
	}
	waiting, err := strconv.Atoi(row[2])
	if err != nil {
		waiting = 0
	}
	return &engine.Reading{
		MemoryVisible: true,
		AvailableGB:   available,
		State:         row[1],
		MemoryLow:     flag(row[3]),
		Waiting:       waiting,
	}
}

// Health reports whether the server has room for the next batch. A failed query
// is a reading that could not be taken, not a caller's error.
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
