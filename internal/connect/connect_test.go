package connect

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/branow/dbmap/internal/config"
)

// secret is the stand-in every leak assertion below looks for. It is assembled
// rather than written down: a test fixture that reads like a credential is a
// credential as far as every scanner and every reader is concerned, and this
// repo keeps neither in a file.
var secret = strings.Repeat("x", 16) + "-placeholder"

// fakeFile is a stat result for a credential cache that exists.
type fakeFile struct{ dir bool }

func (fakeFile) Name() string       { return "krb5cc" }
func (fakeFile) Size() int64        { return 1024 }
func (fakeFile) Mode() fs.FileMode  { return 0o600 }
func (fakeFile) ModTime() time.Time { return time.Time{} }
func (f fakeFile) IsDir() bool      { return f.dir }
func (fakeFile) Sys() any           { return nil }

// world builds an environment with no real filesystem and no real process
// environment behind it, so a ticket can be present or absent on demand.
func world(vars map[string]string, files map[string]bool) environment {
	return environment{
		uid: "501",
		getenv: func(name string) string {
			return vars[name]
		},
		stat: func(path string) (os.FileInfo, error) {
			dir, ok := files[path]
			if !ok {
				return nil, fs.ErrNotExist
			}
			return fakeFile{dir: dir}, nil
		},
	}
}

func sqlLogin() config.Connection {
	return config.Connection{
		Engine:   config.SQLServer,
		Host:     "db.example.internal",
		Database: "AppCore",
		Auth:     config.SQLLogin,
		Username: "reader",
	}
}

// The tool reads catalogs and samples rows. Neither belongs against production,
// so the refusal is in the resolver rather than in a command: a future caller
// taking another route still cannot reach one.
func TestAProductionConnectionRefusesToResolve(t *testing.T) {
	cfg := sqlLogin()
	cfg.Production = true

	pool, err := Open("prod", cfg, secret)

	if pool != nil {
		t.Error("a production connection produced a pool")
	}
	var refused *ProductionError
	if !errors.As(err, &refused) {
		t.Fatalf("error is %T, want *ProductionError", err)
	}
	if refused.Name != "prod" {
		t.Errorf("the error names %q, want the connection name", refused.Name)
	}
}

// The refusal must not depend on the engine, the auth mode, or anything else
// being valid: production is checked before any of it is looked at.
func TestProductionIsRefusedBeforeAnythingElseIsLookedAt(t *testing.T) {
	cfg := config.Connection{Engine: "nonsense", Production: true}

	_, err := Open("prod", cfg, "")

	var refused *ProductionError
	if !errors.As(err, &refused) {
		t.Fatalf("error is %T, want *ProductionError", err)
	}
}

// A DSN holds the password. An error is the most widely copied string a program
// produces, so the two must never meet.
func TestNoErrorEverCarriesTheSecret(t *testing.T) {
	cases := []struct {
		name string
		cfg  config.Connection
	}{
		{"an unsupported engine", config.Connection{Engine: "oracle", Host: "h"}},
		{"a kerberos connection with no ticket", func() config.Connection {
			cfg := sqlLogin()
			cfg.Auth = config.Kerberos
			return cfg
		}()},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Open("db", c.cfg, secret)
			if err == nil {
				t.Skip("this case opened successfully, nothing to assert")
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("the secret reached an error: %v", err)
			}
		})
	}
}

// scrub is the last place a password can be taken out of a driver's own
// message, which is built from the connection string it was handed.
func TestScrubTakesTheSecretOutOfADriverMessage(t *testing.T) {
	original := errors.New(`parse "sqlserver://reader:` + secret + `@h": invalid port`)

	cleaned := scrub(original, secret)

	if strings.Contains(cleaned.Error(), secret) {
		t.Fatalf("the secret survived scrubbing: %v", cleaned)
	}
	if !strings.Contains(cleaned.Error(), "<redacted>") {
		t.Errorf("the scrubbed message says nothing about what was removed: %v", cleaned)
	}
	if !errors.Is(cleaned, original) {
		t.Error("scrubbing lost the original error")
	}
	if scrub(nil, secret) != nil {
		t.Error("scrub invented an error")
	}
}

