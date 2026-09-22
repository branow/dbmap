package credentials

import "errors"

// errMissing is what a backend reports when the item is simply not there. Each
// platform's backend translates its own vocabulary into it, so the store above
// has one meaning of "absent" to reason about.
var errMissing = errors.New("keychain item not found")

// Keychain stores secrets in the operating system's own credential store: the
// Security framework on macOS, wincred on Windows, secret-service on Linux.
//
// The three operations are fields rather than direct calls so a unit test can
// drive every branch, including an unavailable keychain, without one being
// present. The item is addressed by service plus account, and the account is
// our own key - db:<name> or llm:<name>.
type Keychain struct {
	Service string
	get     func(service, account string) (string, error)
	set     func(service, account, secret string) error
	remove  func(service, account string) error
}

// NewKeychain returns a store backed by the OS keychain.
func NewKeychain(service string) *Keychain {
	if service == "" {
		service = Service
	}
	return &Keychain{
		Service: service,
		get:     itemGet,
		set:     itemSet,
		remove:  itemDelete,
	}
}

func (k *Keychain) Get(key string) (Secret, error) {
	value, err := k.get(k.Service, key)
	switch {
	case errors.Is(err, errMissing):
		return Secret{}, &NotFoundError{Key: key}
	case err != nil:
		return Secret{}, &KeychainError{Op: "read", Key: key, Err: err, Remedy: remedy}
	}
	return NewSecret(value), nil
}

func (k *Keychain) Set(key string, secret Secret) error {
	if err := k.set(k.Service, key, secret.Reveal()); err != nil {
		return &KeychainError{Op: "write", Key: key, Err: err, Remedy: remedy}
	}
	return nil
}

func (k *Keychain) Delete(key string) error {
	err := k.remove(k.Service, key)
	switch {
	case err == nil, errors.Is(err, errMissing):
		return nil
	default:
		return &KeychainError{Op: "delete", Key: key, Err: err, Remedy: remedy}
	}
}
