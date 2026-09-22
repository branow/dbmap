// Package index is the build pipeline: the stage order that turns one live
// database into the index tree an agent reads, and the only place the whole
// tool is assembled.
//
// The order is fixed, and every step of it exists because of a measurement in
// DESIGN.md:
//
//	manifest        one cheap catalog query; the checkpoint everything resumes
//	                from, and the source of the modify signal
//	plan.Fetch      what the modify signal says must be pulled again
//	fetch           structure and module bodies, cache first, engine on a miss
//	fingerprint     the exact content hash of what came back
//	plan.Describe   what the hash says must be sent to a model
//	sample          the first rows of the tables being described
//	release         the connection is closed HERE, before any model call
//	describe        batched model calls
//	render          the TSV tree
//
// The two-tier split is the whole point. The modify signal decides whether to
// FETCH and the content hash decides whether to DESCRIBE, so the release that
// ALTERs 58 procedures pulls 58 bodies (seconds) and describes the three that
// actually changed instead of paying for 58 model calls.
//
// Prior state comes out of the catalogs the last build wrote. There is
// deliberately no sidecar state file: a parallel file drifts out of step with
// the index beside it, and the modify signal earns its place in a catalog row
// for a human reader anyway.
package index

import (
	"context"
	"errors"
	"io/fs"
	"os"
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

// columnsDir mirrors the per-object directory render writes into. It is named
// here because removing an object from the index means removing its detail file
// too, and nothing else in the tree is addressed per object.
const columnsDir = "columns"

// Options configure one build. Every field is resolved by the caller: this
// package reads no flag, no environment variable and no config file.
type Options struct {
	// Environment scopes both the index tree and the fetch cache. It is the
	// connection name, because one connection is one deployment of a database
	// and an index of stage must not overwrite an index of dev.
	Environment string
	// Database is the database being indexed, and the only name a prompt is
	// given for context.
	Database string
	// Out is the index root. The tree for this build lands in
	// <Out>/<Environment>/<Database>.
	Out string
	// Cache is the fetch cache root, or empty for a build that caches nothing.
	Cache string
	// Match narrows the build to objects whose key contains this substring,
	// case-insensitively. A narrowed build writes a PARTIAL index: the
	// catalogs are rewritten from what was selected, which is what makes the
	// concept provable against forty objects before anyone waits on 1,237.
	Match string
	// Limit caps how many objects are processed at all. Zero means every one.
	Limit int
	// Samples caps how many tables are sampled: a negative value means every
	// table being described, and zero means none at all. Sampling is the only
	// stage that reads a row of user data, so turning it off is a first-class
	// choice rather than an omission.
	Samples int
	// Model overrides the backend's default model for this build.
	Model string
	// Force rebuilds everything, ignoring both staleness tiers: the prior
	// state is not read, so every object fetches and every object describes.
	Force bool
	// DryRun reports the plan and stops. It sends no query beyond the
	// manifest, makes no model call, and writes nothing anywhere.
	DryRun bool
	Logger Logger
}

// sampleLimit maps the sample cap onto what the sampler understands, where zero
// means no cap. The build skips the stage outright for Samples == 0, so the two
// meanings of zero never meet.
func (o Options) sampleLimit() int {
	if o.Samples < 0 {
		return 0
	}
	return o.Samples
}

// Summary is what a build did, as values. It is a struct rather than a rendered
// string because the shell decides how a result looks and a test asserts on
// numbers, not on wording.
type Summary struct {
	Environment string
	Database    string
	// Dir is the tree this build wrote, or would have written.
	Dir    string
	DryRun bool

	// Objects is how many objects the build covered after Match and Limit.
	Objects int
	// Fetched is how many of them were read from the database this run, and
	// Reused how many the fetch cache could prove current. In a dry run they
	// are what the plan would have done, since nothing is fetched.
	Fetched int
	Reused  int
	// Described is how many objects a model wrote a sentence for, and
	// Unchanged how many kept the sentence the previous build wrote because
	// their content hash had not moved. Unchanged is the tier that pays for
	// itself. A kind that is never described — a synonym, indexed for its
	// target rather than for a sentence — counts in neither.
	Described int
	Unchanged int
	Sampled   int
	// Dropped are objects the previous build indexed that the database no
	// longer has. Their rows and detail files are removed.
	Dropped []string
	// Missing are objects the model skipped. They are reported rather than
	// left silently blank in the index.
	Missing []string
	// Reasons counts why objects were refetched, so a build can say what it is
	// doing and why.
	Reasons map[plan.Reason]int
	// Redactions tallies the secrets stripped out of module bodies, per class.
	// It is surfaced rather than swallowed: a real credential in a procedure
	// body is something the operator has to know about.
	Redactions redact.Counts
	Usage      llm.Usage
	// Failed are describe batches that errored. A failed batch is skipped, not
	// fatal, because a partial index beats none.
	Failed      []error
	Catalogs    []render.Written
	ColumnFiles int
}

// Build runs the whole pipeline once and reports what it did.
//
// client may be nil, which describes nothing — the shape a structure-only build
// takes. src is released before the describe stage in every path, including
// every error path.
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

	// The describe stage never holds a database connection. Everything below
	// this line runs with the pool closed.
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

	if err := remove(dir, work.Dropped); err != nil {
		return summary, err
	}
	return summary, nil
}

