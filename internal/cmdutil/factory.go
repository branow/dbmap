// Package cmdutil holds what every command needs and nothing a command does:
// the Factory that carries its dependencies, the typed error vocabulary, and
// the single translation from an error to a process exit code.
package cmdutil

import (
	"context"

	"github.com/branow/dbmap/internal/config"
	"github.com/branow/dbmap/internal/credentials"
	"github.com/branow/dbmap/internal/iostreams"
	"github.com/branow/dbmap/internal/output"
)

// Flags are the persistent flags after the whole precedence chain has run.
// Commands read the resolved values and never re-derive them.
type Flags struct {
	Profile string
	Output  output.Format
	NoInput bool
	Quiet   bool
	Force   bool
}

// ConnectionProbe verifies a connection before its settings are stored. It is
// the "verify before store" seam: nil means no verification, which is what M1
// ships; the engine milestone supplies the real probe without changing any
// command's surface.
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
// so there are no globals: a test builds one with buffer streams, an in-memory
// config and a fake secret store, and the command under test cannot tell.
type Factory struct {
	IO     *iostreams.IOStreams
	Config *config.Config
	Store  credentials.Store
	Flags  Flags
	Probes Probes
}

// Writer returns the output writer for the resolved format.
func (f *Factory) Writer() output.Writer { return output.New(f.Flags.Output, f.IO.Out) }

// Note writes a status line unless the run is quiet. Status lines go to the
// writer, so a machine-readable format drops them.
func (f *Factory) Note(text string) error {
	if f.Flags.Quiet {
		return nil
	}
	return f.Writer().Note(text)
}

// Resolve runs the precedence chain once and fills the resolved flags, so every
// command sees the same answer. It is called from the root command's
// pre-run, before any command body.
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
