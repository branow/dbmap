package credentials

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/branow/gokey"
)

// errOwnerEdit is how gokey reports errSecInvalidOwnerEdit: no sentinel, the
// status in the message.
var errOwnerEdit = errors.New("the keychain refused the request (OSStatus -25244)")

// foreignKeychain models the macOS keychain after an upgrade: an entry stored
// by another build is released only through the dialog, and storing it again
// binds it to this one.
type foreignKeychain struct {
	values  map[string]string
	foreign map[string]bool
	// setErrs, when not empty, are returned by the next calls to set in turn.
	setErrs []error
	reads   []bool
	dropped []string
}

func newForeignKeychain(k *Keychain, key, value string) *foreignKeychain {
	f := &foreignKeychain{
		values:  map[string]string{key: value},
		foreign: map[string]bool{key: true},
	}
	k.get = func(_, account string, prompt bool) (string, error) {
		f.reads = append(f.reads, prompt)
		value, ok := f.values[account]
		switch {
		case !ok:
			return "", gokey.ErrNotFound
		case f.foreign[account] && !prompt:
			return "", gokey.ErrBlocked
		}
		return value, nil
	}
	k.set = func(_, account, value string, _ bool) error {
		if len(f.setErrs) > 0 {
			err := f.setErrs[0]
			f.setErrs = f.setErrs[1:]
			if err != nil {
				return err
			}
		}
		f.values[account] = value
		f.foreign[account] = false
		return nil
	}
	k.drop = func(_, account string) error {
		f.dropped = append(f.dropped, account)
		delete(f.values, account)
		delete(f.foreign, account)
		return nil
	}
	return f
}

// TestApprovedEntryIsStoredAgain is the dialog after every upgrade: an entry
// released through it is stored again under this build, so the next read is
// silent instead of asking on every run.
func TestApprovedEntryIsStoredAgain(t *testing.T) {
	key := DBKey("primary")
	keychain := newKeychain("test", true, &bytes.Buffer{})
	fake := newForeignKeychain(keychain, key, password)

	secret, err := keychain.Get(key)
	if err != nil || secret.Reveal() != password {
		t.Fatalf("Get = %q, %v", secret.Reveal(), err)
	}
	if want := []bool{false, true}; !slices.Equal(fake.reads, want) {
		t.Errorf("reads = %v, want %v: silent first, then the dialog", fake.reads, want)
	}
	if fake.foreign[key] {
		t.Fatal("the approved entry is still bound to the other build")
	}

	fake.reads = nil
	if _, err := keychain.Get(key); err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if want := []bool{false}; !slices.Equal(fake.reads, want) {
		t.Errorf("second reads = %v, want %v: no dialog once stored again", fake.reads, want)
	}
}

// TestBlockedEntryWithoutPromptIsLeftAlone: a store not allowed to show the
// dialog fails the read with the remedy, never raises the dialog, and does not
// touch the entry.
func TestBlockedEntryWithoutPromptIsLeftAlone(t *testing.T) {
	key := DBKey("primary")
	keychain := NewKeychain("test")
	fake := newForeignKeychain(keychain, key, password)
	keychain.set = func(_, _, _ string, _ bool) error {
		t.Error("a refused read wrote the entry")
		return nil
	}

	_, err := keychain.Get(key)
	var failure *KeychainError
	if !errors.As(err, &failure) || !errors.Is(err, gokey.ErrBlocked) {
		t.Fatalf("error = %v, want a blocked KeychainError", err)
	}
	if want := []bool{false}; !slices.Equal(fake.reads, want) {
		t.Errorf("reads = %v, want %v", fake.reads, want)
	}
	if !fake.foreign[key] || fake.values[key] != password || len(fake.dropped) != 0 {
		t.Error("the entry was changed")
	}
}

// TestOwnerEditDropsAndRetries: the keychain will not let this binary replace
// an item another program owns, so on exactly that refusal the old item is
// deleted and added again.
func TestOwnerEditDropsAndRetries(t *testing.T) {
	key := DBKey("primary")
	keychain := NewKeychain("test")
	fake := newForeignKeychain(keychain, key, "old")
	fake.setErrs = []error{errOwnerEdit}

	if err := keychain.Set(key, NewSecret(password)); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if len(fake.dropped) != 1 || fake.dropped[0] != key {
		t.Errorf("dropped = %v, want [%s]", fake.dropped, key)
	}
	if fake.values[key] != password || fake.foreign[key] {
		t.Errorf("entry = %q (foreign %v), want the new value bound to this build",
			fake.values[key], fake.foreign[key])
	}
}

// TestOtherSetErrorDoesNotDrop: after any other failure the retry would most
// likely fail too, and a deleted entry would be a lost secret.
func TestOtherSetErrorDoesNotDrop(t *testing.T) {
	for _, cause := range []error{broken, gokey.ErrBlocked, gokey.ErrUnavailable} {
		t.Run(cause.Error(), func(t *testing.T) {
			key := DBKey("primary")
			keychain := NewKeychain("test")
			fake := newForeignKeychain(keychain, key, "old")
			fake.setErrs = []error{cause}

			err := keychain.Set(key, NewSecret(password))
			var failure *KeychainError
			if !errors.As(err, &failure) || !errors.Is(err, cause) {
				t.Fatalf("error = %v, want KeychainError wrapping %v", err, cause)
			}
			if len(fake.dropped) != 0 || fake.values[key] != "old" {
				t.Error("the entry was deleted after a failure that was not -25244")
			}
		})
	}
}

// TestFailedRebindWarnsButReads: storing an approved entry again only spares
// the next dialog, so its failure is a warning and the read still succeeds.
func TestFailedRebindWarnsButReads(t *testing.T) {
	key := DBKey("primary")
	warn := &bytes.Buffer{}
	keychain := newKeychain("test", true, warn)
	fake := newForeignKeychain(keychain, key, password)
	fake.setErrs = []error{broken}

	secret, err := keychain.Get(key)
	if err != nil || secret.Reveal() != password {
		t.Fatalf("Get = %q, %v; a failed re-save failed the read", secret.Reveal(), err)
	}
	for _, want := range []string{"warning:", key, broken.Error()} {
		if !strings.Contains(warn.String(), want) {
			t.Errorf("warning %q does not mention %q", warn.String(), want)
		}
	}
	if strings.Contains(warn.String(), password) {
		t.Error("the warning carries the secret")
	}
}
