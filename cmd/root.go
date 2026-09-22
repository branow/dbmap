// Package cmd is the cobra tree. It parses flags, calls into internal packages
// and renders the result: no business logic lives here, and no command exits
// the process - every one of them returns an error that main translates.
package cmd

import (
	"github.com/spf13/cobra"

	"github.com/branow/dbmap/internal/cmdutil"
	"github.com/branow/dbmap/internal/config"
)

// Build is the version information main stamps into the binary.
type Build struct {
	Version string
	Commit  string
	Date    string
}

// NewRoot assembles the command tree. The persistent flags are resolved once,
// before any command body runs, so a command reads settled values from the
// factory instead of consulting flags, environment and file for itself.
func NewRoot(f *cmdutil.Factory, build Build) *cobra.Command {
	var (
		profile   string
		format    string
		noInput   bool
		quiet     bool
		force     bool
		plaintext bool
	)

	root := &cobra.Command{
		Use:   "dbmap",
		Short: "Index a database into a compact tree an agent reads in one pass",
		Long: "dbmap reads a database catalog and writes a compact index.\n\n" +
			"Connections and llm backends are two independent named namespaces; a " +
			"profile binds one of each. Secrets never enter the config file.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	flags := root.PersistentFlags()
	flags.StringVar(&profile, "profile", "", "profile to use")
	flags.StringVarP(&format, "output", "o", "", "output format: table or json")
	flags.BoolVar(&noInput, "no-input", false, "never prompt; a missing value is an error")
	flags.BoolVar(&quiet, "quiet", false, "suppress status messages")
	flags.BoolVar(&force, "force", false, "proceed when a command would otherwise refuse")
	flags.BoolVar(&plaintext, "allow-plaintext", false,
		"allow a 0600 file when the keychain is unavailable")

	root.PersistentPreRunE = func(*cobra.Command, []string) error {
		overrides := config.Overrides{Profile: profile, Output: format}
		if flags.Changed("no-input") {
			overrides.NoInput = &noInput
		}
		if flags.Changed("quiet") {
			overrides.Quiet = &quiet
		}
		if flags.Changed("allow-plaintext") {
			overrides.Plain = &plaintext
		}
		if err := f.Resolve(overrides); err != nil {
			return err
		}
		// Force describes one invocation only: it has no env or file layer.
		f.Flags.Force = force
		if f.Store == nil {
			store, err := openStore(f.Config, overrides)
			if err != nil {
				return err
			}
			f.Store = store
		}
		return nil
	}

	root.CompletionOptions.DisableDefaultCmd = true
	root.AddCommand(
		newIndex(f),
		newDoctor(f),
		newConnection(f),
		newBackend(f),
		newProfile(f),
		newConfig(f),
		newCompletion(f),
		newVersion(f, build),
	)
	root.SetOut(f.IO.Out)
	root.SetErr(f.IO.ErrOut)
	root.SetIn(f.IO.In)
	return root
}
