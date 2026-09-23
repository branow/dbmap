// Package postgres reads a Postgres catalog out of pg_catalog, owning the
// relkind and prokind codes, the system schemas it refuses to index, and the
// shape of every query sent to Postgres.
//
// Two things differ from the SQL Server engine, both deliberate. There is no
// modify signal, because Postgres records none; an object with no signal always
// refetches and the content fingerprint does the real gating. And the guard is
// a session rather than a statement suffix, because Postgres has a read-only
// transaction mode, so the server makes the promise instead of it being
// inferred from statement text. The read-only gate still runs in front of it.
package postgres

import (
	"context"
	"strconv"
	"strings"

	"github.com/branow/dbmap/internal/engine"
)

// The session guard: caps low enough that a catalog query never notices them
// and a runaway one cannot get far. See Guard for the third setting.
const (
	StatementTimeout = "120s"
	WorkMem          = "16MB"
)

// Engine is the Postgres implementation of engine.Engine.
type Engine struct{}

// New returns the Postgres engine. It holds no state, so one value serves every
// database in a run.
func New() *Engine { return &Engine{} }

// Name is the engine name as config spells it.
func (*Engine) Name() string { return "postgres" }

// Guard is this engine's resource cap, entirely a session: the server enforces
// the read-only transaction, and SET LOCAL cannot leak onto the next borrower
// of a pooled connection.
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

// flag reads a catalog boolean. Drivers disagree about how a boolean reaches a
// string scan, spelling it "1" or "true", and reading only one spelling made
// every flag silently false: nullable columns were written "not null" and a
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
