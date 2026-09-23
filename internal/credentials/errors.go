package credentials

import (
	"errors"
	"fmt"
)

// NotFoundError reports a key no store holds. It carries the key - an entry
// name - and never the value.
type NotFoundError struct{ Key string }

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("no secret stored for %q", e.Key)
}

// KeychainError reports an OS keychain that could not answer. It is a hard
// error on purpose: dbmap never falls back to a plaintext file on its own,
// because a transient failure would otherwise persist a database password.
type KeychainError struct {
	Op     string
	Key    string
	Remedy string
	Err    error
}

func (e *KeychainError) Error() string {
	return fmt.Sprintf("keychain %s failed for %q: %v. %s", e.Op, e.Key, e.Err, e.Remedy)
}

func (e *KeychainError) Unwrap() error { return e.Err }

// ReadOnlyError reports a write to a store that can only be read, such as the
// environment.
type ReadOnlyError struct{ Store string }

func (e *ReadOnlyError) Error() string {
	return fmt.Sprintf("the %s store is read-only", e.Store)
}

// fail wraps a backend failure with the remedy that fits its cause: an item the
// keychain will not release has a different way out than an absent keychain.
func fail(op, key string, err error) error {
	way := remedy
	if errors.Is(err, errBlocked) {
		way = blocked(key)
	}
	return &KeychainError{Op: op, Key: key, Err: err, Remedy: way}
}

// remedy is the way out when the keychain could not answer at all.
const remedy = "supply the secret through the " +
	"environment (see dbmap help), or opt in to a 0600 file with " +
	"--allow-plaintext or `dbmap config set secrets.fallback plaintext`"

// blocked is the way out for an item the keychain holds but will not release.
// A rebuild gives the binary a new code identity, so the item's access control
// no longer names it; storing it again from this build repairs that.
func blocked(key string) string {
	return fmt.Sprintf("re-authorize the item - allow access when the keychain "+
		"asks, or remove and re-add the entry so this build stores it again - "+
		"or supply the secret through %s", EnvName(key))
}
