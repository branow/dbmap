package credentials

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// stub is a keychain whose three operations are scripted, so every branch is
// reachable without an operating system keychain being present.
func stub(values map[string]string, failure error) *Keychain {
	k := NewKeychain("test")
	k.get = func(_, key string) (string, error) {
		if failure != nil {
			return "", failure
		}
		value, ok := values[key]
		if !ok {
			return "", errMissing
		}
		return value, nil
	}
	k.set = func(_, key, value string) error {
		if failure != nil {
			return failure
		}
		values[key] = value
		return nil
	}
	k.remove = func(_, key string) error {
		if failure != nil {
			return failure
		}
		if _, ok := values[key]; !ok {
			return errMissing
		}
		delete(values, key)
		return nil
	}
	return k
}

var broken = errors.New("dbus is not running")

func TestKeychainErrors(t *testing.T) {
	t.Run("missing key", func(t *testing.T) {
		_, err := stub(map[string]string{}, nil).Get(DBKey("primary"))
		var missing *NotFoundError
		if !errors.As(err, &missing) {
			t.Fatalf("error = %v, want NotFoundError", err)
		}
	})
	t.Run("unavailable keychain names the fix", func(t *testing.T) {
		_, err := stub(nil, broken).Get(DBKey("primary"))
		var failure *KeychainError
		if !errors.As(err, &failure) {
			t.Fatalf("error = %v, want KeychainError", err)
		}
		if failure.Remedy == "" {
			t.Error("the error does not name a way out")
		}
		if !errors.Is(err, broken) {
			t.Error("the cause was dropped")
		}
	})
	t.Run("deleting an absent key is not an error", func(t *testing.T) {
		if err := stub(map[string]string{}, nil).Delete(DBKey("primary")); err != nil {
			t.Fatalf("Delete: %v", err)
		}
	})
}

func TestEnvStore(t *testing.T) {
	store := NewEnv(func(name string) string {
		if name == "DBMAP_SECRET_DB_PRIMARY" {
			return "from-env"
		}
		return ""
	})
	secret, err := store.Get(DBKey("primary"))
	if err != nil || secret.Reveal() != "from-env" {
		t.Fatalf("secret = %v, err = %v", secret, err)
	}
	if _, err := store.Get(DBKey("other")); err == nil {
		t.Error("an unset variable produced a secret")
	}
	var readOnly *ReadOnlyError
	if err := store.Set(DBKey("primary"), NewSecret("x")); !errors.As(err, &readOnly) {
		t.Errorf("Set error = %v, want ReadOnlyError", err)
	}
	if err := store.Delete(DBKey("primary")); !errors.As(err, &readOnly) {
		t.Errorf("Delete error = %v, want ReadOnlyError", err)
	}
}

func TestFileStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	store := NewFile(path)
	if err := store.Set(DBKey("primary"), NewSecret("s3cret")); err != nil {
		t.Fatal(err)
	}
	secret, err := store.Get(DBKey("primary"))
	if err != nil || secret.Reveal() != "s3cret" {
		t.Fatalf("secret = %v, err = %v", secret, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("permissions = %o, want 600", perm)
	}
	if err := store.Delete(DBKey("primary")); err != nil {
		t.Fatal(err)
	}
	var missing *NotFoundError
	if _, err := store.Get(DBKey("primary")); !errors.As(err, &missing) {
		t.Errorf("error after delete = %v, want NotFoundError", err)
	}
}

func TestFakeStore(t *testing.T) {
	fake := NewFake()
	if err := fake.Set(LLMKey("main"), NewSecret("key")); err != nil {
		t.Fatal(err)
	}
	if fake.Values[LLMKey("main")] != "key" {
		t.Error("the fake did not record the secret")
	}
	fake.Err = broken
	if _, err := fake.Get(LLMKey("main")); !errors.Is(err, broken) {
		t.Errorf("error = %v, want the injected failure", err)
	}
}

// TestNewKeychainUsesThePlatformBackend catches a build where no platform file
// supplied the three operations, which would otherwise only show up as a nil
// call at runtime on a user's machine.
func TestNewKeychainUsesThePlatformBackend(t *testing.T) {
	keychain := NewKeychain("")
	if keychain.Service != Service {
		t.Errorf("service = %q, want %q", keychain.Service, Service)
	}
	if keychain.get == nil || keychain.set == nil || keychain.remove == nil {
		t.Fatal("the platform backend is not wired")
	}
	if named := NewKeychain("other"); named.Service != "other" {
		t.Errorf("service = %q, want the one given", named.Service)
	}
}
