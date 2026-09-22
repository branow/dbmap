package cmd

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/branow/dbmap/internal/cmdutil"
	"github.com/branow/dbmap/internal/config"
	"github.com/branow/dbmap/internal/credentials"
	"github.com/branow/dbmap/internal/output"
)

// newConnection builds the database connection namespace. Defining a
// connection never activates it: `dbmap profile switch` does that.
func newConnection(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "connection",
		Short: "Manage named database connections",
		Long: "A connection holds non-secret settings only. Its password lives in " +
			"the OS keychain under db:<name> and never enters the config file.",
	}
	cmd.AddCommand(
		newConnectionAdd(f),
		newConnectionList(f),
		newConnectionShow(f),
		newConnectionRemove(f),
	)
	return cmd
}

// connectionOptions is the flag surface of `connection add`. Every value is a
// flag so a CI run needs no terminal, and every one is prompted for when it is
// missing and a terminal is there.
type connectionOptions struct {
	engine     string
	host       string
	port       int
	database   string
	auth       string
	username   string
	params     map[string]string
	production bool
	stdin      bool
	noVerify   bool
}

func newConnectionAdd(f *cmdutil.Factory) *cobra.Command {
	opts := connectionOptions{params: map[string]string{}}
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Define a database connection",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return addConnection(c, f, args[0], &opts)
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&opts.engine, "engine", "", "engine: "+strings.Join(config.Engines(), " or "))
	flags.StringVar(&opts.host, "host", "", "host name")
	flags.IntVar(&opts.port, "port", 0, "port; zero means the driver default")
	flags.StringVar(&opts.database, "database", "", "default database")
	flags.StringVar(&opts.auth, "auth", "", "authentication mode")
	flags.StringVar(&opts.username, "username", "", "login name")
	flags.StringToStringVar(&opts.params, "param", nil, "extra driver parameter, repeatable")
	flags.BoolVar(&opts.production, "production", false,
		"mark as production; dbmap refuses to index it")
	flags.BoolVar(&opts.stdin, "password-stdin", false, "read the password from stdin")
	flags.BoolVar(&opts.noVerify, "no-verify", false, "store without probing the connection")
	return cmd
}

// addConnection collects the settings, verifies before it stores anything, then
// writes the secret to the keychain and the rest to the config file.
func addConnection(c *cobra.Command, f *cmdutil.Factory, name string,
	opts *connectionOptions) error {
	if err := ask(f, &opts.engine, "--engine", "engine", string(config.SQLServer),
		true); err != nil {
		return err
	}
	engine, err := config.ParseEngine(opts.engine)
	if err != nil {
		return err
	}
	if err := ask(f, &opts.host, "--host", "host", "", true); err != nil {
		return err
	}
	if err := ask(f, &opts.database, "--database", "database", "", false); err != nil {
		return err
	}
	allowed := config.Auths(engine)
	if err := ask(f, &opts.auth, "--auth", "auth ("+strings.Join(allowed, ", ")+")",
		allowed[0], true); err != nil {
		return err
	}
	auth, err := config.ParseAuth(engine, opts.auth)
	if err != nil {
		return err
	}
	if config.NeedsUsername(auth) {
		if err := ask(f, &opts.username, "--username", "username", "", true); err != nil {
			return err
		}
	}
	if !c.Flags().Changed("production") {
		marked, err := confirm(f, "is this a production database", false)
		if err != nil {
			return err
		}
		opts.production = marked
	}

	entry := config.Connection{
		Engine:     engine,
		Host:       opts.host,
		Port:       opts.port,
		Database:   opts.database,
		Auth:       auth,
		Username:   opts.username,
		Params:     opts.params,
		Production: opts.production,
	}
	key := credentials.DBKey(name)
	secret, from, err := readSecret(f, opts.stdin, key, "password", "--password-stdin",
		config.NeedsPassword(auth))
	if err != nil {
		return err
	}
	var probe func() error
	if f.Probes.Connection != nil {
		probe = func() error { return f.Probes.Connection(c.Context(), name, entry, secret) }
	}
	if err := verify(f, opts.noVerify, probe); err != nil {
		return err
	}
	if err := remember(f, key, secret, from); err != nil {
		return err
	}
	if err := f.Config.SetConnection(name, entry); err != nil {
		return err
	}
	if err := f.Config.Save(); err != nil {
		return err
	}
	return f.Note("connection " + name + " defined; activate it with a profile")
}

func newConnectionList(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List defined connections",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			records := []output.Record{}
			for _, name := range f.Config.ConnectionNames() {
				entry := f.Config.Connections[name]
				records = append(records, output.Record{
					{Name: "name", Value: name},
					{Name: "engine", Value: string(entry.Engine)},
					{Name: "host", Value: entry.Host},
					{Name: "database", Value: entry.Database},
					{Name: "auth", Value: string(entry.Auth)},
					{Name: "production", Value: entry.Production},
				})
			}
			return f.Writer().List(records)
		},
	}
}

func newConnectionShow(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Show one connection's settings",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			entry, err := f.Config.Connection(args[0])
			if err != nil {
				return err
			}
			return f.Writer().Show(output.Record{
				{Name: "name", Value: args[0]},
				{Name: "engine", Value: string(entry.Engine)},
				{Name: "host", Value: entry.Host},
				{Name: "port", Value: entry.Port},
				{Name: "database", Value: entry.Database},
				{Name: "auth", Value: string(entry.Auth)},
				{Name: "username", Value: entry.Username},
				{Name: "params", Value: entry.Params},
				{Name: "production", Value: entry.Production},
			})
		},
	}
}

func newConnectionRemove(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a connection and its stored password",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			name := args[0]
			if err := f.Config.RemoveConnection(name, f.Flags.Force); err != nil {
				return err
			}
			if err := forget(f, credentials.DBKey(name)); err != nil {
				return err
			}
			if err := f.Config.Save(); err != nil {
				return err
			}
			return f.Note("connection " + name + " removed")
		},
	}
}
