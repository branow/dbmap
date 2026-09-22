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

// readSecret obtains a credential: from stdin for a scripted run, from a hidden
// prompt for a person, and from neither when the entry needs no secret. The
// value is returned, never echoed and never stored in the config file.
func readSecret(f *cmdutil.Factory, fromStdin bool, key, label, flag string,
	required bool) (credentials.Secret, error) {
	if fromStdin {
		raw, err := f.IO.ReadAll()
		if err != nil {
			return credentials.Secret{}, err
		}
		if raw == "" && required {
			return credentials.Secret{}, &cmdutil.ValidationError{
				Field: flag, Reason: "no value arrived on stdin"}
		}
		return credentials.NewSecret(raw), nil
	}
	if !required {
		return credentials.Secret{}, nil
	}
	if !f.IO.CanPrompt() {
		return credentials.Secret{}, &cmdutil.ValidationError{
			Field:  label,
			Reason: fmt.Sprintf("required; use %s or set %s", flag, credentials.NewEnv(nil).Name(key)),
		}
	}
	raw, err := f.IO.PromptPassword(label)
	if err != nil {
		return credentials.Secret{}, err
	}
	if raw == "" {
		return credentials.Secret{}, &cmdutil.ValidationError{Field: label, Reason: "required"}
	}
	return credentials.NewSecret(raw), nil
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
