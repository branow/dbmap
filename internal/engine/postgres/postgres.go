// Package postgres reads a Postgres catalog out of pg_catalog. It owns the
// relkind and prokind codes, the system schemas it refuses to index, and the
// shape of every query this tool sends to Postgres.
//
// Two things differ from the SQL Server engine, and both are decisions rather
// than omissions:
//
// There is no modify signal. Postgres records no modify_date for a relation or
// a routine, so every object leaves here with catalog.Signal absent. That is
// already the planner's "can never be proven untouched" case: an object with no
// signal always refetches, and the content fingerprint does all the real gating.
// No stage above this one names an engine to get that behaviour.
//
// The guard is a session rather than a suffix. Postgres has a read-only
// transaction mode, so the promise that nothing can be written is made by the
// server instead of inferred from the text of a statement. The read-only gate
// stays in front of it anyway: two independent guarantees, and the cheaper one
// runs first.
package postgres

import (
	"context"
	"strconv"
	"strings"

	"github.com/branow/dbmap/internal/engine"
)

// The session guard. Each value is a floor low enough that a catalog query
// never notices it and a runaway one cannot get far:
//
//	statement_timeout                 a query that hangs is cancelled, not waited on
//	work_mem                          one sort or hash cannot claim the server's memory
//	max_parallel_workers_per_gather   a plan cannot multiply its footprint per worker
const (
	StatementTimeout = "120s"
	WorkMem          = "16MB"
)

// Engine is the Postgres implementation of engine.Engine.
type Engine struct{}

// New returns the Postgres engine. It holds no state: a connection is passed
// per call, so one engine value serves every database in a run.
func New() *Engine { return &Engine{} }

// Name is the engine name as config spells it.
func (*Engine) Name() string { return "postgres" }

// Guard is this engine's resource cap. It is entirely a session: the read-only
// transaction is enforced by the server, and the three settings are session
// local, so they cannot leak onto the next borrower of a pooled connection.
func (*Engine) Guard() engine.Guard {
	return engine.Guard{
		Session: engine.Session{
			ReadOnly: true,
			Set: []string{
				"SET LOCAL statement_timeout = '" + StatementTimeout + "'",
				"SET LOCAL work_mem = '" + WorkMem + "'",
				"SET LOCAL max_parallel_workers_per_gather = 0",
			},
		},
	}
}

// Quote renders one identifier in double quotes, doubling an embedded quote so
// a name carrying one cannot end the quoting early.
func (*Engine) Quote(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
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

// halt asks the server for room before a stage sends anything.
func (e *Engine) halt(ctx context.Context, conn engine.Conn, stage string) error {
	health, err := e.Health(ctx, conn)
	if err != nil {
		return err
	}
	return engine.Assert(health, stage)
}

// flag reads a boolean the query already rendered as "1" or "0", so no parser
// here has to know how a driver spells true.
// flag reads a catalog boolean. Drivers disagree about how a SQL bit reaches a
// string scan — go-mssqldb hands back a Go bool, which reads as "true", while a
// catalog queried through other paths renders the same column as "1". Accepting
// both is boundary parsing, not defensiveness: reading only one spelling made
// every flag silently false, so nullable columns were written "not null" and a
// primary key was indexed as an ordinary index.
func flag(cell string) bool {
	switch strings.ToLower(strings.TrimSpace(cell)) {
	case "1", "true", "t", "yes", "y":
		return true
	}
	return false
}

// cells trims every cell of a row.
func cells(row []string) []string {
	out := make([]string, len(row))
	for i, cell := range row {
		out[i] = strings.TrimSpace(cell)
	}
	return out
}

// number reads a catalog count, treating anything unparseable as zero.
func number(cell string) int64 {
	n, err := strconv.ParseInt(cell, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

var _ engine.Engine = (*Engine)(nil)
