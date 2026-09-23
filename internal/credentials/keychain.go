package credentials

import "errors"

// errMissing is what a backend reports when the item is simply not there. Each
// platform's backend translates its own vocabulary into it, so the store above
// has one meaning of "absent" to reason about.
var errMissing = errors.New("keychain item not found")

// errBlocked is what a backend reports when the keychain would only hand the
// item over after asking the user something: the item's access control no
// longer names this binary, or the keychain wants to be unlocked. It is a
// separate meaning from "absent" because it has its own way out, and because a
// dbmap that let that question be asked where nobody can see it would block
// forever instead of failing.
var errBlocked = errors.New("the keychain did not authorize dbmap for this item")

// ui says whether one keychain call may put the operating system's
// authorization dialog on screen. The zero value refuses it: a store nobody
// configured can fail, but it can never hang.
type ui bool

const (
	noUI    ui = false
	allowUI ui = true
)

// Keychain stores secrets in the operating system's own credential store: the
// Security framework on macOS, wincred on Windows, secret-service on Linux.
//
// The three operations are fields rather than direct calls so a unit test can
// drive every branch, including an unavailable keychain, without one being
// present. The item is addressed by service plus account, and the account is
// our own key - db:<name> or llm:<name>.
type Keychain struct {
	Service string

	// ui carries the caller's answer to "may this prompt". It belongs to the
	// session, decided once at startup from the streams and --no-input, not to
	// an individual read, so every call made through this store shares it.
	ui     ui
	get    func(service, account string, allow ui) (string, error)
	set    func(service, account, secret string, allow ui) error
	remove func(service, account string, allow ui) error
}

// NewKeychain returns a store backed by the OS keychain that never lets the
// keychain ask the user anything. Prompting is opt-in through Options, because
// the caller is the only one that knows whether a human is watching a terminal.
func NewKeychain(service string) *Keychain { return newKeychain(service, noUI) }

// newKeychain wires the platform backend with an explicit answer to whether a
// call may prompt.
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
