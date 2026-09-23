// Package cmdutil holds what every command needs and nothing a command does:
// the Factory, the typed error vocabulary, and the one translation from an
// error to a process exit code.
package cmdutil

import (
	"context"

	"github.com/branow/dbmap/internal/config"
	"github.com/branow/dbmap/internal/credentials"
	"github.com/branow/dbmap/internal/iostreams"
	"github.com/branow/dbmap/internal/output"
)

// Flags are the persistent flags after the precedence chain has run. Commands
// read the resolved values and never re-derive them.
type Flags struct {
	Profile string
	Output  output.Format
	NoInput bool
	Quiet   bool
	Force   bool
}

// ConnectionProbe verifies a connection before its settings are stored. Nil
// means no verification.
type ConnectionProbe func(ctx context.Context, name string, entry config.Connection,
	secret credentials.Secret) error

// BackendProbe verifies an llm backend before its settings are stored.
type BackendProbe func(ctx context.Context, name string, entry config.Backend,
	secret credentials.Secret) error

// Probes carries the verification seams. A nil member is a no-op.
type Probes struct {
	Connection ConnectionProbe
	Backend    BackendProbe
}

// Factory is the dependency bundle every command constructor takes. It exists
// so there are no globals: a test builds one from buffers and fakes.
type Factory struct {
	IO     *iostreams.IOStreams
	Config *config.Config
	Store  credentials.Store
	Flags  Flags
	Probes Probes
	// Env reads the process environment, as a field so nothing in a test can
	// reach the machine's real variables.
	Env func(string) string
}

// EnvSecret returns the secret the environment already carries for a key: the
// path that lets a headless run take a credential with no keychain at all.
func (f *Factory) EnvSecret(key string) (credentials.Secret, bool) {
	secret, err := credentials.NewEnv(f.Env).Get(key)
	return secret, err == nil
}

// Writer returns the output writer for the resolved format.
func (f *Factory) Writer() output.Writer { return output.New(f.Flags.Output, f.IO.Out) }

// Note writes a status line unless the run is quiet.
func (f *Factory) Note(text string) error {
	if f.Flags.Quiet {
		return nil
	}
	return f.Writer().Note(text)
}

// Resolve runs the precedence chain once, from the root command's pre-run, so
// every command sees the same answer.
func (f *Factory) Resolve(o config.Overrides) error {
	format, err := output.ParseFormat(f.Config.Output(o))
	if err != nil {
		return &ValidationError{Field: "output", Value: f.Config.Output(o),
			Allowed: output.Formats()}
	}
	f.Flags.Profile = f.Config.ProfileName(o)
	f.Flags.Output = format
	f.Flags.NoInput = f.Config.NoInput(o)
	f.Flags.Quiet = f.Config.Quiet(o)
	f.IO.SetNeverPrompt(f.Flags.NoInput)
	return nil
}