// The three SQL Server parameters that are safety properties rather than
// preferences: a read-only intent that routes to a replica, failover that does
// not hang on an availability-group listener, and encryption.
func TestSqlserverDSNCarriesTheReadOnlyIntent(t *testing.T) {
	source, err := sqlserverDSN(sqlLogin(), secret, world(nil, nil))
	if err != nil {
		t.Fatalf("sqlserverDSN: %v", err)
	}

	for _, want := range []string{
		"ApplicationIntent=ReadOnly",
		"MultiSubnetFailover=true",
		"encrypt=true",
		"database=AppCore",
	} {
		if !strings.Contains(source.String(), want) {
			t.Errorf("DSN is missing %s", want)
		}
	}
	if !strings.HasPrefix(source.String(), "sqlserver://") {
		t.Errorf("DSN is not a SQL Server DSN")
	}
}

// A params entry is a user's escape hatch for everything EXCEPT the parameters
// that are there for safety.
func TestParamsCannotTurnOffTheReadOnlyIntent(t *testing.T) {
	cfg := sqlLogin()
	cfg.Params = map[string]string{
		"ApplicationIntent":   "ReadWrite",
		"MultiSubnetFailover": "false",
		"packet size":         "32767",
	}

	source, err := sqlserverDSN(cfg, secret, world(nil, nil))
	if err != nil {
		t.Fatalf("sqlserverDSN: %v", err)
	}

	if !strings.Contains(source.String(), "ApplicationIntent=ReadOnly") {
		t.Error("a params entry turned off the read-only intent")
	}
	if strings.Contains(source.String(), "ApplicationIntent=ReadWrite") {
		t.Error("the read-write intent survived")
	}
	if !strings.Contains(source.String(), "MultiSubnetFailover=true") {
		t.Error("a params entry turned off multi-subnet failover")
	}
	if !strings.Contains(source.String(), "packet+size=32767") {
		t.Error("an ordinary params entry was dropped")
	}
}

func TestSqlserverKerberosUsesTheFileCredentialCache(t *testing.T) {
	cfg := sqlLogin()
	cfg.Auth = config.Kerberos
	env := world(
		map[string]string{"KRB5CCNAME": "FILE:/tmp/krb5cc_501"},
		map[string]bool{"/tmp/krb5cc_501": false},
	)

	source, err := sqlserverDSN(cfg, "", env)
	if err != nil {
		t.Fatalf("sqlserverDSN: %v", err)
	}

	if !strings.Contains(source.String(), "authenticator=krb5") {
		t.Error("the Kerberos authenticator was not selected")
	}
	if !strings.Contains(source.String(), "krb5-credcachefile=%2Ftmp%2Fkrb5cc_501") {
		t.Errorf("the credential cache was not passed: %s", source)
	}
	// Kerberos takes the identity from the ticket, so a login in the DSN would
	// be both wrong and a leak.
	if strings.Contains(source.String(), "reader:") {
		t.Error("a Kerberos DSN carried a login")
	}
}

func TestPostgresDSNIsBuiltFromTheRecord(t *testing.T) {
	cfg := config.Connection{
		Engine:   config.Postgres,
		Host:     "db.example.internal",
		Port:     6543,
		Database: "appcore",
		Auth:     config.SCRAM,
		Username: "reader",
	}

	source, err := postgresDSN(cfg, secret, world(nil, nil))
	if err != nil {
		t.Fatalf("postgresDSN: %v", err)
	}

	for _, want := range []string{"postgres://", "db.example.internal:6543", "/appcore",
		"sslmode=require", "application_name=dbmap"} {
		if !strings.Contains(source.String(), want) {
			t.Errorf("DSN is missing %s: %s", want, source)
		}
	}
}

func TestDefaultPortsAreFilledIn(t *testing.T) {
	sqlserver, err := sqlserverDSN(sqlLogin(), secret, world(nil, nil))
	if err != nil {
		t.Fatalf("sqlserverDSN: %v", err)
	}
	if !strings.Contains(sqlserver.String(), ":1433") {
		t.Errorf("SQL Server default port missing: %s", sqlserver)
	}

	cfg := config.Connection{Engine: config.Postgres, Host: "h", Auth: config.SCRAM}
	pg, err := postgresDSN(cfg, secret, world(nil, nil))
	if err != nil {
		t.Fatalf("postgresDSN: %v", err)
	}
	if !strings.Contains(pg.String(), ":5432") {
		t.Errorf("Postgres default port missing: %s", pg)
	}
}

