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

// ccachePrefix matches a MIT Kerberos cache type prefix. Two or more letters,
// so a Windows drive letter is not mistaken for one.
var ccachePrefix = regexp.MustCompile(`^([A-Za-z]{2,}):(.*)$`)

// environment is the ambient state credential-cache resolution reads. It is a
// struct rather than direct calls so a test can resolve a cache with no
// environment variable set and no file on disk.
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
// or explains why there is not one.
//
// The order is: what the connection says, then what the environment says, then
// the MIT default. A cache of any type but FILE: is refused rather than
// attempted, because gokrb5 reads no other kind and the driver's own failure
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
// otherwise. It is also what the remedy message tells the user to write to, so
// the two cannot drift apart.
func defaultCache(env environment) string {
	return filepath.Join(os.TempDir(), "krb5cc_"+env.uid)
}

// readable refuses a cache that is not there or cannot be opened. An absent
// ticket is the common case and deserves the same clear remedy as a wrong cache
// type, rather than a login failure two layers down.
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

// crossRealm reports whether a connection's configured realm is a realm other
// than the one its host belongs to.
//
// Measured behaviour: gokrb5 cannot OBTAIN a cross-realm service ticket — it
// asks its own realm's KDC and is told the principal is unknown — but it will
// happily USE one already sitting in the cache. So a cross-realm setup is not
// refused; it is diagnosed here with the two commands that prime the cache,
// because the driver's own message names neither realms nor tickets.
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
	return &CrossRealmError{Host: cfg.Host, Realm: strings.ToUpper(cfg.Params[RealmParam])}
}

// signatures recognise a failure whose cause is not what the driver's message
// says it is. They are data because each row is one diagnosis, and because the
// whole point of the table is that it can be read as a list of things known to
// go wrong.
var signatures = []struct {
	Name  string
	Match *regexp.Regexp
	Error func(host string) error
}{
	{
		// A listener in another realm answers a referral the driver cannot
		// follow, and the server reports only that it could not build a
		// security context.
		Name: "cross-realm",
		Match: regexp.MustCompile(
			`(?i)cannot generate sspi context|KDC_ERR_WRONG_REALM|wrong realm|` +
				`KDC_ERR_S_PRINCIPAL_UNKNOWN|server not found in kerberos database`),
		Error: func(host string) error { return &CrossRealmError{Host: host} },
	},
	{
		// This build supplies pgx's GSSAPI provider itself, so a server asking
		// for GSSAPI is now served. Reaching this message means the connection
		// was opened without its Kerberos preparation — which Open does — and
		// naming that is more useful than repeating the driver's pointer at a
		// third-party package.
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
		// Anything else the server may ask for — SSPI, SCM credentials — is an
		// authentication method with no implementation behind it at all.
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
// recognised. An unrecognised failure is returned untouched: a guess dressed as
// a diagnosis is worse than the driver's own words.
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
