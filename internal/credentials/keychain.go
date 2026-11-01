package credentials

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/branow/gokey"
)

// Keychain stores secrets in the OS credential store, addressed by service plus
// account, the account being our own key. gokey carries the per-platform
// backends; what this type adds is dbmap's error vocabulary around them.
//
// The operations are fields so a unit test can drive every branch with no
// credential store present.
type Keychain struct {
	Service string

	// prompt says a call may put the OS authorization dialog on screen. The
	// zero value refuses: a dialog nobody can see blocks forever.
	prompt bool

	// warn receives what goes wrong without failing the call: an entry that
	// could not be stored again after approval. Nil means standard error.
	warn io.Writer

	get    func(service, account string, prompt bool) (string, error)
	set    func(service, account, secret string, prompt bool) error
	remove func(service, account string, prompt bool) error

	// drop deletes an item the keychain will not let this binary replace; see
	// Set.
	drop func(service, account string) error
}

// NewKeychain returns an OS-keychain store that never lets the keychain prompt.
// Prompting is opt-in through Options: only the caller knows whether a human is
// watching a terminal.
func NewKeychain(service string) *Keychain { return newKeychain(service, false, nil) }

func newKeychain(service string, prompt bool, warn io.Writer) *Keychain {
	if service == "" {
		service = Service
	}
	return &Keychain{
		Service: service,
		prompt:  prompt,
		warn:    warn,
		get:     itemGet,
		set:     itemSet,
		remove:  itemDelete,
		drop:    dropWithSecurityTool,
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

// Get reads silently first: an entry this binary stored needs no dialog, and
// only a refusal tells that it does not. Only then, and only when the caller
// opted in to the dialog, is the read repeated with it allowed.
func (k *Keychain) Get(key string) (Secret, error) {
	value, err := k.get(k.Service, key, false)
	approved := false
	if errors.Is(err, gokey.ErrBlocked) && k.prompt {
		value, err = k.get(k.Service, key, true)
		approved = err == nil
	}
	switch {
	case errors.Is(err, gokey.ErrNotFound):
		return Secret{}, &NotFoundError{Key: key}
	case err != nil:
		return Secret{}, fail("read", key, err)
	}
	secret := NewSecret(value)
	// An entry released through the dialog is bound to another build - a
	// previous release, a local rebuild - and would ask again on every run
	// until this binary stores it again.
	if approved {
		k.rebind(key, secret)
	}
	return secret, nil
}

// Set stores the secret, replacing any entry under key.
func (k *Keychain) Set(key string, secret Secret) error {
	err := k.set(k.Service, key, secret.Reveal(), k.prompt)
	if isOwnerEdit(err) {
		// The keychain will not let this binary replace an item another
		// program owns, such as one an older build stored through
		// /usr/bin/security. The old item is deleted through that tool and
		// added again, bound to this binary.
		if k.drop(k.Service, key) == nil {
			err = k.set(k.Service, key, secret.Reveal(), k.prompt)
		}
	}
	if err != nil {
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

// rebind stores an approved entry again so the next read is silent. Failing
// costs only that dialog on later reads, so it warns instead of failing the
// read.
func (k *Keychain) rebind(key string, secret Secret) {
	if err := k.Set(key, secret); err != nil {
		warn := k.warn
		if warn == nil {
			warn = os.Stderr
		}
		fmt.Fprintf(warn, "warning: could not store %q again under this build, "+
			"so the keychain will keep asking for permission: %v\n", key, err)
	}
}

// isOwnerEdit reports the keychain refusing to replace an item another program
// owns (errSecInvalidOwnerEdit). gokey has no sentinel for it, so the status it
// keeps in the message is matched. Only this refusal may lead to deleting the
// old item: after any other failure the retry would most likely fail too, and
// the entry would be gone.
func isOwnerEdit(err error) bool {
	return err != nil && strings.Contains(err.Error(), "OSStatus -25244")
}

// dropWithSecurityTool deletes an item through /usr/bin/security. It only
// deletes: the item's address is passed, and no secret is read or written
// through it.
func dropWithSecurityTool(service, account string) error {
	out, err := exec.Command("/usr/bin/security", "delete-generic-password",
		"-s", service, "-a", account).CombinedOutput()
	if err != nil {
		return fmt.Errorf("security delete-generic-password: %w: %s",
			err, strings.TrimSpace(string(out)))
	}
	return nil
}
