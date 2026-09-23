// Package connect turns a stored connection record plus a secret resolved at
// run time into a pooled handle an engine can read through. The data source
// name carries the password, so it is assembled in memory and goes nowhere
// else: errors name the connection, never the string that opened it.
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

	// go-mssqldb's krb5 authenticator is a separate import, without which
	// authenticator=krb5 is an unknown provider.
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/microsoft/go-mssqldb"
	_ "github.com/microsoft/go-mssqldb/integratedauth/krb5"
)

// Pool limits, deliberately small: the pipeline reads one stage at a time.
const (
	MaxOpen         = 4
	MaxIdle         = 2
	MaxLifetime     = 30 * time.Minute
	MaxIdleLifetime = 5 * time.Minute
)

// dialect is one engine's plumbing: what registers it with database/sql, how
// its data source name is built, and which Engine reads through it.
type dialect struct {
	Name   string
	DSN    func(config.Connection, string, environment) (dsn, error)
	Engine func() engine.Engine
	// Prepare arranges process-global driver state, at pool construction rather
	// than at import so nothing is seized from a program that merely links this
	// package.
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

// preparePostgres registers this package's GSSAPI provider, which pgx hooks but
// does not implement.
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

// Engine returns the reader for one engine name.
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
	// host is carried because a Kerberos remedy names it in the SPN.
	host string
}

// Open resolves a connection record into a pool. It performs no network I/O;
// call Verify to prove the connection works.
func Open(name string, cfg config.Connection, secret string) (*Pool, error) {

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
func (p *Pool) Conn() engine.Conn { return conn{db: p.db, host: p.host} }

// Engine is the reader for this pool's engine.
func (p *Pool) Engine() (engine.Engine, error) { return Engine(p.engine) }

// Verify proves the connection works without reading a catalog or a row,
// diagnosing the failure first.
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
// from the connection string it was handed, so a password can arrive back
// inside a parse failure; this is the last place to take it out.
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

// scrubbed keeps the original for errors.Is and errors.As, and never renders
// it.
type scrubbed struct {
	text string
	err  error
}

func (e *scrubbed) Error() string { return e.text }
func (e *scrubbed) Unwrap() error { return e.err }
