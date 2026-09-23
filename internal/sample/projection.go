package sample

import (
	"strings"

	"github.com/branow/dbmap/internal/catalog"
	"github.com/branow/dbmap/internal/engine"
	"github.com/branow/dbmap/internal/fingerprint"
	"github.com/branow/dbmap/internal/redact"
)

// CellChars caps one sampled cell, so one wide column cannot decide the size of
// a prompt. Engines also cap in their own SQL in their own units; this cap is
// restated in characters because the budget protected is the describer's.
const CellChars = 200

// Unbounded is the width a catalog renders for a column with no declared one.
const Unbounded = "(max)"

// Unsampleable lists the lower-cased engine type names whose values a describer
// cannot read: binary payloads, spatial values and row versions, with both
// engines' spellings of one concept side by side.
//
// Long string types are deliberately absent. Width is already handled by the
// CellChars cap, and excluding them once hid every lookup table's value domain:
// a status table projected its integer key and nothing else.
//
// A table rather than a chain of comparisons, so a new type is a new row.
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
// Withheld columns are carried rather than dropped, so the describer is not
// misled about the table's shape.
type Plan struct {
	// Table is the engine's instruction: the table, and the projection already
	// filtered. The engine adds the row cap and the per-cell cap and never
	// widens what it is handed.
	Table    engine.Table
	Withheld []catalog.Withheld
	// Complete marks a table the sample reads in full. Those are the value
	// domains, and the reason the run is ordered the way it is.
	Complete bool
}

// Empty reports a table nothing may be read from. Such a table is never
// queried: not queried and discarded, never sent.
func (p Plan) Empty() bool { return len(p.Table.Columns) == 0 }

// Project splits a table's columns into the ones a sample may read and the ones
// it withholds, with the reason for each. PII is decided on the column NAME,
// because a type cannot tell a product code from a surname; the name test comes
// first so a column that is both reports as personal data.
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
