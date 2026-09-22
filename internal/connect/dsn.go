package connect

import (
	"net"
	"net/url"
	"strconv"

	"github.com/branow/dbmap/internal/config"
)

// Application is the name this tool reports to a server, so a DBA looking at a
// session list can see what is reading their catalog.
const Application = "dbmap"

// Timeouts, in seconds. Login is generous because a Kerberos exchange against a
// distant KDC is not fast; the query timeout is the same 120 seconds the
// reference implementation used, and the Postgres engine repeats it as a
// session-local statement_timeout because a connection-level setting is not a
// promise the server keeps.
const (
	LoginTimeout = 30
	DialTimeout  = 15
)

// dsn is one engine's data source name, built in memory and never written
// anywhere. It is a distinct type so that a value carrying a password cannot be
// passed to something expecting an ordinary string by accident.
type dsn string

// String is the DSN as a driver wants it. It is deliberately the only way to
// read the value, so every use of it is visible at a call site.
func (d dsn) String() string { return string(d) }

// sqlserverDSN builds a SQL Server data source name.
//
// Three parameters are not negotiable and are applied after the user's own, so
// a params entry cannot turn them off:
//
//	ApplicationIntent=ReadOnly   on an availability group this routes to a
//	                             readable secondary, so the catalog read lands
//	                             on the replica rather than the primary
//	MultiSubnetFailover=true     an availability-group listener otherwise hangs
//	                             rather than failing over, which is the reason
//	                             the reference implementation passed -M
//	authenticator=krb5           chosen by the auth mode, not by a parameter
func sqlserverDSN(cfg config.Connection, secret string, env environment) (dsn, error) {
	query := url.Values{}
	query.Set("app name", Application)
	query.Set("encrypt", "true")
	// The measured estate presents a certificate from an internal authority,
	// as such estates do. Transport encryption is always on; verifying the
	// chain is opt-in through params, because defaulting it on would fail every
	// such deployment with an error a user cannot act on from here.
	query.Set("TrustServerCertificate", "true")
	query.Set("connection timeout", strconv.Itoa(LoginTimeout))
	query.Set("dial timeout", strconv.Itoa(DialTimeout))
	if cfg.Database != "" {
		query.Set("database", cfg.Database)
	}

	for key, value := range cfg.Params {
		query.Set(key, value)
	}

	var user *url.Userinfo
	switch cfg.Auth {
	case config.Kerberos:
		cache, err := credentialCache(cfg, env)
		if err != nil {
			return "", err
		}
		if crossRealm(cfg) {
			return "", crossRealmError(cfg)
		}
		query.Set("authenticator", "krb5")
		query.Set(CredCacheParam, cache)
	default:
		user = url.UserPassword(cfg.Username, secret)
	}

	query.Set("ApplicationIntent", "ReadOnly")
	query.Set("MultiSubnetFailover", "true")

	return dsn((&url.URL{
		Scheme:   "sqlserver",
		User:     user,
		Host:     hostPort(cfg, 1433),
		RawQuery: query.Encode(),
	}).String()), nil
}

// postgresDSN builds a Postgres data source name.
//
// sslmode=require encrypts the transport without demanding a verifiable chain,
// which is the same trade the SQL Server side makes and for the same reason.
// A deployment with a real chain sets params.sslmode to verify-full and gets
// the stronger behaviour with no code change.
func postgresDSN(cfg config.Connection, secret string, env environment) (dsn, error) {
	query := url.Values{}
	query.Set("application_name", Application)
	query.Set("sslmode", "require")
	query.Set("connect_timeout", strconv.Itoa(LoginTimeout))

	for key, value := range cfg.Params {
		query.Set(key, value)
	}

	var user *url.Userinfo
	switch cfg.Auth {
	case config.Kerberos:
		// The ticket is checked here rather than at login for the same reason
		// as on SQL Server: an unreadable cache is a fixable mistake with a
		// one-line remedy, and the driver reports it as a login failure.
		if _, err := credentialCache(cfg, env); err != nil {
			return "", err
		}
		if crossRealm(cfg) {
			return "", crossRealmError(cfg)
		}
		if query.Get("krbsrvname") == "" {
			query.Set("krbsrvname", "postgres")
		}
		if cfg.Username != "" {
			user = url.User(cfg.Username)
		}
	default:
		user = url.UserPassword(cfg.Username, secret)
	}

	return dsn((&url.URL{
		Scheme:   "postgres",
		User:     user,
		Host:     hostPort(cfg, 5432),
		Path:     "/" + cfg.Database,
		RawQuery: query.Encode(),
	}).String()), nil
}

// hostPort renders the address, filling in the engine's own default port when
// the connection names none.
func hostPort(cfg config.Connection, fallback int) string {
	port := cfg.Port
	if port == 0 {
		port = fallback
	}
	return net.JoinHostPort(cfg.Host, strconv.Itoa(port))
}
