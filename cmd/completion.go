package cmd

import (
	"sort"

	"github.com/spf13/cobra"

	"github.com/branow/dbmap/internal/cmdutil"
)

// shells maps a shell name to its generator. It is a table so the argument
// validator and the generator can never list different shells.
var shells = map[string]func(*cobra.Command) error{
	"bash": func(c *cobra.Command) error {
		return c.Root().GenBashCompletionV2(c.OutOrStdout(), true)
	},
	"zsh": func(c *cobra.Command) error { return c.Root().GenZshCompletion(c.OutOrStdout()) },
	"fish": func(c *cobra.Command) error {
		return c.Root().GenFishCompletion(c.OutOrStdout(), true)
	},
	"powershell": func(c *cobra.Command) error {
		return c.Root().GenPowerShellCompletionWithDesc(c.OutOrStdout())
	},
}

// newCompletion writes a shell completion script to stdout. dbmap generates its
// own rather than using cobra's default command, so the script goes to the
// factory's stream like every other output.
func newCompletion(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:       "completion <shell>",
		Short:     "Print a shell completion script",
		Args:      cobra.ExactArgs(1),
		ValidArgs: shellNames(),
		RunE: func(c *cobra.Command, args []string) error {
			generate, ok := shells[args[0]]
			if !ok {
				return &cmdutil.ValidationError{Field: "shell", Value: args[0],
					Allowed: shellNames()}
			}
			c.SetOut(f.IO.Out)
			return generate(c)
		},
	}
}

func shellNames() []string {
	names := make([]string, 0, len(shells))
	for name := range shells {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
