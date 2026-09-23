package connect

import (
	"net"
	"net/url"
	"strconv"

	"github.com/branow/dbmap/internal/config"
)

// Application is the name this tool reports to a server, so a DBA reading a
// session list can see what is reading their catalog.
const Application = "dbmap"

// Timeouts, in seconds. Login is generous because a Kerberos exchange against a
// distant KDC is not fast.
const (
	LoginTimeout = 30
	DialTimeout  = 15
)

// dsn is one engine's data source name, built in memory and never written
// anywhere. A distinct type, so a value carrying a password cannot be passed
// where an ordinary string is expected.
type dsn string

// String is the DSN as a driver wants it. It is deliberately the only way to
// read the value, so every use of it is visible at a call site.
func (d dsn) String() string { return string(d) }

// sqlserverDSN builds a SQL Server data source name. ApplicationIntent,
// MultiSubnetFailover and authenticator are applied after the user's params so
// no params entry can turn them off: the first routes a catalog read to a
// readable secondary, the second stops an availability-group listener hanging
// instead of failing over, and the third follows the auth mode.
func sqlserverDSN(cfg config.Connection, secret string, env environment) (dsn, error) {
	query := url.Values{}
	query.Set("app name", Application)
	query.Set("encrypt", "true")
	// Transport encryption is always on; verifying the chain is opt-in through
	// params, because an internal certificate authority is the norm and
	// defaulting verification on fails with an error a user cannot act on.
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

// postgresDSN builds a Postgres data source name. sslmode=require encrypts the
// transport without demanding a verifiable chain, the same trade sqlserverDSN
// makes; a deployment with a real chain sets params.sslmode to verify-full.
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
		// Checked here rather than at login, where the driver would report an
		// unreadable cache as an unfixable-looking login failure.
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
