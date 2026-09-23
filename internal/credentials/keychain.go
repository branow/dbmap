package credentials

import "errors"

// errMissing is every platform's "the item is not there".
var errMissing = errors.New("keychain item not found")

// errBlocked is the keychain wanting to ask the user something first. It is
// separate from absent because it has its own remedy.
var errBlocked = errors.New("the keychain did not authorize dbmap for this item")

// ui says whether a keychain call may put the OS authorization dialog on
// screen. The zero value refuses: a dialog nobody can see blocks forever.
type ui bool

const (
	noUI    ui = false
	allowUI ui = true
)

// Keychain stores secrets in the OS credential store, addressed by service plus
// account, the account being our own key. The three operations are fields so a
// unit test can drive every branch with no keychain present.
type Keychain struct {
	Service string

	ui     ui
	get    func(service, account string, allow ui) (string, error)
	set    func(service, account, secret string, allow ui) error
	remove func(service, account string, allow ui) error
}

// NewKeychain returns an OS-keychain store that never lets the keychain prompt.
// Prompting is opt-in through Options: only the caller knows whether a human is
// watching a terminal.
func NewKeychain(service string) *Keychain { return newKeychain(service, noUI) }

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
