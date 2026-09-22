// Package sqlserver reads a SQL Server catalog. It owns the sys.objects type
// codes, the excluded schemas and the shape of every query this tool sends to
// SQL Server, and it hands the result up as catalog vocabulary.
//
// Two rules shape every query here, both from the same measured incident:
//
// Row counts and sizes come from sys.dm_db_partition_stats, which reads stored
// page totals. COUNT(*) and sp_spaceused scan, and the estate this was measured
// against holds 606 tables over 563 GB with a largest table of 616,802,364 rows
// — scanning it is how a build stops being a build and becomes an outage.
//
// Every statement carries OPTION (MAXDOP 1, MAX_GRANT_PERCENT = 1). The grant
// cap is the load-bearing half: a query wanting more memory spills to tempdb
// instead of taking RAM the operating system needs. MAXDOP 1 stops a parallel
// plan from multiplying the footprint per thread.
package sqlserver

import (
	"context"
	"regexp"
	"strings"

	"github.com/branow/dbmap/internal/engine"
)

// MaxGrantPercent ceilings a statement's memory grant at 1% of the workspace
// limit. Verified working on SQL Server 2019 (15.0.4430.1, compat 150).
const MaxGrantPercent = 1

// Option is the guard appended to every statement. It is a suffix rather than
// a session setting because SQL Server has no read-only transaction mode to
// hang a session guarantee on; the read-only connection intent carries that
// half, and this carries the resource half.
const Option = "OPTION (MAXDOP 1, MAX_GRANT_PERCENT = 1)"

// trailingSemicolon is stripped before the OPTION clause is appended, because
// OPTION belongs to the statement and a semicolon would have ended it.
var trailingSemicolon = regexp.MustCompile(`;\s*$`)

// Engine is the SQL Server implementation of engine.Engine.
type Engine struct{}

// New returns the SQL Server engine. It holds no state: a connection is passed
// per call, so one engine value serves every database in a run.
func New() *Engine { return &Engine{} }

// Name is the engine name as config spells it.
func (*Engine) Name() string { return "sqlserver" }

// Guard is this engine's resource cap.
func (*Engine) Guard() engine.Guard {
	return engine.Guard{Statement: withOption}
}

// withOption appends the grant cap to one statement.
func withOption(sql string) string {
	return trailingSemicolon.ReplaceAllString(sql, "") + "\n" + Option
}

// Quote renders one identifier in brackets, doubling a closing bracket so a
// name carrying one cannot end the quoting early.
func (*Engine) Quote(identifier string) string {
	return "[" + strings.ReplaceAll(identifier, "]", "]]") + "]"
}

// query runs one statement through the central safety path.
func (e *Engine) query(
	ctx context.Context,
	conn engine.Conn,
	statement string,
	args ...any,
) ([][]string, error) {
	return engine.Query(ctx, conn, e.Guard(), statement, args...)
}

// halt asks the server for room before a stage sends anything, so a run that
// began on a healthy server still stops if it stops being one.
func (e *Engine) halt(ctx context.Context, conn engine.Conn, stage string) error {
	health, err := e.Health(ctx, conn)
	if err != nil {
		return err
	}
	return engine.Assert(health, stage)
}

// flag reads a SQL Server bit column, which arrives as "1" or "0".
func flag(cell string) bool { return strings.TrimSpace(cell) == "1" }

// cells trims every cell of a row, because the catalog pads nothing but the
// parsers should not care either way.
func cells(row []string) []string {
	out := make([]string, len(row))
	for i, cell := range row {
		out[i] = strings.TrimSpace(cell)
	}
	return out
}

var _ engine.Engine = (*Engine)(nil)
