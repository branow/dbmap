package cmd

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/branow/dbmap/internal/cmdutil"
	"github.com/branow/dbmap/internal/config"
	"github.com/branow/dbmap/internal/output"
)

// checks holds the per-key validation a setting cannot do for itself: the
// output format is owned by the renderer, not by the config file.
var checks = map[string]func(string) error{
	"output": func(v string) error {
		_, err := output.ParseFormat(v)
		return err
	},
}

// newConfig exposes the scalar settings of config.yml. The keys are a table in
// internal/config, so get, set and list can never disagree about what exists.
func newConfig(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Read and write the scalar settings of config.yml",
	}
	cmd.AddCommand(newConfigGet(f), newConfigSet(f), newConfigList(f))
	return cmd
}

func newConfigGet(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: "Print one setting's stored value",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			value, err := f.Config.Get(args[0])
			if err != nil {
				return err
			}
			return f.Writer().Value(value)
		},
	}
}

func newConfigSet(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Write one setting",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			key, value := args[0], args[1]
			if check, ok := checks[key]; ok {
				if err := check(value); err != nil {
					return err
				}
			}
			if err := f.Config.Set(key, value); err != nil {
				return err
			}
			if err := f.Config.Save(); err != nil {
				return err
			}
			return f.Note(key + " set")
		},
	}
}

func newConfigList(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List every setting with its stored value",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			records := []output.Record{}
			for _, setting := range config.Settings() {
				value, err := f.Config.Get(setting.Key)
				if err != nil {
					return err
				}
				records = append(records, output.Record{
					{Name: "key", Value: setting.Key},
					{Name: "value", Value: value},
					{Name: "doc", Value: strings.TrimSpace(setting.Doc)},
				})
			}
			return f.Writer().List(records)
		},
	}
}
