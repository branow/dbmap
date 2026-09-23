package connect

import (
	"regexp"
	"strings"

	"github.com/branow/dbmap/internal/config"
)

// crossRealm reports whether a connection's configured realm differs from its
// host's.
func crossRealm(cfg config.Connection) bool {
	realm := strings.ToUpper(strings.TrimSpace(cfg.Params[RealmParam]))
	if realm == "" {
		return false
	}
	host := strings.ToUpper(strings.TrimSpace(cfg.Host))
	domain := host
	if dot := strings.Index(host, "."); dot >= 0 {
		domain = host[dot+1:]
	}
	if domain == "" {
		return false
	}
	return domain != realm && !strings.HasSuffix(domain, "."+realm)
}

func crossRealmError(cfg config.Connection) error {
	return &CrossRealmError{
		Host:  cfg.Host,
		Realm: strings.ToUpper(cfg.Params[RealmParam]),
		SPN:   SPN(cfg.Engine, cfg.Host),
	}
}

// signatures recognise a failure whose cause is not what the driver's message
// says it is.
var signatures = []struct {
	Name  string
	Match *regexp.Regexp
	Error func(host string) error
}{
	{
		// A listener in another realm answers a referral the driver cannot follow.
		Name: "cross-realm",
		Match: regexp.MustCompile(
			`(?i)cannot generate sspi context|KDC_ERR_WRONG_REALM|wrong realm|` +
				`KDC_ERR_S_PRINCIPAL_UNKNOWN|server not found in kerberos database`),
		Error: func(host string) error { return &CrossRealmError{Host: host} },
	},
	{
		Name:  "postgres-gssapi-unregistered",
		Match: regexp.MustCompile(`(?i)no gssapi provider registered`),
		Error: func(string) error {
			return &KerberosError{
				Stage:  StageCredential,
				Reason: "this connection was opened without its Kerberos preparation",
			}
		},
	},
	{
		Name:  "postgres-unknown-auth",
		Match: regexp.MustCompile(`(?i)unknown authentication (message|response)`),
		Error: func(string) error {
			return &UnsupportedError{
				Configuration: "this server's authentication method",
				Reason:        "it is neither scram nor Kerberos, the two this build speaks",
				Remedy:        "configure the server to accept scram or Kerberos for this role",
			}
		},
	},
	{
		// A server that rejected the login has decided. Left unclassified this
		// reads as "unavailable", and setup then stores a credential the server
		// has already refused.
		Name: "login-rejected",
		Match: regexp.MustCompile(`(?i)password authentication failed|` +
			`\blogin failed for user\b|SQLSTATE 28[0-9A-Z]{3}|` +
			`authentication failed|\blogin failed\b`),
		Error: func(string) error { return &RejectedError{Subject: "the login"} },
	},
	{
		// Naming a database that is not there is a typo, not an outage.
		Name: "no-such-database",
		Match: regexp.MustCompile(`(?i)cannot open database|database ".*" does not exist|` +
			`SQLSTATE 3D000`),
		Error: func(string) error { return &RejectedError{Subject: "the database"} },
	},
	{
		Name:  "no-ticket",
		Match: regexp.MustCompile(`(?i)no credentials? cache|credentials cache file .* not found`),
		Error: func(string) error {
			return &CredentialCacheError{Path: defaultCache(ambient())}
		},
	},
}

// diagnose replaces a recognised driver failure with a named one, returning an
// unrecognised one untouched.
func diagnose(err error, host string) error {
	if err == nil {
		return nil
	}
	text := err.Error()
	for _, signature := range signatures {
		if signature.Match.MatchString(text) {
			return signature.Error(host)
		}
	}
	return err
}

// SPN is the service principal a ticket for this engine is issued against. SQL
// Server registers MSSQLSvc with the port, PostgreSQL postgres with none, and a
// remedy naming the wrong one sends the reader in circles.
func SPN(engine config.Engine, host string) string {
	if engine == config.Postgres {
		return "postgres/" + host
	}
	return "MSSQLSvc/" + host + ":1433"
}
