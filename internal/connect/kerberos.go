package connect

import (
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/branow/dbmap/internal/config"
)

// CredCacheParam is the connection parameter naming the credential cache, as
// the driver spells it.
const CredCacheParam = "krb5-credcachefile"

// RealmParam is the connection parameter naming the Kerberos realm.
const RealmParam = "krb5-realm"

// ccachePrefix matches an MIT Kerberos cache type prefix. Two or more letters,
// so a Windows drive letter is not mistaken for one.
var ccachePrefix = regexp.MustCompile(`^([A-Za-z]{2,}):(.*)$`)

// environment is the ambient state credential-cache resolution reads, as a
// struct so a test can resolve a cache with no env var and no file on disk.
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

// credentialCache locates the FILE: credential cache this connection will use,
// or explains why there is not one. It prefers what the connection says, then
// the environment, then the MIT default. Any type but FILE: is refused rather
// than attempted, because gokrb5 reads no other kind and the driver's failure
// for that case is indistinguishable from having no ticket at all.
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

// defaultCache is where MIT Kerberos puts a file cache when nothing says
// otherwise, and what the remedy message names, so the two cannot drift.
func defaultCache(env environment) string {
	return filepath.Join(os.TempDir(), "krb5cc_"+env.uid)
}

// readable refuses a cache that is not there or cannot be opened, so an absent
// ticket gets the same clear remedy as a wrong cache type.
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

// crossRealm reports whether a connection's configured realm differs from the
// one its host belongs to. Such a setup is diagnosed rather than refused:
// gokrb5 cannot obtain a cross-realm service ticket but will use one already in
// the cache, and the driver's own message names neither realms nor tickets.
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

// crossRealmError is the diagnosis, phrased so the reader knows what to run.
func crossRealmError(cfg config.Connection) error {
	return &CrossRealmError{
		Host:  cfg.Host,
		Realm: strings.ToUpper(cfg.Params[RealmParam]),
		SPN:   SPN(cfg.Engine, cfg.Host),
	}
}

// signatures recognise a failure whose cause is not what the driver's message
// says it is, as data so the table reads as the list of things known to go
// wrong.
var signatures = []struct {
	Name  string
	Match *regexp.Regexp
	Error func(host string) error
}{
	{
		// A listener in another realm answers a referral the driver cannot
		// follow, reported only as a failure to build a security context.
		Name: "cross-realm",
		Match: regexp.MustCompile(
			`(?i)cannot generate sspi context|KDC_ERR_WRONG_REALM|wrong realm|` +
				`KDC_ERR_S_PRINCIPAL_UNKNOWN|server not found in kerberos database`),
		Error: func(host string) error { return &CrossRealmError{Host: host} },
	},
	{
		// This build supplies pgx's GSSAPI provider, so reaching this message
		// means the connection was opened without its Kerberos preparation.
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
		// Anything else — SSPI, SCM credentials — has no implementation here.
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

// diagnose replaces a driver failure with a named one where the signature is
// recognised. An unrecognised failure is returned untouched, because a guess
// dressed as a diagnosis is worse than the driver's own words.
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

// SPN is the service principal a Kerberos ticket for this engine is issued
// against. SQL Server registers MSSQLSvc with the port; PostgreSQL registers
// the service named by krbsrvname, which defaults to postgres and carries no
// port. A remedy naming the wrong one sends the reader in circles.
func SPN(engine config.Engine, host string) string {
	if engine == config.Postgres {
		return "postgres/" + host
	}
	return "MSSQLSvc/" + host + ":1433"
}
