package index

import (
	"context"
	"strconv"

	"github.com/branow/dbmap/internal/catalog"
	"github.com/branow/dbmap/internal/connect"
	"github.com/branow/dbmap/internal/engine"
	"github.com/branow/dbmap/internal/redact"
)

// Reader is the half of engine.Engine a build reads through.
type Reader interface {
	Manifest(ctx context.Context, conn engine.Conn) ([]catalog.Object, error)
	Structure(ctx context.Context, conn engine.Conn) (map[string]catalog.Structure, error)
	Modules(ctx context.Context, conn engine.Conn, keys []string) (map[string]redact.Body, error)
	Sample(ctx context.Context, conn engine.Conn, table engine.Table, n int) (catalog.Sample, error)
	Health(ctx context.Context, conn engine.Conn) (engine.Health, error)
}

// Source is the live database half of a build. Close is not cleanup: the build
// calls it before the first model call, so a describe run never holds a pooled
// connection across minutes of third-party latency. A test pins that ordering.
type Source interface {
	Reader
	Conn() engine.Conn
	Close() error
}

// pooled joins a connection pool to the engine that reads through it.
type pooled struct {
	Reader
	pool *connect.Pool
}

func (p pooled) Conn() engine.Conn { return p.pool.Conn() }

func (p pooled) Close() error { return p.pool.Close() }

// Open adapts an opened pool into a build Source, performing no I/O.
func Open(pool *connect.Pool) (Source, error) {
	reader, err := pool.Engine()
	if err != nil {
		return nil, err
	}
	return pooled{Reader: reader, pool: pool}, nil
}

// Logger reports what a build is doing and what it skipped.
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

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

// assert refuses to start a stage unless the server says it has room. Asked
// before every read, because headroom changes during a run; an unreadable
// reading counts as unknown, never healthy.
func assert(ctx context.Context, src Source, stage string) error {
	health, err := src.Health(ctx, src.Conn())
	if err != nil {
		// Still a halt, but with the reason the server actually gave: an
		// unreadable reading is unknown, and unknown is never healthy.
		return err
	}
	return engine.Assert(health, stage)
}
