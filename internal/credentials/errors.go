package credentials

import "fmt"

// NotFoundError reports a key no store holds. It carries the key, never the
// value, and the key is an entry name, not a secret.
type NotFoundError struct{ Key string }

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("no secret stored for %q", e.Key)
}

// KeychainError reports an OS keychain that could not answer. It is a hard
// error on purpose: dbmap never falls back to a plaintext file on its own,
// because a transient failure would otherwise persist a database password. The
// remedy names what the user can do instead.
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
// environment: a CI job supplies secrets, it does not record them.
type ReadOnlyError struct{ Store string }

func (e *ReadOnlyError) Error() string {
	return fmt.Sprintf("the %s store is read-only", e.Store)
}

// remedy is the one message that explains the way out of a keychain failure. It
// is written once so every failure path says the same thing.
const remedy = "supply the secret through the " +
	"environment (see dbmap help), or opt in to a 0600 file with " +
	"--allow-plaintext or `dbmap config set secrets.fallback plaintext`"
