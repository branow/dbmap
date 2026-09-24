// Package index is the build pipeline: the stage order that turns one live
// database into the index tree an agent reads, and the only place the whole
// tool is assembled. See docs/DESIGN.md for the stage order and why.
package index

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/branow/dbmap/internal/cache"
	"github.com/branow/dbmap/internal/catalog"
	"github.com/branow/dbmap/internal/describe"
	"github.com/branow/dbmap/internal/plan"
	"github.com/branow/dbmap/internal/redact"
	"github.com/branow/dbmap/internal/render"
	"github.com/branow/dbmap/internal/sample"
	"github.com/branow/dbmap/llm"
)

// Options configure one build. Every field is resolved by the caller: this
// package reads no flag, environment variable or config file.
type Options struct {
	// Environment scopes both the index tree and the fetch cache.
	Environment string
	Database    string
	// Out is the index root; this build lands in <Out>/<Environment>/<Database>.
	Out string
	// Cache is the fetch cache root, or empty for a build that caches nothing.
	Cache string
	// Match narrows the build to keys containing this substring, case-
	// insensitively. A narrowed build writes a PARTIAL index.
	Match string
	// Limit caps how many objects are processed; zero means every one.
	Limit int
	// Samples caps how many tables are sampled: negative means every table
	// being described, zero none at all.
	Samples int
	// Model overrides the backend's default model for this build.
	Model string
	// Force ignores both staleness tiers, so everything fetches and describes.
	Force bool
	// DryRun reports the plan and stops, writing and sending nothing.
	DryRun bool
	Logger Logger
}

// sampleLimit maps the cap onto the sampler's convention, where zero means no
// cap; the build skips the stage outright for Samples == 0.
func (o Options) sampleLimit() int {
	if o.Samples < 0 {
		return 0
	}
	return o.Samples
}

// Summary is what a build did, as values rather than rendered text.
type Summary struct {
	Environment string
	Database    string
	// Dir is the tree this build wrote, or would have written.
	Dir    string
	DryRun bool

	// Objects is how many the build covered after Match and Limit.
	Objects int
	// Fetched came from the database this run, Reused from the cache.
	Fetched int
	Reused  int
	// Described got a new sentence, Unchanged kept the previous build's. A kind
	// that is never described counts in neither.
	Described int
	Unchanged int
	Sampled   int
	// Dropped are objects the database no longer has; their rows and detail
	// files are removed.
	Dropped []string
	// Missing are objects the model skipped, reported rather than left blank.
	Missing []string
	// Reasons counts why objects were refetched.
	Reasons map[plan.Reason]int
	// Redactions tallies secrets stripped out of module bodies, per class.
	Redactions redact.Counts
	Usage      llm.Usage
	// Failed are describe batches that errored; skipped, not fatal.
	Failed      []error
	Catalogs    []render.Written
	ColumnFiles int
	BodyFiles   int
}

// Build runs the whole pipeline once. A nil client describes nothing. src is
// released before the describe stage on every path, error paths included.
func Build(ctx context.Context, src Source, client llm.Client, opts Options) (Summary, error) {
	dir := filepath.Join(opts.Out, opts.Environment, opts.Database)
	summary := Summary{
		Environment: opts.Environment,
		Database:    opts.Database,
		Dir:         dir,
		DryRun:      opts.DryRun,
	}
	cached := openCache(opts.Cache, opts.Environment, opts.Database, !opts.DryRun, opts.Logger)

	released := false
	release := func() error {
		if released {
			return nil
		}
		released = true
		return src.Close()
	}
	defer release()

	objects, err := manifest(ctx, src, cached, opts)
	if err != nil {
		return summary, err
	}

	state, err := prior(dir, opts.Force)
	if err != nil {
		return summary, err
	}

	work := plan.Fetch(objects, state)
	selected := narrow(objects, opts.Match, opts.Limit)
	summary.Objects = len(selected)
	summary.Dropped = work.Dropped
	summary.Reasons = reasons(selected, work.Reasons)

	if opts.DryRun {
		summary.Fetched = total(summary.Reasons)
		summary.Reused = len(selected) - summary.Fetched
		return summary, nil
	}

	entries, err := fetch(ctx, src, cached, selected, work.Reasons, &summary, opts)
	if err != nil {
		return summary, err
	}

	split, err := plan.Describe(entries, state)
	if err != nil {
		return summary, err
	}
	summary.Unchanged = len(split.Unchanged)

	todo := plan.Describable(split.Describe)
	sampled, err := samples(ctx, src, todo, opts)
	if err != nil {
		return summary, err
	}
	summary.Sampled = len(sampled)

	// Everything below this line runs with the pool closed.
	if err := release(); err != nil {
		return summary, err
	}

	result, err := describeAll(ctx, client, todo, sampled, opts)
	if err != nil {
		return summary, err
	}
	summary.Described = len(result.Sentences)
	summary.Missing = result.Missing
	summary.Failed = result.Failed
	summary.Usage = result.Usage

	written, err := write(dir, selected, split, result, state)
	if err != nil {
		return summary, err
	}
	summary.Catalogs = written.Catalogs
	summary.ColumnFiles = written.ColumnFiles
	summary.BodyFiles = written.BodyFiles

	if err := render.Remove(dir, work.Dropped); err != nil {
		return summary, err
	}
	return summary, nil
}

