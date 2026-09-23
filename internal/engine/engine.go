// Package engine is the boundary between the engine-agnostic pipeline above it
// and one database's catalog below. The resource guard is applied centrally in
// Query, never at a call site.
package engine

import (
	"context"

	"github.com/branow/dbmap/internal/catalog"
	"github.com/branow/dbmap/internal/redact"
)

// Engine reads one database's catalog, mapping its type codes, system views and
// quoting onto the catalog vocabulary before anything leaves this package.
type Engine interface {
	Name() string

	// Manifest reads object facts from stored catalog totals, never a scan.
	Manifest(ctx context.Context, conn Conn) ([]catalog.Object, error)

	Structure(ctx context.Context, conn Conn) (map[string]catalog.Structure, error)

	// Modules returns redact.Body, which only the redactor can produce, so an
	// unredacted body cannot leave this package.
	Modules(ctx context.Context, conn Conn, keys []string) (map[string]redact.Body, error)

	// Sample takes an already-chosen projection: which columns are safe to read
	// is a PII rule, and those live above this boundary.
	Sample(ctx context.Context, conn Conn, table Table, n int) (catalog.Sample, error)

	Health(ctx context.Context, conn Conn) (Health, error)

	// Quote renders one identifier, escaping the terminator.
	Quote(identifier string) string
}

// Table names one table to sample. The engine adds the row and per-cell caps
// and never widens the projection; an empty Columns opens no query at all.
type Table struct {
	Schema  string
	Name    string
	Columns []string
}

func (t Table) Key() string { return t.Schema + "." + t.Name }

// Conn is the seam between an engine and a live pooled connection.
type Conn interface {
	Query(ctx context.Context, session Session, statement string, args ...any) (Rows, error)
}

// Rows is the cursor a Conn returns; *sql.Rows satisfies it as it stands.
type Rows interface {
	Columns() ([]string, error)
	Next() bool
	Scan(dest ...any) error
	Err() error
	Close() error
}

// Session is what the query path arranges around a statement rather than
// appends to it. A read-only transaction is a promise the server keeps.
type Session struct {
	// ReadOnly asks the driver for a read-only transaction. SQL Server has no
	// such mode and buys the guarantee with a read-only connection intent
	// instead, so its guard leaves this false.
	ReadOnly bool
	// Set are session-local statements run first, and are the engine's own.
	Set []string
}

// Guard is one engine's resource cap, as data. A nil Statement means the whole
// cap lives in the session.
type Guard struct {
	Statement func(sql string) string
	Session   Session
}

func (g Guard) Apply(sql string) string {
	if g.Statement == nil {
		return sql
	}
	return g.Statement(sql)
}
