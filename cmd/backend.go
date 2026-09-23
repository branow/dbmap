package cmd

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/branow/dbmap/internal/cmdutil"
	"github.com/branow/dbmap/internal/config"
	"github.com/branow/dbmap/internal/credentials"
	"github.com/branow/dbmap/internal/output"
)

// newBackend builds the llm backend namespace, independent of the connection
// namespace on purpose: one api key serves every database.
func newBackend(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backend",
		Short: "Manage named llm backends",
		Long: "A backend holds non-secret settings only. Its api key lives in the OS " +
			"keychain under llm:<name> and never enters the config file.",
	}
	cmd.AddCommand(
		newBackendAdd(f),
		newBackendList(f),
		newBackendShow(f),
		newBackendRemove(f),
	)
	return cmd
}

// backendOptions is the flag surface of `backend add`.
type backendOptions struct {
	provider string
	model    string
	baseURL  string
	stdin    bool
	noVerify bool
}

func newBackendAdd(f *cmdutil.Factory) *cobra.Command {
	var opts backendOptions
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Define an llm backend",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return addBackend(c, f, args[0], &opts)
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&opts.provider, "provider", "",
		"provider: "+strings.Join(config.Providers(), ", "))
	flags.StringVar(&opts.model, "model", "", "model identifier")
	flags.StringVar(&opts.baseURL, "base-url", "", "api base url")
	flags.BoolVar(&opts.stdin, "api-key-stdin", false, "read the api key from stdin")
	flags.BoolVar(&opts.noVerify, "no-verify", false, "store without probing the backend")
	return cmd
}

// addBackend collects the settings, verifies before it stores anything, then
// writes the api key to the keychain and the rest to the config file.
func addBackend(c *cobra.Command, f *cmdutil.Factory, name string, opts *backendOptions) error {
	if err := ask(f, &opts.provider, "--provider", "provider", string(config.Anthropic),
		true); err != nil {
		return err
	}
	provider, err := config.ParseProvider(opts.provider)
	if err != nil {
		return err
	}
	if err := ask(f, &opts.model, "--model", "model", "", false); err != nil {
		return err
	}
	if err := ask(f, &opts.baseURL, "--base-url", "base url", "", false); err != nil {
		return err
	}

	entry := config.Backend{Provider: provider, Model: opts.model, BaseURL: opts.baseURL}
	key := credentials.LLMKey(name)
	secret, from, err := readSecret(f, opts.stdin, key, "api key", "--api-key-stdin",
		config.NeedsAPIKey(provider))
	if err != nil {
		return err
	}
	var probe func() error
	if f.Probes.Backend != nil {
		probe = func() error { return f.Probes.Backend(c.Context(), name, entry, secret) }
	}
	if err := verify(f, opts.noVerify, probe); err != nil {
		return err
	}
	if err := remember(f, key, secret, from); err != nil {
		return err
	}
	if err := f.Config.SetBackend(name, entry); err != nil {
		return err
	}
	if err := f.Config.Save(); err != nil {
		return err
	}
	return f.Note("backend " + name + " defined; activate it with a profile")
}

func newBackendList(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List defined backends",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			records := []output.Record{}
			for _, name := range f.Config.BackendNames() {
				entry := f.Config.Backends[name]
				records = append(records, output.Record{
					{Name: "name", Value: name},
					{Name: "provider", Value: string(entry.Provider)},
					{Name: "model", Value: entry.Model},
					{Name: "base_url", Value: entry.BaseURL},
				})
			}
			return f.Writer().List(records)
		},
	}
}

func newBackendShow(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Show one backend's settings",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			entry, err := f.Config.Backend(args[0])
			if err != nil {
				return err
			}
			return f.Writer().Show(output.Record{
				{Name: "name", Value: args[0]},
				{Name: "provider", Value: string(entry.Provider)},
				{Name: "model", Value: entry.Model},
				{Name: "base_url", Value: entry.BaseURL},
			})
		},
	}
}

func newBackendRemove(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a backend and its stored api key",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			name := args[0]
			if err := f.Config.RemoveBackend(name, f.Flags.Force); err != nil {
				return err
			}
			if err := forget(f, credentials.LLMKey(name)); err != nil {
				return err
			}
			if err := f.Config.Save(); err != nil {
				return err
			}
			return f.Note("backend " + name + " removed")
		},
	}
}
