package config

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestDefiningNeverActivates(t *testing.T) {
	c := New()
	if err := c.SetConnection("primary", Connection{Engine: SQLServer,
		Host: "example.internal", Auth: SQLLogin}); err != nil {
		t.Fatal(err)
	}
	if err := c.SetBackend("main", Backend{Provider: Anthropic}); err != nil {
		t.Fatal(err)
	}
	if err := c.SetProfile("work", Profile{Connection: "primary", Backend: "main"}); err != nil {
		t.Fatal(err)
	}
	if c.CurrentProfile != "" {
		t.Fatalf("current profile = %q after defining entries, want none", c.CurrentProfile)
	}
	if err := c.Switch("work"); err != nil {
		t.Fatal(err)
	}
	if c.CurrentProfile != "work" {
		t.Errorf("current profile = %q after switch, want work", c.CurrentProfile)
	}
}

func TestSwitchRefusesAnUndefinedProfile(t *testing.T) {
	c := New()
	var missing *NotFoundError
	if err := c.Switch("nope"); !errors.As(err, &missing) || missing.Kind != KindProfile {
		t.Fatalf("error = %v, want a profile NotFoundError", err)
	}
}

func TestProfileMustBindDefinedEntries(t *testing.T) {
	c := New()
	if err := c.SetConnection("primary", Connection{Engine: Postgres,
		Host: "example.internal", Auth: SCRAM}); err != nil {
		t.Fatal(err)
	}
	var missing *NotFoundError
	err := c.SetProfile("work", Profile{Connection: "primary", Backend: "absent"})
	if !errors.As(err, &missing) || missing.Kind != KindBackend {
		t.Fatalf("error = %v, want a backend NotFoundError", err)
	}
}

func TestRemoveRefusesWhileBound(t *testing.T) {
	c := loaded(t)
	var inUse *InUseError
	if err := c.RemoveConnection("primary", false); !errors.As(err, &inUse) {
		t.Fatalf("error = %v, want InUseError", err)
	}
	if err := c.RemoveConnection("primary", true); err != nil {
		t.Fatalf("forced removal: %v", err)
	}
	if _, err := c.Connection("primary"); err == nil {
		t.Error("connection survived a forced removal")
	}
}

func TestRemoveProfileClearsCurrent(t *testing.T) {
	c := loaded(t)
	if err := c.Switch("work"); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveProfile("work"); err != nil {
		t.Fatal(err)
	}
	if c.CurrentProfile != "" {
		t.Errorf("current profile = %q after removing it", c.CurrentProfile)
	}
}

