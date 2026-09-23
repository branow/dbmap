package connect

import (
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/branow/dbmap/internal/config"
)

func kerberos() config.Connection {
	cfg := sqlLogin()
	cfg.Auth = config.Kerberos
	cfg.Params = map[string]string{}
	return cfg
}

// The case this exists for: macOS keeps tickets in a Keychain-backed API: cache
// the driver cannot read. Converting it is the tool's job, not a step the user
// has to remember before every run.
func TestAnUnreadableCacheTypeIsConvertedAutomatically(t *testing.T) {
	var ran []string
	env := snapshotting(map[string]string{"KRB5CCNAME": "API:ABC-123"}, map[string]bool{}, &ran)

	path, err := credentialCache(kerberos(), env)
	if err != nil {
		t.Fatalf("credentialCache: %v", err)
	}
	if len(ran) != 1 {
		t.Fatalf("ran %d commands, want 1: %v", len(ran), ran)
	}
	if !strings.Contains(ran[0], "KRB5CCNAME=API:ABC-123") {
		t.Errorf("the conversion did not read the ambient cache: %s", ran[0])
	}
	if !strings.Contains(path, "dbmap") || strings.HasPrefix(path, "FILE:") {
		t.Errorf("converted cache path = %q, want a bare path under the cache dir", path)
	}
}

// A FILE: cache is already usable, so nothing is copied and nothing is spawned.
func TestAUsableCacheIsNotCopied(t *testing.T) {
	cases := map[string]map[string]string{
		"explicit FILE prefix": {"KRB5CCNAME": "FILE:/tmp/krb5cc_501"},
		"bare path":            {"KRB5CCNAME": "/tmp/krb5cc_501"},
	}

	for name, vars := range cases {
		t.Run(name, func(t *testing.T) {
			var ran []string
			env := snapshotting(vars, map[string]bool{"/tmp/krb5cc_501": false}, &ran)

			path, err := credentialCache(kerberos(), env)
			if err != nil {
				t.Fatalf("credentialCache: %v", err)
			}
			if path != "/tmp/krb5cc_501" {
				t.Errorf("path = %q, want the cache as given", path)
			}
			if len(ran) != 0 {
				t.Errorf("copied a cache that was already usable: %v", ran)
			}
		})
	}
}

// The parameter stays as an override for someone who wants a specific cache,
// and it must win over the ambient one.
func TestAnExplicitCacheParameterWins(t *testing.T) {
	var ran []string
	cfg := kerberos()
	cfg.Params[CredCacheParam] = "/tmp/mine"
	env := snapshotting(
		map[string]string{"KRB5CCNAME": "API:ABC-123"},
		map[string]bool{"/tmp/mine": false},
		&ran,
	)

	path, err := credentialCache(cfg, env)
	if err != nil {
		t.Fatalf("credentialCache: %v", err)
	}
	if path != "/tmp/mine" {
		t.Errorf("path = %q, want the parameter's value", path)
	}
	if len(ran) != 0 {
		t.Errorf("converted a cache despite an explicit override: %v", ran)
	}
}

// Where no conversion tool exists the user does have to act, so that is the one
// case whose remedy still names a file and the override parameter.
func TestAnUnconvertibleCacheAsksForTheManualStep(t *testing.T) {
	env := world(map[string]string{"KRB5CCNAME": "KEYRING:persistent:501"}, map[string]bool{})

	_, err := credentialCache(kerberos(), env)

	var cache *CredentialCacheError
	if !errors.As(err, &cache) {
		t.Fatalf("error is %T, want *CredentialCacheError", err)
	}
	if !cache.Unconvertible {
		t.Error("the error does not mark the cache unconvertible")
	}
	for _, want := range []string{"KEYRING", "kinit -c FILE:", CredCacheParam} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message does not name %q: %v", want, err)
		}
	}
}

// A conversion that fails must not be reported as an absent ticket: the user
// has one, and saying otherwise sends them to run kinit for nothing.
func TestAFailedConversionSaysWhatItCouldNotDo(t *testing.T) {
	env := world(map[string]string{"KRB5CCNAME": "API:ABC-123"}, map[string]bool{})
	env.look = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	env.run = func(string, []string, ...string) error { return errors.New("exit status 1") }

	_, err := credentialCache(kerberos(), env)

	var cache *CredentialCacheError
	if !errors.As(err, &cache) {
		t.Fatalf("error is %T, want *CredentialCacheError", err)
	}
	if cache.Type != "API" {
		t.Errorf("Type = %q, want the type it could not convert", cache.Type)
	}
	if !strings.Contains(err.Error(), "could not be converted") {
		t.Errorf("the message reads as something other than a failed conversion: %v", err)
	}
}

// No ticket at all is the only remaining case where kinit alone is the answer.
func TestNoTicketAsksForKinitAndNothingElse(t *testing.T) {
	env := world(map[string]string{}, map[string]bool{})

	_, err := credentialCache(kerberos(), env)

	var cache *CredentialCacheError
	if !errors.As(err, &cache) {
		t.Fatalf("error is %T, want *CredentialCacheError", err)
	}
	if cache.Remedy() != "kinit" {
		t.Errorf("Remedy() = %q, want a bare kinit", cache.Remedy())
	}
	if !errors.Is(cache.Err, fs.ErrNotExist) {
		t.Errorf("the cause is %v, want a missing file", cache.Err)
	}
}
