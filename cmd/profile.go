package cmd

import (
	"github.com/spf13/cobra"

	"github.com/branow/dbmap/internal/cmdutil"
	"github.com/branow/dbmap/internal/config"
	"github.com/branow/dbmap/internal/output"
)

// newProfile builds the profile namespace. Creating a profile does not make it
// current; switching does.
func newProfile(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profile",
		Short: "Bind a connection and a backend under one name",
	}
	cmd.AddCommand(
		newProfileCreate(f),
		newProfileList(f),
		newProfileSwitch(f),
		newProfileShow(f),
		newProfileRemove(f),
	)
	return cmd
}

func newProfileCreate(f *cmdutil.Factory) *cobra.Command {
	var entry config.Profile
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a profile without activating it",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := ask(f, &entry.Connection, "--connection", "connection", "",
				true); err != nil {
				return err
			}
			if err := ask(f, &entry.Backend, "--backend", "backend", "", true); err != nil {
				return err
			}
			if entry.Output != "" {
				if _, err := output.ParseFormat(entry.Output); err != nil {
					return err
				}
			}
			if err := f.Config.SetProfile(args[0], entry); err != nil {
				return err
			}
			if err := f.Config.Save(); err != nil {
				return err
			}
			return f.Note("profile " + args[0] + " created; make it current with " +
				"`dbmap profile switch " + args[0] + "`")
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&entry.Connection, "connection", "", "connection this profile binds")
	flags.StringVar(&entry.Backend, "backend", "", "backend this profile binds")
	flags.StringVar(&entry.Output, "output", "", "preferred output format for this profile")
	return cmd
}

func newProfileList(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List profiles and which one is current",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			records := []output.Record{}
			for _, name := range f.Config.ProfileNames() {
				entry := f.Config.Profiles[name]
				records = append(records, output.Record{
					{Name: "name", Value: name},
					{Name: "connection", Value: entry.Connection},
					{Name: "backend", Value: entry.Backend},
					{Name: "output", Value: entry.Output},
					{Name: "current", Value: name == f.Config.CurrentProfile},
				})
			}
			return f.Writer().List(records)
		},
	}
}

func newProfileSwitch(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "switch <name>",
		Short: "Make a profile current",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := f.Config.Switch(args[0]); err != nil {
				return err
			}
			if err := f.Config.Save(); err != nil {
				return err
			}
			return f.Note("profile " + args[0] + " is current")
		},
	}
}

func newProfileShow(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "show [name]",
		Short: "Show a profile and what it resolves to",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			name := f.Flags.Profile
			if len(args) == 1 {
				name = args[0]
			}
			if name == "" {
				return &cmdutil.NotConfiguredError{
					What: "current profile",
					Fix:  "create one with `dbmap profile create`, then `dbmap profile switch`",
				}
			}
			entry, err := f.Config.Profile(name)
			if err != nil {
				return err
			}
			connection, err := f.Config.Connection(entry.Connection)
			if err != nil {
				return err
			}
			backend, err := f.Config.Backend(entry.Backend)
			if err != nil {
				return err
			}
			return f.Writer().Show(output.Record{
				{Name: "name", Value: name},
				{Name: "current", Value: name == f.Config.CurrentProfile},
				{Name: "connection", Value: entry.Connection},
				{Name: "engine", Value: string(connection.Engine)},
				{Name: "host", Value: connection.Host},
				{Name: "database", Value: connection.Database},
				{Name: "backend", Value: entry.Backend},
				{Name: "provider", Value: string(backend.Provider)},
				{Name: "model", Value: backend.Model},
				{Name: "output", Value: string(f.Flags.Output)},
			})
		},
	}
}

func newProfileRemove(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := f.Config.RemoveProfile(args[0]); err != nil {
				return err
			}
			if err := f.Config.Save(); err != nil {
				return err
			}
			return f.Note("profile " + args[0] + " removed")
		},
	}
}
