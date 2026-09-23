// Package sqlserver reads a SQL Server catalog, owning the sys.objects type
// codes, the excluded schemas and the shape of every query sent to SQL Server.
//
// Two rules shape every query. Row counts and sizes come from
// sys.dm_db_partition_stats, which reads stored page totals, because COUNT(*)
// and sp_spaceused scan, and scanning a large table can cost a server more than
// it can spare. And every statement carries OPTION (MAXDOP 1, MAX_GRANT_PERCENT = 1):
// the grant cap makes a hungry query spill to tempdb instead of taking RAM the
// operating system needs, and MAXDOP 1 stops a parallel plan from multiplying
// that footprint per thread.
package sqlserver

import (
	"context"
	"regexp"
	"strings"

	"github.com/branow/dbmap/internal/engine"
)

// MaxGrantPercent ceilings a statement's memory grant at 1% of the workspace
// limit.
const MaxGrantPercent = 1

// Option is the guard appended to every statement. It is a suffix rather than a
// session setting because SQL Server has no read-only transaction mode; the
// read-only connection intent carries that half.
const Option = "OPTION (MAXDOP 1, MAX_GRANT_PERCENT = 1)"

// trailingSemicolon is stripped before OPTION is appended, because a semicolon
// would have ended the statement OPTION belongs to.
var trailingSemicolon = regexp.MustCompile(`;\s*$`)

// Engine is the SQL Server implementation of engine.Engine.
type Engine struct{}

// New returns the SQL Server engine. It holds no state, so one value serves
// every database in a run.
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

// flag reads a catalog boolean. Drivers disagree about how a SQL bit reaches a
// string scan: go-mssqldb hands back a Go bool reading as "true", other paths
// render the same column as "1". Reading only one spelling made every flag
// silently false, so nullable columns were written "not null" and a primary key
// was indexed as an ordinary index.
func flag(cell string) bool {
	switch strings.ToLower(strings.TrimSpace(cell)) {
	case "1", "true", "t", "yes", "y":
		return true
	}
	return false
}

// cells trims every cell of a row, so a parser never sees CHAR padding.
func cells(row []string) []string {
	out := make([]string, len(row))
	for i, cell := range row {
		out[i] = strings.TrimSpace(cell)
	}
	return out
}

var _ engine.Engine = (*Engine)(nil)