// macOS defaults to an API: credential cache, which is keychain-backed and
// which gokrb5 cannot read. It is the failure everyone hits first, and the
// driver reports it as "no credentials", so the remedy has to be spelled out
// here or it is not spelled out anywhere.
func TestAnApiCredentialCacheNamesTheKinitRemedy(t *testing.T) {
	cfg := sqlLogin()
	cfg.Auth = config.Kerberos
	env := world(map[string]string{"KRB5CCNAME": "API:orest@EXAMPLE.LOCAL"}, nil)

	_, err := sqlserverDSN(cfg, "", env)

	var cache *CredentialCacheError
	if !errors.As(err, &cache) {
		t.Fatalf("error is %T, want *CredentialCacheError", err)
	}
	if cache.Type != "API" {
		t.Errorf("the error records type %q, want API", cache.Type)
	}
	if !strings.Contains(err.Error(), "kinit -c FILE:") {
		t.Fatalf("the remedy does not name kinit -c FILE:: %v", err)
	}
	if !strings.Contains(cache.Remedy(), "kinit -c FILE:") {
		t.Errorf("Remedy() = %q", cache.Remedy())
	}
}

// An absent ticket gets the same remedy as a wrong cache type, because it has
// the same fix.
func TestAMissingCredentialCacheNamesTheKinitRemedy(t *testing.T) {
	cfg := sqlLogin()
	cfg.Auth = config.Kerberos

	_, err := sqlserverDSN(cfg, "", world(nil, nil))

	var cache *CredentialCacheError
	if !errors.As(err, &cache) {
		t.Fatalf("error is %T, want *CredentialCacheError", err)
	}
	if !strings.Contains(err.Error(), "kinit -c FILE:") {
		t.Fatalf("the remedy does not name kinit -c FILE:: %v", err)
	}
}

// Postgres Kerberos checks the same ticket in the same way, so the remedy is
// the same sentence rather than a second one that drifts.
func TestPostgresKerberosAlsoNamesTheKinitRemedy(t *testing.T) {
	cfg := config.Connection{
		Engine: config.Postgres, Host: "db.example.internal",
		Database: "appcore", Auth: config.Kerberos,
	}

	_, err := postgresDSN(cfg, "", world(map[string]string{"KRB5CCNAME": "KCM:501"}, nil))

	var cache *CredentialCacheError
	if !errors.As(err, &cache) {
		t.Fatalf("error is %T, want *CredentialCacheError", err)
	}
	if !strings.Contains(err.Error(), "kinit -c FILE:") {
		t.Fatalf("the remedy does not name kinit -c FILE:: %v", err)
	}
}

