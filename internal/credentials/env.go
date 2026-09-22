package credentials

import "os"

// Env reads secrets from the environment. It is what makes a headless run work
// on a machine with no keychain, and it is the reason the default policy can
// refuse a plaintext file without stranding CI.
type Env struct {
	lookup func(string) string
}

// NewEnv returns an environment-backed store. A nil lookup reads the process
// environment; a test passes its own.
func NewEnv(lookup func(string) string) *Env {
	if lookup == nil {
		lookup = os.Getenv
	}
	return &Env{lookup: lookup}
}

// Name reports which variable supplies a key, for help text and diagnostics.
func (e *Env) Name(key string) string { return EnvName(key) }

func (e *Env) Get(key string) (Secret, error) {
	if value := e.lookup(EnvName(key)); value != "" {
		return NewSecret(value), nil
	}
	return Secret{}, &NotFoundError{Key: key}
}

// Set refuses: the environment is supplied to dbmap, not written by it.
func (e *Env) Set(string, Secret) error { return &ReadOnlyError{Store: "environment"} }

// Delete refuses, for the same reason as Set.
func (e *Env) Delete(string) error { return &ReadOnlyError{Store: "environment"} }
