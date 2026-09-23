package connect

import (
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/branow/dbmap/internal/config"
)

// CredCacheParam is the connection parameter naming the credential cache.
const CredCacheParam = "krb5-credcachefile"

// RealmParam is the connection parameter naming the Kerberos realm.
const RealmParam = "krb5-realm"

// ccachePrefix matches an MIT cache type prefix. Two or more letters, so a
// Windows drive letter is not mistaken for one.
var ccachePrefix = regexp.MustCompile(`^([A-Za-z]{2,}):(.*)$`)

// environment is the ambient state cache resolution reads, as a struct so a
// test needs no env var and no file on disk.
type environment struct {
	getenv func(string) string
	stat   func(string) (os.FileInfo, error)
	uid    string
}

func ambient() environment {
	uid := ""
	if current, err := user.Current(); err == nil {
		uid = current.Uid
	}
	return environment{getenv: os.Getenv, stat: os.Stat, uid: uid}
}

// credentialCache locates the FILE: cache this connection will use, preferring
// the connection, then the environment, then the MIT default. Any other type is
// refused rather than attempted: gokrb5 reads no other kind, and the driver's
// failure for that case looks like having no ticket at all.
func credentialCache(cfg config.Connection, env environment) (string, error) {
	if path := strings.TrimSpace(cfg.Params[CredCacheParam]); path != "" {
		return readable(strings.TrimPrefix(path, "FILE:"), env)
	}

	name := strings.TrimSpace(env.getenv("KRB5CCNAME"))
	if match := ccachePrefix.FindStringSubmatch(name); match != nil {
		kind, path := strings.ToUpper(match[1]), match[2]
		if kind != "FILE" {
			return "", &CredentialCacheError{Type: kind, Path: defaultCache(env)}
		}
		return readable(path, env)
	}
	if name != "" {
		return readable(name, env)
	}
	return readable(defaultCache(env), env)
}

// defaultCache is the MIT default, and what the remedy message names.
func defaultCache(env environment) string {
	return filepath.Join(os.TempDir(), "krb5cc_"+env.uid)
}

// readable refuses a cache that is missing or unopenable, so an absent ticket
// gets the same remedy as a wrong cache type.
func readable(path string, env environment) (string, error) {
	info, err := env.stat(path)
	if err != nil {
		return "", &CredentialCacheError{Path: path, Err: err}
	}
	if info.IsDir() {
		return "", &CredentialCacheError{Type: "DIR", Path: path}
	}
	return path, nil
}

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