func TestCredentialCacheResolution(t *testing.T) {
	present := map[string]bool{"/tmp/krb5cc_501": false, "/var/tickets/db": false}

	cases := []struct {
		name  string
		cfg   config.Connection
		vars  map[string]string
		want  string
		fails bool
	}{
		{
			name: "the connection names one",
			cfg:  config.Connection{Params: map[string]string{CredCacheParam: "/var/tickets/db"}},
			want: "/var/tickets/db",
		},
		{
			name: "the connection names one with a FILE prefix",
			cfg: config.Connection{
				Params: map[string]string{CredCacheParam: "FILE:/var/tickets/db"},
			},
			want: "/var/tickets/db",
		},
		{
			name: "the environment names a FILE cache",
			vars: map[string]string{"KRB5CCNAME": "FILE:/tmp/krb5cc_501"},
			want: "/tmp/krb5cc_501",
		},
		{
			name: "the environment names a bare path",
			vars: map[string]string{"KRB5CCNAME": "/tmp/krb5cc_501"},
			want: "/tmp/krb5cc_501",
		},
		{
			name:  "the environment names a keyring cache",
			vars:  map[string]string{"KRB5CCNAME": "KEYRING:persistent:501"},
			fails: true,
		},
		{
			name:  "the connection names a cache that is not there",
			cfg:   config.Connection{Params: map[string]string{CredCacheParam: "/nowhere"}},
			fails: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := credentialCache(c.cfg, world(c.vars, present))
			if c.fails {
				var cache *CredentialCacheError
				if !errors.As(err, &cache) {
					t.Fatalf("error is %T, want *CredentialCacheError", err)
				}
				if !strings.Contains(err.Error(), "kinit -c FILE:") {
					t.Errorf("the remedy does not name kinit: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("credentialCache: %v", err)
			}
			if got != c.want {
				t.Errorf("resolved %q, want %q", got, c.want)
			}
		})
	}
}

// gokrb5 follows no realm referral, so a cross-realm setup must be named rather
// than attempted and misreported as an ordinary authentication failure.
func TestCrossRealmIsNamedRatherThanAttempted(t *testing.T) {
	cfg := sqlLogin()
	cfg.Auth = config.Kerberos
	cfg.Params = map[string]string{RealmParam: "OTHER.LOCAL"}
	env := world(
		map[string]string{"KRB5CCNAME": "FILE:/tmp/krb5cc_501"},
		map[string]bool{"/tmp/krb5cc_501": false},
	)

	_, err := sqlserverDSN(cfg, "", env)

	var unsupported *UnsupportedError
	if !errors.As(err, &unsupported) {
		t.Fatalf("error is %T, want *UnsupportedError", err)
	}
	if !strings.Contains(strings.ToLower(unsupported.Configuration), "cross-realm") {
		t.Errorf("the error does not name cross-realm: %q", unsupported.Configuration)
	}
}

func TestARealmMatchingTheHostIsNotCrossRealm(t *testing.T) {
	cases := []struct {
		host  string
		realm string
		cross bool
	}{
		{"db.example.internal", "EXAMPLE.INTERNAL", false},
		{"db.sub.example.internal", "EXAMPLE.INTERNAL", false},
		{"db.example.internal", "OTHER.LOCAL", true},
		{"db.example.internal", "", false},
		{"db", "EXAMPLE.INTERNAL", true},
	}
	for _, c := range cases {
		cfg := config.Connection{Host: c.host}
		if c.realm != "" {
			cfg.Params = map[string]string{RealmParam: c.realm}
		}
		if got := crossRealm(cfg); got != c.cross {
			t.Errorf("crossRealm(%q, %q) = %v, want %v", c.host, c.realm, got, c.cross)
		}
	}
}

// "Cannot generate SSPI context" says nothing about realms, which is exactly
// why the diagnosis cannot be left to the reader.
func TestDiagnoseNamesWhatTheDriverMessageDoesNot(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "a cross-realm listener",
			err:  errors.New("mssql: login error: Cannot generate SSPI context"),
			want: "cross-realm",
		},
		{
			// GSSAPI itself is served now — this package supplies pgx's
			// provider — so an unknown method is one that is neither scram nor
			// Kerberos, and saying so is the useful diagnosis.
			name: "a postgres server demanding a method this build does not speak",
			err:  errors.New("unknown authentication message: 9"),
			want: "authentication method",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var unsupported *UnsupportedError
			if !errors.As(diagnose(c.err), &unsupported) {
				t.Fatalf("diagnose left %v unnamed", c.err)
			}
			if !strings.Contains(unsupported.Configuration, c.want) {
				t.Errorf("configuration = %q, want it to name %q",
					unsupported.Configuration, c.want)
			}
		})
	}
}

// pgx's own message for a missing provider points at a third-party package this
// build deliberately does not use, so it is replaced with what actually went
// wrong: the connection was opened without its Kerberos preparation.
func TestAnUnregisteredProviderIsNamedAsOurOwnMistake(t *testing.T) {
	err := diagnose(errors.New(
		"kerberos error: no GSSAPI provider registered, see https://example.invalid"))

	var kerberos *KerberosError
	if !errors.As(err, &kerberos) {
		t.Fatalf("error is %T, want *KerberosError", err)
	}
	if strings.Contains(err.Error(), "https://") {
		t.Errorf("the driver's pointer at a third-party package survived: %v", err)
	}
}

