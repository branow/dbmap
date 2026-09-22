//go:build darwin && cgo && keychain

// This file is excluded from the normal suite twice over: it needs the
// `keychain` build tag and DBMAP_KEYCHAIN_TEST=1. `go test ./...` must never
// touch the user's login keychain.
//
//	DBMAP_KEYCHAIN_TEST=1 go test -tags keychain ./internal/credentials/ -run Keychain -v
package credentials

import (
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

// TestKeychainRoundTrip exercises the real Security framework: add, read back,
// replace, delete. It files its items under a service of its own so it can
// never read, change or delete an item dbmap stored for a real connection.
func TestKeychainRoundTrip(t *testing.T) {
	if os.Getenv("DBMAP_KEYCHAIN_TEST") != "1" {
		t.Skip("set DBMAP_KEYCHAIN_TEST=1 to run against the real keychain")
	}
	service := fmt.Sprintf("dbmap-test-%d-%d", os.Getpid(), time.Now().UnixNano())
	keychain := NewKeychain(service)
	key := DBKey("integration")
	t.Cleanup(func() {
		if err := keychain.Delete(key); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})

	var missing *NotFoundError
	if _, err := keychain.Get(key); !errors.As(err, &missing) {
		t.Fatalf("error before the item exists = %v, want NotFoundError", err)
	}
	if err := keychain.Set(key, NewSecret("first")); err != nil {
		t.Fatalf("Set: %v", err)
	}
	secret, err := keychain.Get(key)
	if err != nil || secret.Reveal() != "first" {
		t.Fatalf("secret = %v, err = %v", secret, err)
	}

	// A second Set must replace the value rather than fail on a duplicate.
	if err := keychain.Set(key, NewSecret("second")); err != nil {
		t.Fatalf("Set over an existing item: %v", err)
	}
	secret, err = keychain.Get(key)
	if err != nil || secret.Reveal() != "second" {
		t.Fatalf("secret after replacement = %v, err = %v", secret, err)
	}

	if err := keychain.Delete(key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := keychain.Get(key); !errors.As(err, &missing) {
		t.Fatalf("error after delete = %v, want NotFoundError", err)
	}
	// Deleting what is already gone stays a no-op, as the store contract says.
	if err := keychain.Delete(key); err != nil {
		t.Fatalf("Delete of an absent item: %v", err)
	}
}
