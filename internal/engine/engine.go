// Package engine is the boundary between the engine-agnostic pipeline above it
// and one database's catalog below.
//
// Every statement must be provably read-only and carry its engine's resource
// guard: an earlier tool starved a live SQL Server of memory. Both rules are
// enforced centrally in Query, because a rule applied per call site is a rule
// the next call site can forget.
package engine

import (
	"context"

	"github.com/branow/dbmap/internal/catalog"
	"github.com/branow/dbmap/internal/redact"
)

// Engine reads one database's catalog. An implementation owns its own type
// codes, system views and quoting, and maps all of it onto the catalog
// vocabulary before anything leaves this package.
type Engine interface {
	// Name is the engine name as config spells it: "sqlserver", "postgres".
	Name() string

	// Manifest lists every in-scope object with the cheap facts the planner
	// versions against. Row counts and sizes come from stored catalog totals,
	// never from a scan, which would cost the server a full read.
	Manifest(ctx context.Context, conn Conn) ([]catalog.Object, error)

	// Structure fetches columns, keys, indexes, parameters and synonym targets
	// for every in-scope object, keyed by catalog.Object.Key.
	Structure(ctx context.Context, conn Conn) (map[string]catalog.Structure, error)

	// Modules fetches the bodies of the module-bearing objects named by keys,
	// batched. Keys are catalog.Object.Key values; a key whose object has no
	// body is simply absent from the result.
	//
	// The result is redact.Body, which only the redactor can produce, so
	// "a secret in the schema never reaches disk" holds by construction rather
	// than by everyone remembering to redact.
	Modules(ctx context.Context, conn Conn, keys []string) (map[string]redact.Body, error)

	// Sample reads the first n rows of one table. The caller chooses the
	// projection, because which columns are safe to read is a PII rule and PII
	// rules live above the engine boundary.
	Sample(ctx context.Context, conn Conn, table Table, n int) (catalog.Sample, error)

	// Health reports whether the server has room for the next batch. An
	// unreadable reading is unknown, never healthy.
	Health(ctx context.Context, conn Conn) (Health, error)

	// Quote renders one identifier for this engine, escaping the terminator.
	Quote(identifier string) string
}

// Table names one table to sample and the columns chosen for it. The engine
// adds the row cap and the per-cell cap; it never widens the projection.
type Table struct {
	Schema string
	Name   string
	// Columns is the projection, already filtered. Empty means the engine opens
	// no query for this table at all.
	Columns []string
}

// Key is the name the pipeline indexes this table by.
func (t Table) Key() string { return t.Schema + "." + t.Name }

// Conn is the seam between an engine and a live pooled connection, an interface
// so a test can drive an engine with no database.
//
// An implementation runs what it is handed and does not re-check it: Query has
// already proved the statement read-only and guarded it.
type Conn interface {
	// Query runs one statement inside the session the guard asked for.
	Query(ctx context.Context, session Session, statement string, args ...any) (Rows, error)
}

// Rows is the cursor a Conn returns. *sql.Rows satisfies it as it stands.
type Rows interface {
	Columns() ([]string, error)
	Next() bool
	Scan(dest ...any) error
	Err() error
	Close() error
}

// Session is what the query path arranges around a statement rather than
// appends to it: the gate's regex is a guess about what SQL will do, while a
// read-only transaction is a promise the server itself keeps.
type Session struct {
	// ReadOnly asks the driver for a read-only transaction. SQL Server has no
	// such transaction mode and buys the same guarantee with a read-only
	// connection intent, so its guard leaves this false.
	ReadOnly bool
	// Set are session-local statements run before the statement itself. They are
	// the engine's own, never user input, and bypass the read-only gate because
	// a SET is not a read.
	Set []string
}

// Guard is one engine's resource cap, as data. Statement caps what a single
// statement may cost; Session caps what the connection around it may do.
type Guard struct {
	// Statement returns sql carrying the engine's per-statement cap. A nil
	// Statement means the engine's whole cap lives in the session.
	Statement func(sql string) string
	// Session is arranged around every statement this guard covers.
	Session Session
}

// Apply renders a statement under this guard.
func (g Guard) Apply(sql string) string {
	if g.Statement == nil {
		return sql
	}
	return g.Statement(sql)
}
