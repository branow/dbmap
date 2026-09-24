// Package sample reads the first rows of a table for the describer, and decides
// what may be read at all. Sampled values are transient describer input and are
// never persisted.
package sample

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/branow/dbmap/internal/catalog"
	"github.com/branow/dbmap/internal/engine"
	"github.com/branow/dbmap/internal/fingerprint"
)

// Fetcher is the half of an engine this package uses. Health is NOT asked
// before each table: the engine already checks it before the statement it is
// about to send, and asking first as well doubles the round trips a sampling
// run costs. It is asked once a sample has failed, to tell a table that failed
// for its own reasons from a run that cannot continue.
type Fetcher interface {
	Sample(ctx context.Context, conn engine.Conn, table engine.Table, n int) (catalog.Sample, error)
	Health(ctx context.Context, conn engine.Conn) (engine.Health, error)
}

// Logger reports what a run is doing and what it skipped.
type Logger interface {
	Info(message string)
	Warn(message string)
}

// Options configure one sampling run.
type Options struct {
	// Rows per table; zero means fingerprint.SampleRows.
	Rows int
	// Limit caps how many tables are sampled; zero means no cap.
	Limit  int
	Logger Logger
}

func (o Options) rows() int {
	if o.Rows <= 0 {
		return fingerprint.SampleRows
	}
	return o.Rows
}

// Order sorts plans complete-tables-first, then smallest first, then by key so
// a run never depends on map order.
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

// Plans projects every table entry and drops the ones nothing may be read from,
// so such a table is never queried at all.
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

// All samples the tables in entries, keyed by object. A table that fails for
// its own reasons is skipped, but a server saying it has no room - or refusing
// to say - halts the run rather than being skipped table by table.
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
	for i, plan := range plans {
		// Announced BEFORE the statement, not after: a progress line is worth
		// having precisely when a read does not come back, and one printed on
		// the way out names every table but the one that is stuck.
		info(opts.Logger, fmt.Sprintf("sample: %d/%d %s (%s)",
			i+1, len(plans), plan.Table.Key(), depth(plan, rows, opts.rows())))

		got, err := fetcher.Sample(ctx, conn, plan.Table, opts.rows())
		if err != nil {
			if fatal(ctx, fetcher, conn, err) {
				return samples, err
			}
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

// fatal reports whether a failed sample ends the run rather than costing one
// table. A server saying it has no room does; so does a connection that is
// gone, which is why the probe happens at all - skipping table by table through
// a dead connection reports five hundred skips and no failure.
func fatal(ctx context.Context, fetcher Fetcher, conn engine.Conn, err error) bool {
	var unhealthy *engine.UnhealthyError
	if errors.As(err, &unhealthy) {
		return true
	}
	_, probe := fetcher.Health(ctx, conn)
	return probe != nil
}

// depth says how much of a table this sample reads, which is the fact a reader
// of the line wants: a whole small table is a value domain, 25 rows off a large
// one is a shape.
func depth(plan Plan, rows map[string]int64, n int) string {
	total := rows[plan.Table.Key()]
	if plan.Complete {
		return fmt.Sprintf("%d rows, whole table", total)
	}
	return fmt.Sprintf("%d of %d rows", n, total)
}

func info(logger Logger, message string) {
	if logger != nil {
		logger.Info(message)
	}
}

func warn(logger Logger, message string) {
	if logger != nil {
		logger.Warn(message)
	}
}

// Render turns a sample into the compact block the describer reads. Withheld
// columns are named rather than omitted, or the describer misreads the shape.
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
