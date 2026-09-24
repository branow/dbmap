package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"github.com/branow/dbmap/internal/cmdutil"
	"github.com/branow/dbmap/internal/config"
	"github.com/branow/dbmap/internal/connect"
	"github.com/branow/dbmap/internal/credentials"
	"github.com/branow/dbmap/internal/index"
	"github.com/branow/dbmap/internal/iostreams"
	"github.com/branow/dbmap/internal/output"
	"github.com/branow/dbmap/internal/plan"
	"github.com/branow/dbmap/llm"
	"github.com/branow/dbmap/llm/provider"
)

// DefaultOut is where the index tree lands when --out is not given: beside the
// project it describes, so the index travels with the checkout that needs it.
const DefaultOut = ".dbmap"

// Concurrency bounds how many model calls may be in flight, and how many
// describe batches the build runs at once. Batches are independent and the
// describe stage is the long pole of a run, so this is a target rather than
// only a ceiling. Four is deliberately modest: the cap that matters is the
// provider's rate limit, not this machine.
const Concurrency = 4

// indexOptions is the flag surface of `dbmap index`. --force is a persistent
// root flag, so it is read off the factory rather than declared again here.
type indexOptions struct {
	database string
	match    string
	limit    int
	samples  int
	out      string
	backend  string
	dryRun   bool
}

// newIndex builds the command that produces the index. It only resolves and
// reports: the stage order lives in internal/index.
func newIndex(f *cmdutil.Factory) *cobra.Command {
	opts := indexOptions{samples: -1}
	cmd := &cobra.Command{
		Use:   "index [connection]",
		Short: "Build the index for one database",
		Long: "Reads a database catalog and writes a compact TSV tree an agent reads " +
			"in one pass.\n\n" +
			"Staleness is decided in two tiers. The engine's modify signal decides " +
			"what to fetch, which is cheap; the content fingerprint of what came " +
			"back decides what to describe, which is a model call. A release that " +
			"touches fifty procedures therefore fetches fifty bodies and describes " +
			"only the ones whose text actually changed.\n\n" +
			"With no connection named, the active profile's connection is used. " +
			"--match and --limit write a PARTIAL index, covering only what they " +
			"selected. The persistent --force flag rebuilds everything, ignoring " +
			"both tiers.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return runIndex(c.Context(), f, arg(args), &opts)
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&opts.database, "db", "", "database to index; defaults to the connection's")
	flags.StringVar(&opts.match, "match", "", "only objects whose name contains this substring")
	flags.IntVar(&opts.limit, "limit", 0, "process at most this many objects; 0 means all")
	flags.IntVar(&opts.samples, "samples", -1,
		"tables to sample: 0 for none, -1 for every table being described")
	flags.StringVar(&opts.out, "out", DefaultOut, "directory the index tree is written under")
	flags.StringVar(&opts.backend, "backend", "", "llm backend to describe with")
	flags.BoolVar(&opts.dryRun, "dry-run", false,
		"report the plan; send no query beyond the manifest and make no model call")
	return cmd
}

// arg is the connection named on the command line, or empty for the profile's.
func arg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

