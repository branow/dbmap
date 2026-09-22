package cmd

import (
	"errors"
	"fmt"

	"github.com/branow/dbmap/internal/cmdutil"
	"github.com/branow/dbmap/internal/credentials"
)

// ask fills a value the flags left empty. When a person is at the terminal it
// prompts, offering def; when nobody is, a required value fails naming its flag
// rather than blocking on a stream that will never carry an answer. A default
// is only ever offered to a person: a scripted run states what it wants.
func ask(f *cmdutil.Factory, target *string, flag, label, def string, required bool) error {
	if *target != "" {
		return nil
	}
	if f.IO.CanPrompt() {
		answer, err := f.IO.Prompt(label, def)
		if err != nil {
			return err
		}
		*target = answer
	}
	if *target == "" && required {
		return &cmdutil.ValidationError{Field: flag, Reason: "required"}
	}
	return nil
}

// confirm asks a yes/no question, answering with the default when nobody is
// there to be asked.
func confirm(f *cmdutil.Factory, label string, def bool) (bool, error) {
	if !f.IO.CanPrompt() {
		return def, nil
	}
	return f.IO.Confirm(label, def)
}

// source records where a secret came from. It matters after the fact: a
// keychain that cannot store an environment-supplied secret has lost nothing,
// because the environment supplies it again on every run.
type source int

const (
	sourceNone source = iota
	sourceStdin
	sourceEnv
	sourcePrompt
)

// readSecret obtains a credential, in the order a user expects to be obeyed:
// the explicit --…-stdin flag, then the environment variable for this key, then
// a hidden prompt. Nothing is asked for when the entry needs no secret. The
// value is returned, never echoed and never stored in the config file.
func readSecret(f *cmdutil.Factory, fromStdin bool, key, label, flag string,
	required bool) (credentials.Secret, source, error) {
	if fromStdin {
		raw, err := f.IO.ReadAll()
		if err != nil {
			return credentials.Secret{}, sourceNone, err
		}
		if raw == "" && required {
			return credentials.Secret{}, sourceNone, &cmdutil.ValidationError{
				Field: flag, Reason: "no value arrived on stdin"}
		}
		return credentials.NewSecret(raw), sourceStdin, nil
	}
	// An auth mode that carries no password is asked nothing and reads nothing:
	// the ticket cache is the credential, and the store stays empty.
	if !required {
		return credentials.Secret{}, sourceNone, nil
	}
	if secret, ok := f.EnvSecret(key); ok {
		return secret, sourceEnv, nil
	}
	if !f.IO.CanPrompt() {
		return credentials.Secret{}, sourceNone, &cmdutil.ValidationError{
			Field:  label,
			Reason: fmt.Sprintf("required; use %s or set %s", flag, credentials.EnvName(key)),
		}
	}
	raw, err := f.IO.PromptPassword(label)
	if err != nil {
		return credentials.Secret{}, sourceNone, err
	}
	if raw == "" {
		return credentials.Secret{}, sourceNone,
			&cmdutil.ValidationError{Field: label, Reason: "required"}
	}
	return credentials.NewSecret(raw), sourcePrompt, nil
}

// remember stores a secret under its key. A keychain that refuses one the
// environment supplied is reported and survived: the credential has a durable
// source outside dbmap, so the entry is still worth defining. A secret that
// arrived from stdin or a prompt has no second source, so refusing to store it
// fails the command rather than losing it.
func remember(f *cmdutil.Factory, key string, secret credentials.Secret, from source) error {
	if secret.Empty() {
		return nil
	}
	err := f.Store.Set(key, secret)
	if err == nil || from != sourceEnv {
		return err
	}
	var refused *credentials.KeychainError
	if !errors.As(err, &refused) {
		return err
	}
	fmt.Fprintf(f.IO.ErrOut, "warning: %s was not stored (%v); %s supplies it\n",
		key, refused.Err, credentials.EnvName(key))
	return nil
}

// forget removes a stored secret, treating an absent one as already gone: a
// removal must not fail because the entry never had a password.
func forget(f *cmdutil.Factory, key string) error {
	err := f.Store.Delete(key)
	var missing *credentials.NotFoundError
	if errors.As(err, &missing) {
		return nil
	}
	return err
}

// verify runs a probe and decides what its failure means. A definitive
// rejection - a production target, an unsupported setup, a credential the
// server refused - fails the command, because storing those settings would only
// defer the same failure to the first real run. An unreachable dependency is
// ambiguous: the machine may be off its network, and refusing to record the
// entry helps nobody, so it is stored with a warning. The classification is the
// exit table's, so there is exactly one opinion in the program about what a
// given failure means.
func verify(f *cmdutil.Factory, skip bool, run func() error) error {
	if skip || run == nil {
		return nil
	}
	err := run()
	if err == nil {
		return nil
	}
	if cmdutil.ExitCode(err) != cmdutil.ExitUnavailable {
		return err
	}
	fmt.Fprintf(f.IO.ErrOut, "warning: stored without verifying: %v\n", err)
	return nil
}
