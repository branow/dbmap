package credentials

import (
	"errors"

	"github.com/branow/gokey"
)

// Keychain stores secrets in the OS credential store, addressed by service plus
// account, the account being our own key. gokey carries the per-platform
// backends; what this type adds is dbmap's error vocabulary around them.
//
// The three operations are fields so a unit test can drive every branch with no
// credential store present.
type Keychain struct {
	Service string

	// prompt says a call may put the OS authorization dialog on screen. The
	// zero value refuses: a dialog nobody can see blocks forever.
	prompt bool

	get    func(service, account string, prompt bool) (string, error)
	set    func(service, account, secret string, prompt bool) error
	remove func(service, account string, prompt bool) error
}

// NewKeychain returns an OS-keychain store that never lets the keychain prompt.
// Prompting is opt-in through Options: only the caller knows whether a human is
// watching a terminal.
func NewKeychain(service string) *Keychain { return newKeychain(service, false) }

func newKeychain(service string, prompt bool) *Keychain {
	if service == "" {
		service = Service
	}
	return &Keychain{
		Service: service,
		prompt:  prompt,
		get:     itemGet,
		set:     itemSet,
		remove:  itemDelete,
	}
}

func itemGet(service, account string, prompt bool) (string, error) {
	return gokey.Get(service, account, gokey.WithPrompt(prompt))
}

func itemSet(service, account, secret string, prompt bool) error {
	return gokey.Set(service, account, secret, gokey.WithPrompt(prompt))
}

func itemDelete(service, account string, prompt bool) error {
	return gokey.Delete(service, account, gokey.WithPrompt(prompt))
}

func (k *Keychain) Get(key string) (Secret, error) {
	value, err := k.get(k.Service, key, k.prompt)
	switch {
	case errors.Is(err, gokey.ErrNotFound):
		return Secret{}, &NotFoundError{Key: key}
	case err != nil:
		return Secret{}, fail("read", key, err)
	}
	return NewSecret(value), nil
}

func (k *Keychain) Set(key string, secret Secret) error {
	if err := k.set(k.Service, key, secret.Reveal(), k.prompt); err != nil {
		return fail("write", key, err)
	}
	return nil
}

func (k *Keychain) Delete(key string) error {
	err := k.remove(k.Service, key, k.prompt)
	switch {
	case err == nil, errors.Is(err, gokey.ErrNotFound):
		return nil
	default:
		return fail("delete", key, err)
	}
}
