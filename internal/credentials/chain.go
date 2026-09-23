package credentials

import (
	"errors"
	"fmt"
)

// Policy decides what may hold a secret when the OS keychain cannot. Never is
// the default: a keychain failure is a hard error, never a quiet write to disk.
type Policy string

const (
	PolicyNever     Policy = "never"
	PolicyPlaintext Policy = "plaintext"
)

// Options describe the store stack to assemble.
type Options struct {
	Policy Policy
	// Service empty means the dbmap default.
	Service string
	// File is used only under PolicyPlaintext.
	File string
	// Env nil means the process environment.
	Env func(string) string
	// Interactive says a human is watching a terminal and may answer the
	// keychain's dialog. False by default: a dialog nobody can see would hang
	// a script or a CI job forever.
	Interactive bool
}

// chain is the assembled stack: environment, then keychain, then - only under
// the plaintext policy - the file.
type chain struct {
	env      *Env
	keychain *Keychain
	file     *File
}

// New assembles the store for a policy.
func New(o Options) (Store, error) {
	keychain := newKeychain(o.Service, ui(o.Interactive))
	switch o.Policy {
	case "", PolicyNever:
		return &chain{env: NewEnv(o.Env), keychain: keychain}, nil
	case PolicyPlaintext:
		if o.File == "" {
			return nil, fmt.Errorf("policy %q needs a file path", o.Policy)
		}
		return &chain{
			env:      NewEnv(o.Env),
			keychain: keychain,
			file:     NewFile(o.File),
		}, nil
	default:
		return nil, fmt.Errorf("unknown secrets policy %q", o.Policy)
	}
}

// Get asks the environment, then the keychain, then the file.
func (c *chain) Get(key string) (Secret, error) {
	if secret, err := c.env.Get(key); err == nil {
		return secret, nil
	}
	secret, err := c.keychain.Get(key)
	switch {
	case err == nil:
		return secret, nil
	case c.file == nil:
		return Secret{}, err
	}
	fromFile, fileErr := c.file.Get(key)
	if fileErr == nil {
		return fromFile, nil
	}
	// Report the keychain problem, not the file miss: the keychain is the store
	// the user expects to be answering.
	var missing *NotFoundError
	if errors.As(err, &missing) {
		return Secret{}, fileErr
	}
	return Secret{}, err
}

// Set falls back to the file only when the policy opted in; otherwise a
// keychain failure ends the command.
func (c *chain) Set(key string, secret Secret) error {
	err := c.keychain.Set(key, secret)
	if err == nil || c.file == nil {
		return err
	}
	return c.file.Set(key, secret)
}

// Delete removes the key from every store that can hold it.
func (c *chain) Delete(key string) error {
	err := c.keychain.Delete(key)
	if c.file == nil {
		return err
	}
	if fileErr := c.file.Delete(key); fileErr != nil {
		return fileErr
	}
	return nil
}
