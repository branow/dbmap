// Package enginetest is the in-memory Conn both engines are tested against.
package enginetest

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/branow/dbmap/internal/engine"
)

// Conn is a scripted engine.Conn. Answers match by substring in registration
// order.
type Conn struct {
	answers []answer
	calls   []Call
}

// Call is one statement that reached the connection, with its session.
type Call struct {
	Session   engine.Session
	Statement string
	Args      []any
}

type answer struct {
	match string
	rows  [][]string
	err   error
}

// New returns a connection that answers nothing. An unmatched statement returns
// no rows rather than failing.
func New() *Conn { return &Conn{} }

// On registers rows for every statement containing match.
func (c *Conn) On(match string, rows [][]string) *Conn {
	c.answers = append(c.answers, answer{match: match, rows: rows})
	return c
}

// Fail registers an error for every statement containing match.
func (c *Conn) Fail(match string, err error) *Conn {
	c.answers = append(c.answers, answer{match: match, err: err})
	return c
}

func (c *Conn) Query(
	_ context.Context,
	session engine.Session,
	statement string,
	args ...any,
) (engine.Rows, error) {
	c.calls = append(c.calls, Call{Session: session, Statement: statement, Args: args})
	for _, a := range c.answers {
		if strings.Contains(statement, a.match) {
			if a.err != nil {
				return nil, a.err
			}
			return &rows{data: a.rows}, nil
		}
	}
	return &rows{}, nil
}

// Calls is every call that reached the connection, in order.
func (c *Conn) Calls() []Call { return c.calls }

// Statements is every statement that reached the connection, in order.
func (c *Conn) Statements() []string {
	out := make([]string, len(c.calls))
	for i, call := range c.calls {
		out[i] = call.Statement
	}
	return out
}

// Last is the most recent statement.
func (c *Conn) Last() string {
	if len(c.calls) == 0 {
		return ""
	}
	return c.calls[len(c.calls)-1].Statement
}

// Only is the single statement sent, or an error if there was not exactly one.
func (c *Conn) Only() (string, error) {
	if len(c.calls) != 1 {
		return "", errors.New("enginetest: expected exactly one statement")
	}
	return c.calls[0].Statement, nil
}

type rows struct {
	data [][]string
	at   int
}

func (r *rows) Columns() ([]string, error) {
	width := 0
	if len(r.data) > 0 {
		width = len(r.data[0])
	}
	names := make([]string, width)
	for i := range names {
		names[i] = "c"
	}
	return names, nil
}

func (r *rows) Next() bool { r.at++; return r.at <= len(r.data) }
func (r *rows) Err() error { return nil }
func (r *rows) Close() error {
	return nil
}

// Scan fills sql.NullString destinations; an empty cell arrives as NULL, as a
// real driver does.
func (r *rows) Scan(dest ...any) error {
	row := r.data[r.at-1]
	for i := range dest {
		p, ok := dest[i].(*sql.NullString)
		if !ok {
			return errors.New("enginetest: the query path must scan into *sql.NullString")
		}
		if i < len(row) {
			*p = sql.NullString{String: row[i], Valid: row[i] != ""}
			continue
		}
		*p = sql.NullString{}
	}
	return nil
}
