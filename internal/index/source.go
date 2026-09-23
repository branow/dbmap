package index

import (
	"context"
	"strconv"

	"github.com/branow/dbmap/internal/catalog"
	"github.com/branow/dbmap/internal/connect"
	"github.com/branow/dbmap/internal/engine"
	"github.com/branow/dbmap/internal/redact"
)

// Reader is the half of engine.Engine a build reads through, restated here so a
// build runs against a fake and cannot reach a method it has no business with.
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
// Close is not cleanup. The build calls it after the last query and BEFORE the
// first model call, because a describe run is minutes of third-party latency
// and must never hold a pooled connection open across it. Close belongs on this
// interface so a test can prove that ordering.
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

// Open adapts an opened pool into a build Source, performing no I/O: the pool
// decides when to dial, and Build's first health probe proves the server is up.
func Open(pool *connect.Pool) (Source, error) {
	reader, err := pool.Engine()
	if err != nil {
		return nil, err
	}
	return pooled{Reader: reader, pool: pool}, nil
}

// Logger reports what a build is doing and what it skipped. It is the shape
// sample and describe already ask for, so one logger serves every stage.
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

// plural renders a count with its noun, for progress lines.
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

// assert refuses to start a stage unless the server says it has room. Asked
// before every read rather than once at startup, because headroom is what
// changes during a run. An unreadable reading is unknown, never healthy.
func assert(ctx context.Context, src Source, stage string) error {
	health, err := src.Health(ctx, src.Conn())
	if err != nil {
		health = engine.Classify(nil)
	}
	return engine.Assert(health, stage)
}