// manifest reads the cheap catalog query every later stage versions against,
// and checkpoints it so a killed run can be resumed.
func manifest(ctx context.Context, src Source, cached store,
	opts Options) ([]catalog.Object, error) {
	if err := assert(ctx, src, "the manifest"); err != nil {
		return nil, err
	}
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

// prior reads what the last build recorded, straight out of the catalogs it
// wrote. A forced build reads nothing, which is how --force rebuilds
// everything: with no prior state every object is new to both tiers.
func prior(dir string, force bool) (map[string]catalog.State, error) {
	if force {
		return map[string]catalog.State{}, nil
	}
	return render.ReadState(dir)
}

// narrow applies the partial-build filters. Matching is case-insensitive on the
// whole key, so a schema name narrows as readily as an object name.
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

// reasons counts, over the selected objects only, why each is being refetched.
// The planner decides over the whole manifest; a narrowed build reports on what
// it is actually going to do.
func reasons(selected []catalog.Object, all map[string]plan.Reason) map[plan.Reason]int {
	counted := map[plan.Reason]int{}
	for _, object := range selected {
		if reason, ok := all[object.Key()]; ok {
			counted[reason]++
		}
	}
	return counted
}

// total is how many objects a reason tally covers.
func total(counted map[plan.Reason]int) int {
	sum := 0
	for _, n := range counted {
		sum += n
	}
	return sum
}

// fetch assembles one entry per selected object: the manifest fact plus the
// structure and, for a module kind, the body.
//
// The cache is asked first and the database only for what it cannot prove
// current. A cached entry is keyed by the modify signal, so an object the
// planner called stale is a miss by construction — and an object the planner
// called current but the cache has never seen is refetched too, because a cold
// cache is a reason to read cheaply, never a reason to index an object
// incompletely.
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

// pull reads what the cache could not serve: one bounded structure fetch for
// the database, then the bodies of the module kinds among the misses. Both
// stages are health-checked before they send anything.
func pull(
	ctx context.Context,
	src Source,
	cached store,
	missing []catalog.Object,
	structures map[string]catalog.Structure,
	definitions map[string]string,
	summary *Summary,
) error {
	if err := assert(ctx, src, "the structure fetch"); err != nil {
		return err
	}
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
		if err := assert(ctx, src, "the module fetch"); err != nil {
			return err
		}
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

// module reports whether this object's kind carries a body, read off the kind
// table rather than decided here.
func module(object catalog.Object) bool {
	spec, ok := catalog.Lookup(object.Kind)
	return ok && spec.Module
}

// samples reads the first rows of the tables about to be described. Only those
// tables: a sample is describer input and nothing else, so sampling a table
// whose description is already current would read user data for nothing.
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

// describeAll sends the batches. A build with no client describes nothing,
// which is the structure-only shape; it is not an error.
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

// write renders the tree. Entries come back in manifest order so two builds
// over the same database produce the same files, and an object the model
// skipped keeps whatever sentence the previous build gave it rather than
// losing one it already had.
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

// remove takes objects the database no longer has out of the index. Their
// catalog rows go simply by being rewritten without them; their detail files
// have to be deleted, or the tree would keep describing a table that is gone.
func remove(dir string, dropped []string) error {
	for _, key := range dropped {
		err := os.Remove(filepath.Join(dir, columnsDir, key+".tsv"))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}
