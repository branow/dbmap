package credentials

import "errors"

// errMissing is what a backend reports when the item is not there. Each
// platform translates its own vocabulary into it, so the store above has one
// meaning of "absent".
var errMissing = errors.New("keychain item not found")

// errBlocked is what a backend reports when the keychain would hand the item
// over only after asking the user something. It is separate from "absent"
// because it has its own way out, and because letting that question be asked
// where nobody can see it would block forever instead of failing.
var errBlocked = errors.New("the keychain did not authorize dbmap for this item")

// ui says whether a keychain call may put the OS authorization dialog on
// screen. The zero value refuses: a store nobody configured may fail, never
// hang.
type ui bool

const (
	noUI    ui = false
	allowUI ui = true
)

// Keychain stores secrets in the OS credential store: the Security framework on
// macOS, wincred on Windows, secret-service on Linux. The item is addressed by
// service plus account, the account being our own key - db:<name> or llm:<name>.
//
// The three operations are fields so a unit test can drive every branch,
// including an unavailable keychain, with no keychain present.
type Keychain struct {
	Service string

	// ui carries the caller's answer to "may this prompt". It belongs to the
	// session, decided once at startup, so every call through this store
	// shares it.
	ui     ui
	get    func(service, account string, allow ui) (string, error)
	set    func(service, account, secret string, allow ui) error
	remove func(service, account string, allow ui) error
}

// NewKeychain returns an OS-keychain store that never lets the keychain ask the
// user anything. Prompting is opt-in through Options, because only the caller
// knows whether a human is watching a terminal.
func NewKeychain(service string) *Keychain { return newKeychain(service, noUI) }

// newKeychain wires the platform backend with an explicit answer to "may this
// call prompt".
func newKeychain(service string, allow ui) *Keychain {
	if service == "" {
		service = Service
	}
	return &Keychain{
		Service: service,
		ui:      allow,
		get:     itemGet,
		set:     itemSet,
		remove:  itemDelete,
	}
}

func (k *Keychain) Get(key string) (Secret, error) {
	value, err := k.get(k.Service, key, k.ui)
	switch {
	case errors.Is(err, errMissing):
		return Secret{}, &NotFoundError{Key: key}
	case err != nil:
		return Secret{}, fail("read", key, err)
	}
	return NewSecret(value), nil
}

func (k *Keychain) Set(key string, secret Secret) error {
	if err := k.set(k.Service, key, secret.Reveal(), k.ui); err != nil {
		return fail("write", key, err)
	}
	return nil
}

func (k *Keychain) Delete(key string) error {
	err := k.remove(k.Service, key, k.ui)
	switch {
	case err == nil, errors.Is(err, errMissing):
		return nil
	default:
		return fail("delete", key, err)
	}
}