// runIndex resolves what the pipeline needs, runs it, and reports what it did.
// A dry run opens no pool for anything but the manifest, which is what makes it
// safe rather than merely quiet.
func runIndex(ctx context.Context, f *cmdutil.Factory, connection string,
	opts *indexOptions) error {
	connection, entry, err := resolveConnection(f, connection)
	if err != nil {
		return err
	}
	database, err := resolveDatabase(entry, opts.database)
	if err != nil {
		return err
	}
	// The resolved name is what gets opened, not just what the tree is called.
	// entry is a copy, so the stored connection is untouched.
	entry.Database = database

	// Prove the destination is writable before a single query or model call: an
	// unwritable --out discovered after the describe stage throws away work that
	// was already paid for.
	if err := writable(opts.out); err != nil {
		return err
	}

	secret, err := dbSecret(f, connection, entry)
	if err != nil {
		return err
	}
	pool, err := connect.Open(connection, entry, secret.Reveal())
	if err != nil {
		return err
	}
	// Belt for the paths that return before the pipeline closes the source.
	defer pool.Close()

	source, err := index.Open(pool)
	if err != nil {
		return err
	}

	cache, err := cacheRoot()
	if err != nil {
		return err
	}
	client, model, spend, err := describer(f, opts, cache)
	if err != nil {
		return err
	}

	summary, err := index.Build(ctx, source, client, index.Options{
		Environment: connection,
		Database:    database,
		Out:         opts.out,
		Cache:       cache,
		Match:       opts.match,
		Limit:       opts.limit,
		Samples:     opts.samples,
		Model:       model,
		Workers:     Concurrency,
		Spend:       spend,
		Force:       f.Flags.Force,
		DryRun:      opts.dryRun,
		Logger:      newProgress(f.IO, f.Flags.Quiet),
	})
	if err != nil {
		return err
	}
	if err := report(f, summary); err != nil {
		return err
	}
	return describeOutcome(summary)
}

// describeOutcome fails a run whose descriptions did not happen. The index is
// still written — a partial index beats none — but a tree whose description
// column is empty is not a success, and reporting one to CI is how a broken
// backend goes unnoticed for a week.
//
// A failed batch is not the only way to get there. A backend that answers every
// call and names nothing it was asked about leaves the same empty column with
// no failure to show for it, so objects left without a sentence fail the run on
// their own.
func describeOutcome(summary index.Summary) error {
	if summary.DryRun {
		return nil
	}
	if len(summary.Failed) == 0 && len(summary.Missing) == 0 {
		return nil
	}
	return &cmdutil.UnavailableError{
		Subject: "the describe stage",
		Err: fmt.Errorf("%d batches failed and %d objects have no description",
			len(summary.Failed), len(summary.Missing)),
	}
}

// resolveConnection answers which connection this build reads: the argument
// first, the active profile second.
func resolveConnection(f *cmdutil.Factory, named string) (string, config.Connection, error) {
	if named == "" {
		_, profile, err := f.Config.Active(config.Overrides{Profile: f.Flags.Profile})
		if err != nil {
			// Only a profile that was actually named is a not-found; an empty
			// name means nothing is configured yet, which needs the fuller hint.
			var missing *config.NotFoundError
			if errors.As(err, &missing) && missing.Kind == config.KindProfile &&
				missing.Name != "" {
				return "", config.Connection{}, err
			}
			return "", config.Connection{}, &cmdutil.NotConfiguredError{
				What: "a connection",
				Fix:  "name one, or run dbmap profile switch <name>",
			}
		}
		named = profile.Connection
	}
	entry, err := f.Config.Connection(named)
	if err != nil {
		return "", config.Connection{}, err
	}
	return named, entry, nil
}

