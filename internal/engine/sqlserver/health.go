package sqlserver

import (
	"context"
	"errors"
	"strconv"

	"github.com/branow/dbmap/internal/engine"
)

// healthColumns is how wide a health row is. Anything narrower is not a
// reading, and a reading that is not a reading is unknown.
const healthColumns = 4

// HealthQuery asks the server how much room it has left.
//
// It reads four DMVs and nothing else: no user table, no sort, no grouping, no
// DISTINCT, so the question about memory pressure cannot itself cause any. The
// four facts are the ones the measured incident turned on — operating-system
// free memory, the server's own word for its memory state, how many queries are
// queued for a grant, and whether the server has signalled physical memory low.
func HealthQuery() string {
	return `SELECT available_physical_memory_kb / 1048576,
  system_memory_state_desc,
  (SELECT COUNT(*) FROM sys.dm_exec_query_memory_grants WHERE grant_time IS NULL),
  (SELECT CONVERT(int, process_physical_memory_low) FROM sys.dm_os_process_memory)
FROM sys.dm_os_sys_memory`
}

// parseHealth reads one health row. A missing or short row returns nil, which
// Classify reports as unknown rather than as healthy — an account that cannot
// see the DMVs cannot see the floor either, and not seeing the floor is not the
// same as being above it.
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

// Health reports whether the server has room for the next batch. A failed
// health query is not an error the caller has to handle: it is a reading that
// could not be taken, which is unknown, which is not healthy.
func (e *Engine) Health(ctx context.Context, conn engine.Conn) (engine.Health, error) {
	rows, err := e.query(ctx, conn, HealthQuery())
	if err != nil {
		// A refusal is this tool's own SQL being wrong, which is a bug and not
		// a reading. Everything else — a denied DMV, a dropped connection — is
		// a reading that could not be taken.
		var refused *engine.RefusedError
		if errors.As(err, &refused) {
			return engine.Health{}, err
		}
		return engine.Classify(nil), nil
	}
	return engine.Classify(parseHealth(rows)), nil
}
