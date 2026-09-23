// Package sample reads the first rows of a table for the describer, and decides
// what may be read at all.
//
// Two rules shape everything here, and both exist because an earlier attempt at
// this work overloaded a server: the projection is planned before anything is
// sent, so a person's column is never read and a table of nothing but such
// columns is never queried at all; and the run is ordered complete-tables-first,
// so a capped run spends its budget on the lookup tables that ARE their own
// value domains rather than on the first rows of a huge log.
//
// Sampled values are transient describer input. They never reach the index,
// never enter a fingerprint, and nothing here persists one.
package sample

import (
	"context"
	"sort"
	"strings"

	"github.com/branow/dbmap/internal/catalog"
	"github.com/branow/dbmap/internal/engine"
	"github.com/branow/dbmap/internal/fingerprint"
)

// Fetcher is the half of an engine this package uses, narrow enough that every
// test here runs against a fake with no database behind it.
type Fetcher interface {
	Sample(ctx context.Context, conn engine.Conn, table engine.Table, n int) (catalog.Sample, error)
	Health(ctx context.Context, conn engine.Conn) (engine.Health, error)
}

// Logger reports what a run skipped. A sample that could not be read is not a
// reason to abandon a build, but it is never silent either.
type Logger interface {
	Warn(message string)
}

// Options configure one sampling run.
type Options struct {
	// Rows is how many rows to read per table. Zero means fingerprint.SampleRows,
	// which is the depth the describer is calibrated for.
	Rows int
	// Limit caps how many tables are sampled at all. Zero means no cap. A
	// capped run is the case Order exists for.
	Limit  int
	Logger Logger
}

func (o Options) rows() int {
	if o.Rows <= 0 {
		return fingerprint.SampleRows
	}
	return o.Rows
}

// Order sorts plans complete-tables-first: a table read in full has its whole
// value domain captured with no DISTINCT scan, where the front rows of a huge
// table say almost nothing. Within a group the smaller table comes first and
// ties break on key, so a run does not depend on map order.
func Order(plans []Plan, rows map[string]int64) []Plan {
	ordered := make([]Plan, len(plans))
	copy(ordered, plans)

	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if a.Complete != b.Complete {
			return a.Complete
		}
		if ra, rb := rows[a.Table.Key()], rows[b.Table.Key()]; ra != rb {
			return ra < rb
		}
		return a.Table.Key() < b.Table.Key()
	})
	return ordered
}

// Plans projects every table entry and drops the ones nothing may be read from.
// A table whose every column is withheld never becomes a plan, so it can never
// reach a server.
func Plans(entries []catalog.Entry) []Plan {
	var plans []Plan
	for _, entry := range entries {
		if entry.Object.Kind != catalog.Table {
			continue
		}
		plan := Project(entry)
		if plan.Empty() {
			continue
		}
		plans = append(plans, plan)
	}
	return plans
}

// All samples the tables in entries, in value-domain-first order, keyed by
// object.
//
// Health is rechecked before every table because the server's headroom changes
// during a run, and an unreadable reading halts it: not seeing the floor is not
// the same as being above it. One table that fails to sample is logged and
// skipped; a server saying it has no room stops everything.
func All(
	ctx context.Context,
	fetcher Fetcher,
	conn engine.Conn,
	entries []catalog.Entry,
	opts Options,
) (map[string]catalog.Sample, error) {
	rows := make(map[string]int64, len(entries))
	for _, entry := range entries {
		rows[entry.Key()] = entry.Object.Rows
	}

	plans := Order(Plans(entries), rows)
	if opts.Limit > 0 && len(plans) > opts.Limit {
		plans = plans[:opts.Limit]
	}

	samples := make(map[string]catalog.Sample, len(plans))
	for _, plan := range plans {
		health, err := fetcher.Health(ctx, conn)
		if err != nil {
			health = engine.Classify(nil)
		}
		if err := engine.Assert(health, "sampling "+plan.Table.Key()); err != nil {
			return samples, err
		}

		got, err := fetcher.Sample(ctx, conn, plan.Table, opts.rows())
		if err != nil {
			warn(opts.Logger, "sample: skipped "+plan.Table.Key()+": "+err.Error())
			continue
		}
		got.Key = plan.Table.Key()
		got.Withheld = plan.Withheld
		got.Complete = plan.Complete
		samples[got.Key] = got
	}
	return samples, nil
}

func warn(logger Logger, message string) {
	if logger != nil {
		logger.Warn(message)
	}
}

// Render turns a sample into the compact block the describer reads. Withheld
// columns are named rather than omitted: hiding their existence misleads the
// describer about the table's shape, which is worse than seeing fewer values.
func Render(s catalog.Sample) string {
	if len(s.Columns) == 0 && len(s.Withheld) == 0 {
		return ""
	}

	var out strings.Builder
	out.WriteString(strings.Join(s.Columns, "\t"))
	for _, row := range s.Rows {
		out.WriteString("\n")
		out.WriteString(strings.Join(capCells(row), "\t"))
	}
	if note := withheldNote(s.Withheld); note != "" {
		out.WriteString("\n")
		out.WriteString(note)
	}
	return out.String()
}

// capCells trims every cell to CellChars.
func capCells(row []string) []string {
	out := make([]string, len(row))
	for i, cell := range row {
		runes := []rune(strings.ReplaceAll(strings.ReplaceAll(cell, "\t", " "), "\n", " "))
		if len(runes) > CellChars {
			runes = runes[:CellChars]
		}
		out[i] = string(runes)
	}
	return out
}

func withheldNote(withheld []catalog.Withheld) string {
	if len(withheld) == 0 {
		return ""
	}
	byReason := map[catalog.Withholding][]string{}
	for _, w := range withheld {
		byReason[w.Reason] = append(byReason[w.Reason], w.Column)
	}

	var notes []string
	for _, reason := range []catalog.Withholding{catalog.WithheldPII, catalog.WithheldUnsampleable} {
		columns := byReason[reason]
		if len(columns) == 0 {
			continue
		}
		notes = append(notes, "# not sampled ("+string(reason)+"): "+strings.Join(columns, ", "))
	}
	return strings.Join(notes, "\n")
}
