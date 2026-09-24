package sample

import (
	"strings"

	"github.com/branow/dbmap/internal/catalog"
	"github.com/branow/dbmap/internal/engine"
	"github.com/branow/dbmap/internal/fingerprint"
	"github.com/branow/dbmap/internal/redact"
)

// CellChars caps one sampled cell in characters, the unit the describer's
// budget is measured in.
const CellChars = 200

// Unsampleable lists lower-cased engine type names whose values a describer
// cannot read. Only genuinely opaque values belong here: binary payloads,
// spatial types, row versions.
//
// Long and unbounded string types are deliberately absent. Excluding them hid
// every lookup table's value domain, which is the thing sampling exists to
// capture - first by excluding `text`, and then, less visibly, by excluding
// every column whose width renders as "(max)", which on SQL Server is the
// ordinary spelling of a description column. Every cell is capped at
// CellChars anyway, so unboundedness is not a reason to skip a column.
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

// Sampleable reports whether a column's type permits reading its values; who
// the value belongs to is redact's question, not this one.
func Sampleable(column catalog.Column) bool {
	name := strings.ToLower(strings.TrimSpace(column.Type))
	for _, unsampleable := range Unsampleable {
		if name == unsampleable {
			return false
		}
	}
	return true
}

// Plan is what a table's sample may read, and what it may not. The engine never
// widens the projection it is handed.
type Plan struct {
	Table    engine.Table
	Withheld []catalog.Withheld
	// Complete marks a table the sample reads in full.
	Complete bool
}

// Empty reports a table nothing may be read from; such a table is never queried.
func (p Plan) Empty() bool { return len(p.Table.Columns) == 0 }

// Project splits a table's columns into readable and withheld, with a reason
// for each. The PII test runs first, so a column that is both reports as
// personal data.
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
