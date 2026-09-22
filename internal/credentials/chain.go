package credentials

import (
	"errors"
	"fmt"
)

// Policy decides what may hold a secret when the OS keychain cannot.
type Policy string

// The policies. Never is the default: a keychain failure is a hard error, not a
// quiet write to disk, because a transient failure would otherwise persist a
// database password permanently.
const (
	PolicyNever     Policy = "never"
	PolicyPlaintext Policy = "plaintext"
)

// Options describe the store stack to assemble.
type Options struct {
	// Policy selects whether the plaintext file participates at all.
	Policy Policy
	// Service is the keychain service; empty means the dbmap default.
	Service string
	// File is the plaintext store's path, used only under PolicyPlaintext.
	File string
	// Env reads the environment; nil means the process environment.
	Env func(string) string
}

// chain is the assembled stack: the environment answers first so a headless run
// needs no keychain, the keychain answers next, and the plaintext file
// participates only when the policy opted in.
type chain struct {
	env      *Env
	keychain *Keychain
	file     *File
}

// New assembles the store for a policy.
func New(o Options) (Store, error) {
	switch o.Policy {
	case "", PolicyNever:
		return &chain{env: NewEnv(o.Env), keychain: NewKeychain(o.Service)}, nil
	case PolicyPlaintext:
		if o.File == "" {
			return nil, fmt.Errorf("policy %q needs a file path", o.Policy)
		}
		return &chain{
			env:      NewEnv(o.Env),
			keychain: NewKeychain(o.Service),
			file:     NewFile(o.File),
		}, nil
	default:
		return nil, fmt.Errorf("unknown secrets policy %q", o.Policy)
	}
}

// Get asks the environment, then the keychain, then - only under the plaintext
// policy - the file. A keychain that is present but empty still lets the file
// answer, because that is where an opted-in user's secret was written.
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
	// Report the keychain problem, not the file miss: the keychain is the
	// store the user expects to be answering.
	var missing *NotFoundError
	if errors.As(err, &missing) {
		return Secret{}, fileErr
	}
	return Secret{}, err
}

// Set writes to the keychain, and falls back to the file only when the policy
// opted in. Under the default policy a keychain failure ends the command.
func (c *chain) Set(key string, secret Secret) error {
	err := c.keychain.Set(key, secret)
	if err == nil || c.file == nil {
		return err
	}
	return c.file.Set(key, secret)
}

// Delete removes the key from every store that can hold it, so a removed entry
// leaves no secret behind.
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