// An unrecognised failure is returned untouched: a guess dressed as a diagnosis
// is worse than the driver's own words.
func TestDiagnoseLeavesAnUnknownFailureAlone(t *testing.T) {
	original := errors.New("dial tcp: i/o timeout")

	if got := diagnose(original); got != original {
		t.Errorf("diagnose rewrote an unrecognised failure: %v", got)
	}
	if diagnose(nil) != nil {
		t.Error("diagnose invented an error")
	}
}

// A ticket that has expired reads as no cache at all, and the fix is the same
// kinit.
func TestAMissingTicketIsDiagnosedWithTheKinitRemedy(t *testing.T) {
	err := diagnose(errors.New("krb5: no credentials cache found"))

	var cache *CredentialCacheError
	if !errors.As(err, &cache) {
		t.Fatalf("error is %T, want *CredentialCacheError", err)
	}
	if !strings.Contains(err.Error(), "kinit -c FILE:") {
		t.Errorf("the remedy does not name kinit: %v", err)
	}
}

// Every engine config validates is an engine this package can open, or the
// config layer would accept a connection nothing can resolve.
func TestEveryConfiguredEngineHasADriver(t *testing.T) {
	for _, name := range config.Engines() {
		t.Run(name, func(t *testing.T) {
			reader, err := Engine(config.Engine(name))
			if err != nil {
				t.Fatalf("Engine(%q): %v", name, err)
			}
			if reader.Name() != name {
				t.Errorf("engine %q calls itself %q", name, reader.Name())
			}
		})
	}
	if _, err := Engine("oracle"); err == nil {
		t.Error("an unsupported engine resolved")
	}
}

// Every auth mode config accepts must build a DSN, or a connection the user can
// define is a connection that cannot be opened.
func TestEveryConfiguredAuthModeBuildsADSN(t *testing.T) {
	ticket := world(
		map[string]string{"KRB5CCNAME": "FILE:/tmp/krb5cc_501"},
		map[string]bool{"/tmp/krb5cc_501": false},
	)

	for _, name := range config.Engines() {
		engineName := config.Engine(name)
		for _, mode := range config.Auths(engineName) {
			t.Run(name+"/"+mode, func(t *testing.T) {
				cfg := config.Connection{
					Engine:   engineName,
					Host:     "db.example.internal",
					Database: "appcore",
					Auth:     config.Auth(mode),
					Username: "reader",
				}
				source, err := drivers[engineName].DSN(cfg, secret, ticket)
				if err != nil {
					t.Fatalf("DSN: %v", err)
				}
				if source == "" {
					t.Fatal("DSN is empty")
				}
			})
		}
	}
}

// The pool is small on purpose: the pipeline reads one stage at a time, and the
// instance this was measured against is fragile enough that the number of
// connections a metadata tool holds is itself worth capping.
func TestPoolLimitsAreModest(t *testing.T) {
	if MaxOpen > 8 {
		t.Errorf("MaxOpen = %d, too many for a tool that reads one stage at a time", MaxOpen)
	}
	if MaxIdle > MaxOpen {
		t.Errorf("MaxIdle = %d exceeds MaxOpen = %d", MaxIdle, MaxOpen)
	}
	if MaxLifetime <= 0 || MaxIdleLifetime <= 0 {
		t.Error("a pooled connection must not live forever")
	}
}

// Open performs no network I/O, so this opens a real pool against a host that
// does not exist and asserts only what was assembled.
func TestOpenBuildsAPoolWithoutDiallingAnything(t *testing.T) {
	pool, err := Open("stage", sqlLogin(), secret)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()

	if pool.Name() != "stage" {
		t.Errorf("Name = %q", pool.Name())
	}
	if pool.Conn() == nil {
		t.Error("the pool produced no connection handle")
	}
	reader, err := pool.Engine()
	if err != nil {
		t.Fatalf("Engine: %v", err)
	}
	if reader.Name() != "sqlserver" {
		t.Errorf("the pool reads through %q", reader.Name())
	}
}
