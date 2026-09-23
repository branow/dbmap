package connect

import (
	"context"
	"database/sql"

	"github.com/branow/dbmap/internal/engine"
)

// conn adapts a pooled *sql.DB to engine.Conn. It runs what it is handed:
// engine.Query has already proved the statement read-only and guarded it.
type conn struct {
	db *sql.DB
}

// Query runs one statement in the session the guard asked for. A session guard
// needs a transaction: it is the only scope Postgres gives SET LOCAL, the only
// way to ask for read-only access, and the only way to pin both to one pooled
// connection. A guard with no session uses the pool directly.
func (c conn) Query(
	ctx context.Context,
	session engine.Session,
	statement string,
	args ...any,
) (engine.Rows, error) {
	if !session.ReadOnly && len(session.Set) == 0 {
		return c.db.QueryContext(ctx, statement, args...)
	}

	tx, err := c.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: session.ReadOnly})
	if err != nil {
		return nil, err
	}
	for _, setting := range session.Set {
		if _, err := tx.ExecContext(ctx, setting); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
	}

	rows, err := tx.QueryContext(ctx, statement, args...)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return &scoped{Rows: rows, tx: tx}, nil
}

// scoped ties a cursor to the transaction that scoped its guard, so closing the
// cursor ends it. Rollback rather than commit, because there is nothing to
// commit and rollback says so.
type scoped struct {
	*sql.Rows
	tx *sql.Tx
}

func (s *scoped) Close() error {
	err := s.Rows.Close()
	if rollback := s.tx.Rollback(); err == nil {
		err = rollback
	}
	return err
}
