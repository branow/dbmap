package connect

import (
	"context"
	"database/sql"

	"github.com/branow/dbmap/internal/engine"
)

// conn adapts a pooled *sql.DB to engine.Conn. It runs what it is handed: the
// statement has already been proved read-only and guarded by engine.Query, and
// re-checking it here would be a second implementation of a rule that must have
// exactly one.
type conn struct {
	db *sql.DB
}

// Query runs one statement in the session the guard asked for.
//
// A session guard needs a transaction, because that is the only scope Postgres
// gives SET LOCAL and the only way to ask for a read-only access mode. Taking
// one also pins the statement and its settings to a single pooled connection,
// which a bare Query does not promise. A guard with no session — SQL Server,
// whose cap rides on the statement — skips all of that and uses the pool
// directly.
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
// cursor ends the transaction. A read-only transaction is rolled back rather
// than committed: there is nothing to commit, and rollback says so.
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
