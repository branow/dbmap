package credentials

import (
	"errors"

	"github.com/zalando/go-keyring"
)

// Keychain stores secrets in the operating system's own credential store. The
// three operations are fields rather than direct calls so a unit test can drive
// every branch, including an unavailable keychain, without one being present.
type Keychain struct {
	Service string
	get     func(service, user string) (string, error)
	set     func(service, user, password string) error
	remove  func(service, user string) error
}

// NewKeychain returns a store backed by the OS keychain.
func NewKeychain(service string) *Keychain {
	if service == "" {
		service = Service
	}
	return &Keychain{
		Service: service,
		get:     keyring.Get,
		set:     keyring.Set,
		remove:  keyring.Delete,
	}
}

func (k *Keychain) Get(key string) (Secret, error) {
	value, err := k.get(k.Service, key)
	switch {
	case errors.Is(err, keyring.ErrNotFound):
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
	case err == nil, errors.Is(err, keyring.ErrNotFound):
		return nil
	default:
		return &KeychainError{Op: "delete", Key: key, Err: err, Remedy: remedy}
	}
}