// manifest reads the catalog query every later stage versions against, and
// checkpoints it so a killed run can be resumed.
func manifest(ctx context.Context, src Source, cached store,
	opts Options) ([]catalog.Object, error) {
	objects, err := src.Manifest(ctx, src.Conn())
	if err != nil {
		return nil, err
	}
	cached.putManifest(cache.Manifest{
		Environment: opts.Environment,
		Database:    opts.Database,
		FetchedAt:   time.Now(),
		Objects:     objects,
	})
	info(opts.Logger, "manifest: "+plural(len(objects), "object"))
	return objects, nil
}

// prior reads what the last build recorded, out of the catalogs it wrote.
func prior(dir string, force bool) (map[string]catalog.State, error) {
	if force {
		return map[string]catalog.State{}, nil
	}
	return render.ReadState(dir)
}

// narrow applies the partial-build filters, matching case-insensitively on the
// whole key.
func narrow(objects []catalog.Object, match string, limit int) []catalog.Object {
	kept := objects
	if match != "" {
		needle := strings.ToLower(match)
		kept = nil
		for _, object := range objects {
			if strings.Contains(strings.ToLower(object.Key()), needle) {
				kept = append(kept, object)
			}
		}
	}
	if limit > 0 && len(kept) > limit {
		kept = kept[:limit]
	}
	return kept
}

// reasons counts why each SELECTED object is refetched: the planner decides
// over the whole manifest, but a narrowed build reports only its own work.
func reasons(selected []catalog.Object, all map[string]plan.Reason) map[plan.Reason]int {
	counted := map[plan.Reason]int{}
	for _, object := range selected {
		if reason, ok := all[object.Key()]; ok {
			counted[reason]++
		}
	}
	return counted
}

func total(counted map[plan.Reason]int) int {
	sum := 0
	for _, n := range counted {
		sum += n
	}
	return sum
}

// fetch assembles one entry per selected object, cache first. A partial cache
// hit refetches rather than indexing an object incompletely.
func fetch(
	ctx context.Context,
	src Source,
	cached store,
	selected []catalog.Object,
	stale map[string]plan.Reason,
	summary *Summary,
	opts Options,
) ([]catalog.Entry, error) {
	structures := make(map[string]catalog.Structure, len(selected))
	definitions := make(map[string]string, len(selected))
	var missing []catalog.Object

	for _, object := range selected {
		key := object.Key()
		if _, moved := stale[key]; moved {
			missing = append(missing, object)
			continue
		}
		structure, ok := cached.structure(key, object.Modified)
		if !ok {
			missing = append(missing, object)
			continue
		}
		if module(object) {
			body, ok := cached.module(key, object.Modified)
			if !ok {
				missing = append(missing, object)
				continue
			}
			definitions[key] = body.Body
			summary.Redactions = summary.Redactions.Add(body.Redactions)
		}
		structures[key] = structure
	}

	summary.Fetched = len(missing)
	summary.Reused = len(selected) - len(missing)
	info(opts.Logger, "fetch: "+plural(summary.Fetched, "object")+" from the database, "+
		plural(summary.Reused, "object")+" from the cache")

	if len(missing) > 0 {
		if err := pull(ctx, src, cached, missing, structures, definitions, summary); err != nil {
			return nil, err
		}
	}

	entries := make([]catalog.Entry, 0, len(selected))
	for _, object := range selected {
		key := object.Key()
		structure := structures[key]
		if definition, ok := definitions[key]; ok {
			structure.Definition = definition
		}
		entries = append(entries, catalog.Entry{Object: object, Structure: structure})
	}
	return entries, nil
}

