// Package connect turns a stored connection record plus a secret resolved at
// run time into a pooled handle an engine can read through.
//
// Two rules govern everything here.
//
// The data source name is assembled in memory and goes nowhere else. It carries
// the password, so it is never logged, never returned in an error and never
// written to disk — errors name the CONNECTION, which is what a user can act
// on anyway.
//
// A connection marked production refuses to resolve, before a DSN is built and
// before a driver is asked for anything. This tool reads catalogs and samples
// rows; neither belongs against production, and the refusal lives here so that
// no future caller reaches one by taking a different route.
package connect

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/branow/dbmap/internal/config"
	"github.com/branow/dbmap/internal/engine"
	"github.com/branow/dbmap/internal/engine/postgres"
	"github.com/branow/dbmap/internal/engine/sqlserver"

	// The drivers register themselves. go-mssqldb's krb5 authenticator is a
	// separate package because it pulls in a Kerberos stack, and without this
	// import authenticator=krb5 is an unknown provider at login time.
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/microsoft/go-mssqldb"
	_ "github.com/microsoft/go-mssqldb/integratedauth/krb5"
)

// Pool limits. They are deliberately small. The pipeline reads one stage at a
// time, so a wide pool buys nothing, and the instance this was measured against
// is fragile enough that the number of connections a metadata tool can hold is
// itself worth capping.
const (
	MaxOpen         = 4
	MaxIdle         = 2
	MaxLifetime     = 30 * time.Minute
	MaxIdleLifetime = 5 * time.Minute
)

// dialect is one engine's plumbing: what registers it with database/sql, how its
// data source name is built, and which Engine reads through it. It is a table
// so that an engine is a row, and so the three facts about it cannot be
// declared in three separate places and drift.
type dialect struct {
	Name   string
	DSN    func(config.Connection, string, environment) (dsn, error)
	Engine func() engine.Engine
	// Prepare arranges whatever process-global state this engine's driver needs
	// before a connection of this shape can be opened. It runs at pool
	// construction rather than at import, so nothing is seized from a program
	// that merely links this package. A nil Prepare means there is none.
	Prepare func(config.Connection, environment) error
}

var drivers = map[config.Engine]dialect{
	config.SQLServer: {
		Name:   "sqlserver",
		DSN:    sqlserverDSN,
		Engine: func() engine.Engine { return sqlserver.New() },
	},
	config.Postgres: {
		Name:    "pgx",
		DSN:     postgresDSN,
		Engine:  func() engine.Engine { return postgres.New() },
		Prepare: preparePostgres,
	},
}

// preparePostgres registers this package's GSSAPI provider when the connection
// authenticates with Kerberos. pgx ships the hook and no implementation, so
// without this a Postgres server that demands GSSAPI answers with a message the
// driver cannot read.
func preparePostgres(cfg config.Connection, env environment) error {
	if cfg.Auth != config.Kerberos {
		return nil
	}
	cache, err := credentialCache(cfg, env)
	if err != nil {
		return err
	}
	useGSS(cache)
	return nil
}

// Engine returns the reader for one engine name, so a caller never has to
// import a driver package to get one.
func Engine(name config.Engine) (engine.Engine, error) {
	spec, ok := drivers[name]
	if !ok {
		return nil, &UnsupportedError{
			Configuration: "engine " + string(name),
			Reason:        "this build ships sqlserver and postgres",
		}
	}
	return spec.Engine(), nil
}

// Pool is a live pooled handle to one configured database.
type Pool struct {
	db     *sql.DB
	name   string
	engine config.Engine
	secret string
	// host is carried so a failure can be diagnosed in terms of the server it
	// was aimed at: a Kerberos remedy has to name the host in its SPN.
	host string
}

// Open resolves a connection record into a pool. It performs no network I/O:
// a pool is opened lazily by database/sql, so what this refuses, it refuses
// before anything is dialled. Call Verify to prove the connection actually
// works.
func Open(name string, cfg config.Connection, secret string) (*Pool, error) {
	if cfg.Production {
		return nil, &ProductionError{Name: name}
	}

	spec, ok := drivers[cfg.Engine]
	if !ok {
		return nil, &ConnectError{Name: name, Engine: cfg.Engine, Err: &UnsupportedError{
			Configuration: "engine " + string(cfg.Engine),
			Reason:        "this build ships sqlserver and postgres",
		}}
	}

	env := ambient()
	if spec.Prepare != nil {
		if err := spec.Prepare(cfg, env); err != nil {
			return nil, &ConnectError{Name: name, Engine: cfg.Engine, Err: err}
		}
	}

	source, err := spec.DSN(cfg, secret, env)
	if err != nil {
		return nil, &ConnectError{Name: name, Engine: cfg.Engine, Err: err}
	}

	db, err := sql.Open(spec.Name, source.String())
	if err != nil {
		return nil, &ConnectError{Name: name, Engine: cfg.Engine, Err: scrub(err, secret)}
	}

	db.SetMaxOpenConns(MaxOpen)
	db.SetMaxIdleConns(MaxIdle)
	db.SetConnMaxLifetime(MaxLifetime)
	db.SetConnMaxIdleTime(MaxIdleLifetime)

	return &Pool{db: db, name: name, engine: cfg.Engine, secret: secret, host: cfg.Host}, nil
}

// Name is the connection this pool was opened for.
func (p *Pool) Name() string { return p.name }

// Conn is the handle an engine reads through.
func (p *Pool) Conn() engine.Conn { return conn{db: p.db} }

// Engine is the reader for this pool's engine.
func (p *Pool) Engine() (engine.Engine, error) { return Engine(p.engine) }

// Verify proves the connection works, which is the cheapest real call there is:
// no catalog is read and no row is returned. A failure is diagnosed against the
// signature table first, so a cross-realm Kerberos setup is named as one rather
// than reported as a generic authentication failure.
func (p *Pool) Verify(ctx context.Context) error {
	if err := p.db.PingContext(ctx); err != nil {
		return &ConnectError{
			Name:   p.name,
			Engine: p.engine,
			Err:    diagnose(scrub(err, p.secret), p.host),
		}
	}
	return nil
}

// Close releases the pool.
func (p *Pool) Close() error { return p.db.Close() }

// scrub removes a secret from an error's text. A driver builds its messages
// from the connection string it was handed, so the password can arrive back
// inside a parse failure; this is the last place it can be taken out again.
func scrub(err error, secret string) error {
	if err == nil || secret == "" {
		return err
	}
	text := err.Error()
	if !strings.Contains(text, secret) {
		return err
	}
	return &scrubbed{text: strings.ReplaceAll(text, secret, "<redacted>"), err: err}
}

// scrubbed is an error whose text has had a secret taken out of it. The
// original is kept for errors.Is and errors.As, and is never rendered.
type scrubbed struct {
	text string
	err  error
}

func (e *scrubbed) Error() string { return e.text }
func (e *scrubbed) Unwrap() error { return e.err }