func TestInvalidEntries(t *testing.T) {
	tests := []struct {
		name  string
		field string
		apply func(*Config) error
	}{
		{name: "unknown engine", field: "engine", apply: func(c *Config) error {
			return c.SetConnection("primary", Connection{Engine: "oracle",
				Host: "example.internal", Auth: SQLLogin})
		}},
		{name: "auth the engine rejects", field: "auth", apply: func(c *Config) error {
			return c.SetConnection("primary", Connection{Engine: Postgres,
				Host: "example.internal", Auth: SQLLogin})
		}},
		{name: "empty host", field: "host", apply: func(c *Config) error {
			return c.SetConnection("primary", Connection{Engine: SQLServer, Auth: SQLLogin})
		}},
		{name: "port out of range", field: "port", apply: func(c *Config) error {
			return c.SetConnection("primary", Connection{Engine: SQLServer,
				Host: "example.internal", Auth: SQLLogin, Port: 70000})
		}},
		{name: "unknown provider", field: "provider", apply: func(c *Config) error {
			return c.SetBackend("main", Backend{Provider: "acme"})
		}},
		{name: "name with a slash", field: "connection name", apply: func(c *Config) error {
			return c.SetConnection("a/b", Connection{Engine: SQLServer,
				Host: "example.internal", Auth: SQLLogin})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var invalid *InvalidError
			err := tt.apply(New())
			if !errors.As(err, &invalid) {
				t.Fatalf("error = %v, want InvalidError", err)
			}
			if invalid.Field != tt.field {
				t.Errorf("field = %q, want %q", invalid.Field, tt.field)
			}
		})
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	c := NewAt(path)
	if err := c.SetConnection("primary", Connection{Engine: SQLServer,
		Host: "example.internal", Port: 1433, Database: "AppCore", Auth: SQLLogin,
		Username: "reader", Params: map[string]string{"encrypt": "true"}}); err != nil {
		t.Fatal(err)
	}
	if err := c.SetBackend("main", Backend{Provider: OpenAI, Model: "model-a",
		BaseURL: "https://api.example.internal"}); err != nil {
		t.Fatal(err)
	}
	if err := c.SetProfile("work", Profile{Connection: "primary", Backend: "main"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Switch("work"); err != nil {
		t.Fatal(err)
	}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}

	reloaded, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := reloaded.Connection("primary")
	if err != nil {
		t.Fatal(err)
	}
	if entry.Host != "example.internal" || entry.Port != 1433 {
		t.Errorf("connection round trip lost fields: %+v", entry)
	}
	if reloaded.CurrentProfile != "work" {
		t.Errorf("current profile = %q", reloaded.CurrentProfile)
	}
}

func TestSavedFileIsOwnerOnlyAndHoldsNoSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	c := NewAt(path)
	if err := c.SetConnection("primary", Connection{Engine: SQLServer,
		Host: "example.internal", Auth: SQLLogin, Username: "reader"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	// Windows reports 0666 for any writable file whatever mode was passed, so
	// the mode check cannot run there; the ACL on the profile directory is what
	// protects it. The content check below runs everywhere, and on Windows it is
	// the whole of this test.
	if runtime.GOOS != "windows" {
		info, err := stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("permissions = %o, want 600", perm)
		}
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "username: reader") {
		t.Errorf("the connection was not saved:\n%s", raw)
	}
	// No secret field exists on any config struct, so this fails only if one is
	// added - which is the mistake worth catching. `secrets:` is allowed: that
	// stanza holds the storage POLICY, never a value.
	if secretish.MatchString(string(raw)) {
		t.Errorf("a credential-shaped key reached the config file:\n%s", raw)
	}
}

// secretish matches the keys that would mean a secret had been given a home in
// the file. Deliberately not `secret`, which would hit the policy stanza.
var secretish = regexp.MustCompile(`(?i)\b(password|passwd|pwd|api[_-]?key|apikey|` +
	`access[_-]?token|client[_-]?secret)\b`)

func TestLoadOfAMissingFileIsEmpty(t *testing.T) {
	c, err := LoadFrom(filepath.Join(t.TempDir(), "absent.yml"))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if len(c.Connections) != 0 || len(c.Profiles) != 0 {
		t.Error("a missing file produced entries")
	}
}

func TestLoadRejectsAnInvalidFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := write(path, "connections:\n  primary:\n    engine: oracle\n    host: h\n"); err != nil {
		t.Fatal(err)
	}
	var invalid *InvalidError
	if _, err := LoadFrom(path); !errors.As(err, &invalid) {
		t.Fatalf("error = %v, want InvalidError", err)
	}
}

func loaded(t *testing.T) *Config {
	t.Helper()
	c := NewAt(filepath.Join(t.TempDir(), "config.yml"))
	if err := c.SetConnection("primary", Connection{Engine: SQLServer,
		Host: "example.internal", Auth: SQLLogin, Username: "reader"}); err != nil {
		t.Fatal(err)
	}
	if err := c.SetBackend("main", Backend{Provider: Anthropic}); err != nil {
		t.Fatal(err)
	}
	if err := c.SetProfile("work", Profile{Connection: "primary", Backend: "main"}); err != nil {
		t.Fatal(err)
	}
	return c
}

// TestEngineAuthPairings pins the auth table: both engines authenticate with a
// kerberos ticket, and each keeps its own password-based mode.
func TestEngineAuthPairings(t *testing.T) {
	tests := []struct {
		engine Engine
		auth   Auth
		want   bool
	}{
		{engine: SQLServer, auth: SQLLogin, want: true},
		{engine: SQLServer, auth: Kerberos, want: true},
		{engine: SQLServer, auth: SCRAM, want: false},
		{engine: Postgres, auth: SCRAM, want: true},
		{engine: Postgres, auth: Kerberos, want: true},
		{engine: Postgres, auth: SQLLogin, want: false},
	}
	for _, tt := range tests {
		t.Run(string(tt.engine)+"/"+string(tt.auth), func(t *testing.T) {
			_, err := ParseAuth(tt.engine, string(tt.auth))
			if (err == nil) != tt.want {
				t.Fatalf("ParseAuth = %v, want accepted = %v", err, tt.want)
			}
			entry := Connection{Engine: tt.engine, Host: "example.internal", Auth: tt.auth}
			if err := New().SetConnection("primary", entry); (err == nil) != tt.want {
				t.Errorf("SetConnection = %v, want accepted = %v", err, tt.want)
			}
		})
	}
}

// TestKerberosNeedsNoStoredCredential: the ticket is the credential, so neither
// a password nor a login name is demanded.
func TestKerberosNeedsNoStoredCredential(t *testing.T) {
	if NeedsPassword(Kerberos) || NeedsUsername(Kerberos) {
		t.Error("kerberos is being asked for a stored credential")
	}
	for _, auth := range []Auth{SQLLogin, SCRAM} {
		if !NeedsPassword(auth) || !NeedsUsername(auth) {
			t.Errorf("%s is not being asked for a password and a login name", auth)
		}
	}
}
