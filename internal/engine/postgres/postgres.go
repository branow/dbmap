// Package postgres reads a Postgres catalog out of pg_catalog, owning the
// relkind and prokind codes, the system schemas it refuses to index, and the
// shape of every query sent. There is no modify signal because Postgres records
// none, so the content fingerprint does all the gating here.
package postgres

import (
	"context"
	"strconv"
	"strings"

	"github.com/branow/dbmap/internal/engine"
)

// The session guard: caps low enough that a catalog query never notices them
// and a runaway one cannot get far.
const (
	StatementTimeout = "120s"
	WorkMem          = "16MB"
)

// Engine is the Postgres implementation of engine.Engine.
type Engine struct{}

// New returns the Postgres engine. It holds no state.
func New() *Engine { return &Engine{} }

// Name is the engine name as config spells it.
func (*Engine) Name() string { return "postgres" }

// Guard is entirely a session: the server enforces the read-only transaction,
// and SET LOCAL cannot leak onto the next borrower of a pooled connection.
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

// Quote renders one identifier in double quotes, doubling an embedded quote.
func (*Engine) Quote(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

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

// flag reads a catalog boolean. Both spellings are required: drivers spell a
// boolean "1" or "true", and reading only one silently falsified every
// nullability and primary key.
func flag(cell string) bool {
	switch strings.ToLower(strings.TrimSpace(cell)) {
	case "1", "true", "t", "yes", "y":
		return true
	}
	return false
}

func cells(row []string) []string {
	out := make([]string, len(row))
	for i, cell := range row {
		out[i] = strings.TrimSpace(cell)
	}
	return out
}

func number(cell string) int64 {
	n, err := strconv.ParseInt(cell, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

var _ engine.Engine = (*Engine)(nil)