// pull reads what the cache could not serve, health-checked before each send.
func pull(
	ctx context.Context,
	src Source,
	cached store,
	missing []catalog.Object,
	structures map[string]catalog.Structure,
	definitions map[string]string,
	summary *Summary,
) error {
	fresh, err := src.Structure(ctx, src.Conn())
	if err != nil {
		return err
	}

	var keys []string
	for _, object := range missing {
		if module(object) {
			keys = append(keys, object.Key())
		}
	}

	bodies := map[string]redact.Body{}
	if len(keys) > 0 {
		bodies, err = src.Modules(ctx, src.Conn(), keys)
		if err != nil {
			return err
		}
	}

	for _, object := range missing {
		key := object.Key()
		structure := fresh[key]
		structures[key] = structure
		cached.putStructure(key, object.Modified, structure)

		body, ok := bodies[key]
		if !ok {
			continue
		}
		definitions[key] = body.String()
		summary.Redactions = summary.Redactions.Add(body.Counts())
		cached.putModule(key, object.Modified, body)
	}
	return nil
}

// module reports whether this object's kind carries a body.
func module(object catalog.Object) bool {
	spec, ok := catalog.Lookup(object.Kind)
	return ok && spec.Module
}

// samples reads rows only for tables about to be described: sampling one whose
// description is already current would read user data for nothing.
func samples(
	ctx context.Context,
	src Source,
	todo []catalog.Entry,
	opts Options,
) (map[string]catalog.Sample, error) {
	if opts.Samples == 0 || len(todo) == 0 {
		return map[string]catalog.Sample{}, nil
	}
	return sample.All(ctx, src, src.Conn(), todo, sample.Options{
		Limit:  opts.sampleLimit(),
		Logger: opts.Logger,
	})
}

// describeAll sends the batches. A build with no client describes nothing.
func describeAll(
	ctx context.Context,
	client llm.Client,
	todo []catalog.Entry,
	samples map[string]catalog.Sample,
	opts Options,
) (describe.Result, error) {
	empty := describe.Result{Sentences: map[string]string{}, Lookups: map[string]bool{}}
	if client == nil || len(todo) == 0 {
		return empty, nil
	}

	inputs := make([]describe.Input, 0, len(todo))
	for _, entry := range todo {
		inputs = append(inputs, describe.Input{
			Entry:      entry,
			Database:   opts.Database,
			Definition: entry.Structure.Definition,
			Sample:     sample.Render(samples[entry.Key()]),
		})
	}
	return describe.All(ctx, client, inputs, describe.Options{
		Model:  opts.Model,
		Logger: opts.Logger,
	})
}

// write renders the tree in manifest order, so two builds produce the same
// files. An object the model skipped keeps its previous sentence.
func write(
	dir string,
	selected []catalog.Object,
	split plan.DescribePlan,
	result describe.Result,
	state map[string]catalog.State,
) (render.Result, error) {
	byKey := make(map[string]catalog.Entry, len(selected))
	for _, entry := range split.Unchanged {
		byKey[entry.Key()] = entry
	}
	for _, entry := range split.Describe {
		key := entry.Key()
		entry.Description = state[key].Description
		if sentence, ok := result.Sentences[key]; ok {
			entry.Description = sentence
		}
		byKey[key] = entry
	}

	entries := make([]catalog.Entry, 0, len(selected))
	for _, object := range selected {
		if entry, ok := byKey[object.Key()]; ok {
			entries = append(entries, entry)
		}
	}
	return render.Write(dir, entries)
}
