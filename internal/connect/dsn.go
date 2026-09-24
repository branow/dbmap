package connect

import (
	"net"
	"net/url"
	"strconv"

	"github.com/branow/dbmap/internal/config"
)

// Application is the name this tool reports to a server's session list.
const Application = "dbmap"

// Timeouts, in seconds. Login is generous because a Kerberos exchange against a
// distant KDC is slow.
const (
	LoginTimeout = 30
	DialTimeout  = 15
)

// dsn is one engine's data source name, carrying the password. A distinct type,
// so it cannot be passed where an ordinary string is expected.
type dsn string

// String is the DSN as a driver wants it, and the only way to read the value.
func (d dsn) String() string { return string(d) }

// sqlserverDSN builds a SQL Server data source name. ApplicationIntent,
// MultiSubnetFailover and authenticator are applied after the user's params so
// no params entry can turn them off.
func sqlserverDSN(cfg config.Connection, secret string, env environment) (dsn, error) {
	query := url.Values{}
	query.Set("app name", Application)
	query.Set("encrypt", "true")
	// Encryption is always on, and the chain is verified unless the connection
	// says not to. Trusting any certificate encrypts the traffic to whoever
	// answered, which is not what a reader of "encrypt=true" assumes.
	query.Set("TrustServerCertificate", strconv.FormatBool(cfg.TrustCert))
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
func postgresDSN(cfg config.Connection, secret string, env environment) (dsn, error) {
	query := url.Values{}
	query.Set("application_name", Application)
	query.Set("sslmode", sslMode(cfg))
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

// sslMode is the Postgres spelling of the same choice TrustServerCertificate
// makes on SQL Server: verify-full checks the chain and the host name, require
// only encrypts. params.sslmode still overrides either.
func sslMode(cfg config.Connection) string {
	if cfg.TrustCert {
		return "require"
	}
	return "verify-full"
}

// hostPort renders the address, defaulting the port.
func hostPort(cfg config.Connection, fallback int) string {
	port := cfg.Port
	if port == 0 {
		port = fallback
	}
	return net.JoinHostPort(cfg.Host, strconv.Itoa(port))
}
