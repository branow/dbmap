package index

import (
	"context"
	"strconv"

	"github.com/branow/dbmap/internal/catalog"
	"github.com/branow/dbmap/internal/connect"
	"github.com/branow/dbmap/internal/engine"
	"github.com/branow/dbmap/internal/redact"
)

// Reader is the half of engine.Engine a build reads through. It is restated
// here rather than taken as engine.Engine so that a build runs against a fake
// with no database behind it, and so the pipeline cannot reach a method the
// stages below have no business calling.
type Reader interface {
	Manifest(ctx context.Context, conn engine.Conn) ([]catalog.Object, error)
	Structure(ctx context.Context, conn engine.Conn) (map[string]catalog.Structure, error)
	Modules(ctx context.Context, conn engine.Conn, keys []string) (map[string]redact.Body, error)
	Sample(ctx context.Context, conn engine.Conn, table engine.Table, n int) (catalog.Sample, error)
	Health(ctx context.Context, conn engine.Conn) (engine.Health, error)
}

// Source is the live database half of a build: what to read, what to read it
// through, and how to let go of it.
//
// Close is not merely cleanup. The build calls it after the last query and
// BEFORE the first model call, because the describe stage must never hold a
// database connection: a describe run is minutes of network latency against a
// third party, and a pooled connection held open across it is a connection the
// server cannot reuse for anything. Making Close part of this interface is what
// lets a test prove the ordering.
type Source interface {
	Reader
	Conn() engine.Conn
	Close() error
}

// pooled joins a connection pool to the engine that reads through it, which is
// the only assembly a real build needs.
type pooled struct {
	Reader
	pool *connect.Pool
}

func (p pooled) Conn() engine.Conn { return p.pool.Conn() }

func (p pooled) Close() error { return p.pool.Close() }

// Open adapts an opened pool into a build Source. It performs no I/O: the pool
// decides when to dial, and Build's first health probe is what proves the
// server is there.
func Open(pool *connect.Pool) (Source, error) {
	reader, err := pool.Engine()
	if err != nil {
		return nil, err
	}
	return pooled{Reader: reader, pool: pool}, nil
}

// Logger reports what a build is doing and what it skipped. It is the shape
// both sample and describe already ask for, so one logger serves every stage.
type Logger interface {
	Info(message string)
	Warn(message string)
}

func info(logger Logger, message string) {
	if logger != nil {
		logger.Info(message)
	}
}

func warn(logger Logger, message string) {
	if logger != nil {
		logger.Warn(message)
	}
}

// plural renders a count with its noun, for the progress lines a build writes
// while it runs.
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

// assert refuses to start a stage unless the server says it has room. It is
// asked before every source the build reads, never once at startup, because
// headroom is exactly the thing that changes during a run. An unreadable
// reading is unknown, never healthy.
func assert(ctx context.Context, src Source, stage string) error {
	health, err := src.Health(ctx, src.Conn())
	if err != nil {
		health = engine.Classify(nil)
	}
	return engine.Assert(health, stage)
}
