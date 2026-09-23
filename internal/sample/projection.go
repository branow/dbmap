package sample

import (
	"strings"

	"github.com/branow/dbmap/internal/catalog"
	"github.com/branow/dbmap/internal/engine"
	"github.com/branow/dbmap/internal/fingerprint"
	"github.com/branow/dbmap/internal/redact"
)

// CellChars caps one sampled cell. The describer needs the shape of a value,
// not all of it, so one wide column cannot decide the size of a prompt.
//
// Both engines already cap in their own SQL, in their own units — SQL Server
// counts nvarchar units, Postgres counts characters. The cap is restated here
// in characters because the budget being protected is the describer's, which is
// measured in characters whatever the server counted in.
const CellChars = 200

// Unbounded is the width a catalog renders for a column with no declared one.
const Unbounded = "(max)"

// Unsampleable is the type table: values that are opaque or meaningless to a
// describer. Names are lower-cased engine type names and cover both engines'
// spellings of one concept — SQL Server's `image` and Postgres's `bytea` are
// the same decision.
//
// It lists only what a describer cannot read: binary payloads, spatial values
// and row versions. It deliberately does NOT list the long string types.
// Unboundedness is already handled — every cell is capped at CellChars on the
// way out — so excluding them buys nothing and costs the thing this tool exists
// for. A measured run made that concrete: `text` was on this list, so sampling
// a three-row status lookup projected its integer key and nothing else, and the
// describer never saw Incomplete, Pending or CC Declined. Those values ARE the
// value domain; capturing them without a DISTINCT scan is the whole argument
// for sampling small tables first.
//
// It is a table rather than a chain of comparisons so a new type is a new row.
var Unsampleable = []string{
	"image",
	"varbinary",
	"binary",
	"bytea",
	"geography",
	"geometry",
	"hierarchyid",
	"sql_variant",
	"timestamp",
	"rowversion",
}

// Sampleable reports whether a column's VALUES may be read at all. This is the
// question a type can answer; who the value belongs to is a question only the
// name can answer, and that one belongs to redact.
func Sampleable(column catalog.Column) bool {
	if strings.EqualFold(column.Length, Unbounded) {
		return false
	}
	name := strings.ToLower(strings.TrimSpace(column.Type))
	for _, unsampleable := range Unsampleable {
		if name == unsampleable {
			return false
		}
	}
	return true
}

// Plan is what a table's sample is allowed to read, and what it is not.
//
// The withheld columns are carried rather than dropped: a describer shown four
// of a table's nine columns and told nothing about the other five is being
// misled about the table's shape, which is worse than seeing fewer values.
type Plan struct {
	// Table is the engine's instruction: the table, and the projection already
	// filtered. The engine adds the row cap and the per-cell cap and never
	// widens what it is handed.
	Table    engine.Table
	Withheld []catalog.Withheld
	// Complete marks a table the sample reads in full. Those are the value
	// domains — 45 lookup tables in one measured database, every one under 25
	// rows — and the reason the run is ordered the way it is.
	Complete bool
}

// Empty reports a table nothing may be read from. Such a table is never
// queried: not queried and discarded, never sent.
func (p Plan) Empty() bool { return len(p.Table.Columns) == 0 }

// Project splits a table's columns into the ones a sample may read and the ones
// it withholds, with the reason for each.
//
// PII is decided on the column NAME, using the rules in redact, because a type
// says nothing about who a value belongs to: an nvarchar(50) is a product code
// or a surname and the catalog cannot tell you which. The name test comes
// first, so a withheld person's column reads as personal data rather than as an
// unsampleable type when it happens to be both.
func Project(entry catalog.Entry) Plan {
	plan := Plan{
		Table:    engine.Table{Schema: entry.Object.Schema, Name: entry.Object.Name},
		Complete: entry.Object.Rows <= fingerprint.SampleRows,
	}
	for _, column := range entry.Structure.Columns {
		switch {
		case redact.IsPII(column.Name):
			plan.Withheld = append(plan.Withheld, catalog.Withheld{
				Column: column.Name,
				Reason: catalog.WithheldPII,
			})
		case !Sampleable(column):
			plan.Withheld = append(plan.Withheld, catalog.Withheld{
				Column: column.Name,
				Reason: catalog.WithheldUnsampleable,
			})
		default:
			plan.Table.Columns = append(plan.Table.Columns, column.Name)
		}
	}
	return plan
}