// resolveDatabase settles which database is indexed. The name becomes a
// directory in the index tree, so it must be a single path segment.
// resolveDatabase picks the database to open: --db when given, else the
// connection's own. The name is also the tree's directory, so it must be a
// plain name rather than a path.
func resolveDatabase(entry config.Connection, override string) (string, error) {
	database := override
	if database == "" {
		database = entry.Database
	}
	if database == "" {
		return "", &cmdutil.ValidationError{Field: "--db", Reason: "required; " +
			"this connection names no default database"}
	}
	if strings.ContainsAny(database, `/\`) || database == "." || database == ".." {
		return "", &cmdutil.ValidationError{Field: "--db", Value: database,
			Reason: "must be a plain database name"}
	}
	return database, nil
}

// dbSecret fetches the connection's password. An auth mode that carries none -
// Kerberos, where the ticket cache is the credential - is asked for nothing.
func dbSecret(f *cmdutil.Factory, connection string,
	entry config.Connection) (credentials.Secret, error) {
	if !config.NeedsPassword(entry.Auth) {
		return credentials.Secret{}, nil
	}
	return f.Store.Get(credentials.DBKey(connection))
}

// describer assembles the model client for this build behind the llm module's
// own middleware, and returns the total it meters into. A dry run is the only
// build allowed to run without a client.
//
// The order of the wrappers is the whole point. The meter goes INNERMOST, so it
// sees one call per request that actually reached the provider: retries count,
// because a retried call is billed, and a response replayed from the disk cache
// does not, because it was paid for on an earlier run. Metering outside the
// cache is how a resumed build reports money it did not spend.
func describer(f *cmdutil.Factory, opts *indexOptions,
	cache string) (llm.Client, string, *llm.Usage, error) {
	if opts.dryRun {
		return nil, "", nil, nil
	}
	named, entry, err := resolveBackend(f, opts.backend)
	if err != nil {
		return nil, "", nil, err
	}

	key := credentials.Secret{}
	if config.NeedsAPIKey(entry.Provider) {
		key, err = f.Store.Get(credentials.LLMKey(named))
		if err != nil {
			return nil, "", nil, err
		}
	}

	client, err := provider.New(llm.Config{
		Provider: string(entry.Provider),
		Model:    entry.Model,
		BaseURL:  entry.BaseURL,
		APIKey:   key.Reveal(),
	})
	if err != nil {
		return nil, "", nil, err
	}

	spend := &llm.Usage{}
	client = llm.WithUsage(client, spend)
	client = llm.WithRetry(client, llm.RetryConfig{})
	client = llm.WithCache(client, filepath.Join(cache, "llm"))
	return llm.WithConcurrency(client, Concurrency), entry.Model, spend, nil
}

// resolveBackend answers which backend describes: --backend first, the active
// profile second.
func resolveBackend(f *cmdutil.Factory, named string) (string, config.Backend, error) {
	if named == "" {
		_, profile, err := f.Config.Active(config.Overrides{Profile: f.Flags.Profile})
		if err != nil {
			// Only a profile that was actually named is a not-found; an empty
			// name means nothing is configured yet, which needs the fuller hint.
			var missing *config.NotFoundError
			if errors.As(err, &missing) && missing.Kind == config.KindProfile &&
				missing.Name != "" {
				return "", config.Backend{}, err
			}
			return "", config.Backend{}, &cmdutil.NotConfiguredError{
				What: "an llm backend",
				Fix:  "pass --backend, or run dbmap profile switch <name>",
			}
		}
		named = profile.Backend
	}
	entry, err := f.Config.Backend(named)
	if err != nil {
		return "", config.Backend{}, err
	}
	return named, entry, nil
}

// cacheRoot is where the resumable fetch cache lives, deliberately outside the
// index tree: that tree is the document an agent reads.
func cacheRoot() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", &cmdutil.UnavailableError{Subject: "the user cache directory", Err: err}
	}
	return filepath.Join(dir, "dbmap"), nil
}

// progress writes a build's stage lines to the error stream, keeping them out
// of the document the output writer produces - so `-o json` still pipes cleanly
// while a person watching the terminal sees the work.
//
// The lock is not decoration: describe batches run concurrently, so this is
// called from several goroutines at once, and an unsynchronised writer
// interleaves half-lines on a terminal and races on a buffer in a test.
type progress struct {
	io    *iostreams.IOStreams
	quiet bool
	mu    *sync.Mutex
}

func newProgress(io *iostreams.IOStreams, quiet bool) progress {
	return progress{io: io, quiet: quiet, mu: &sync.Mutex{}}
}

func (p progress) Info(message string) { p.write(message) }

func (p progress) Warn(message string) { p.write("warning: " + message) }

func (p progress) write(message string) {
	if p.quiet {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	_, _ = p.io.ErrOut.Write([]byte(message + "\n"))
}

// report renders the summary, marking a dry run as one so its plan cannot be
// mistaken for a completed build.
func report(f *cmdutil.Factory, summary index.Summary) error {
	record := output.Record{
		{Name: "connection", Value: summary.Environment},
		{Name: "database", Value: summary.Database},
		{Name: "out", Value: summary.Dir},
		{Name: "objects", Value: summary.Objects},
	}
	if summary.DryRun {
		// A dry run reads the manifest and stops, so it knows exactly what the
		// modify signal says. It does NOT know how many objects will be
		// described: that is decided by a fingerprint of content it has not
		// fetched. Reporting a describe count here would be a guess printed in
		// the same column a real run fills with a fact.
		record = append(record,
			output.Field{Name: "dry-run", Value: true},
			output.Field{Name: "refetch", Value: summary.Refetch},
			output.Field{Name: "untouched", Value: summary.Untouched},
			output.Field{Name: "dropped", Value: len(summary.Dropped)},
			output.Field{Name: "reasons", Value: reasons(summary.Reasons)},
			output.Field{Name: "describe", Value: pending(summary)},
		)
		return f.Writer().Show(record)
	}
	record = append(record,
		output.Field{Name: "fetched", Value: summary.Fetched},
		output.Field{Name: "reused", Value: summary.Reused},
		output.Field{Name: "dropped", Value: len(summary.Dropped)},
		output.Field{Name: "reasons", Value: reasons(summary.Reasons)},
		output.Field{Name: "described", Value: summary.Described},
		output.Field{Name: "unchanged", Value: summary.Unchanged},
		output.Field{Name: "sampled", Value: summary.Sampled},
		output.Field{Name: "column files", Value: summary.ColumnFiles},
		output.Field{Name: "body files", Value: summary.BodyFiles},
		output.Field{Name: "missing", Value: len(summary.Missing)},
		output.Field{Name: "failed batches", Value: len(summary.Failed)},
		output.Field{Name: "redactions", Value: redactions(summary)},
		output.Field{Name: "input tokens", Value: summary.Usage.InputTokens},
		output.Field{Name: "output tokens", Value: summary.Usage.OutputTokens},
		output.Field{Name: "cost", Value: summary.Usage.Cost},
	)
	return f.Writer().Show(record)
}

// pending is what a dry run can honestly say about the describe stage: an
// upper bound, because only an object that is refetched can be redescribed, and
// no lower bound at all.
func pending(summary index.Summary) string {
	if summary.Refetch == 0 {
		return "none; nothing is being refetched"
	}
	return "at most " + strconv.Itoa(summary.Refetch) +
		", decided by content fingerprint after the fetch"
}

// reasons renders the fetch tally in the planner's order, so two builds report
// the same thing the same way.
func reasons(counted map[plan.Reason]int) string {
	var parts []string
	for _, rule := range plan.FetchReasons {
		if n := counted[rule.Reason]; n > 0 {
			parts = append(parts, string(rule.Reason)+" "+strconv.Itoa(n))
		}
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

// redactions names what was stripped out of module bodies.
func redactions(summary index.Summary) string {
	if summary.Redactions.Total() == 0 {
		return "none"
	}
	var parts []string
	for _, class := range summary.Redactions.Classes() {
		parts = append(parts, string(class)+" "+strconv.Itoa(summary.Redactions[class]))
	}
	return strings.Join(parts, ", ")
}

// writable reports whether the index tree can be written where --out points.
func writable(dir string) error {
	if dir == "" {
		dir = DefaultOut
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return &cmdutil.ValidationError{Field: "--out", Value: dir,
			Reason: "cannot be written: " + err.Error()}
	}
	probe, err := os.CreateTemp(dir, ".dbmap-write-*")
	if err != nil {
		return &cmdutil.ValidationError{Field: "--out", Value: dir,
			Reason: "cannot be written: " + err.Error()}
	}
	name := probe.Name()
	probe.Close()
	return os.Remove(name)
}
