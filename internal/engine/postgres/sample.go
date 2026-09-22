package postgres

import (
	"context"
	"strconv"
	"strings"

	"github.com/branow/dbmap/internal/catalog"
	"github.com/branow/dbmap/internal/engine"
)

// CellChars caps one sampled cell, for the same reason it does on SQL Server:
// a describer needs the shape of a value, not all of it.
const CellChars = 200

// sampleQuery reads the first n rows of one table.
//
// LIMIT n with NO ORDER BY: a sort would rank the whole table before returning
// a row, while without one the executor stops as soon as n rows have been
// produced. The projection is explicit because the caller has already dropped
// the columns that must not be read, and a star would drag them back in.
//
// Every cell is cast to text rather than to a sized type: Postgres allows an
// explicit cast to text from any type at all, so one unexpected column type
// cannot fail the sample.
func (e *Engine) sampleQuery(table engine.Table, n int) string {
	cells := make([]string, len(table.Columns))
	for i, column := range table.Columns {
		cells[i] = "left(cast(" + e.Quote(column) + " AS text), " +
			strconv.Itoa(CellChars) + ")"
	}
	return "SELECT\n  " + strings.Join(cells, ",\n  ") + "\nFROM " +
		e.Quote(table.Schema) + "." + e.Quote(table.Name) +
		"\nLIMIT " + strconv.Itoa(n)
}

// Sample reads the first n rows of one table for the describer. A table with an
// empty projection is never queried at all.
func (e *Engine) Sample(
	ctx context.Context,
	conn engine.Conn,
	table engine.Table,
	n int,
) (catalog.Sample, error) {
	sample := catalog.Sample{Key: table.Key(), Columns: table.Columns}
	if len(table.Columns) == 0 || n < 1 {
		return sample, nil
	}
	if err := e.halt(ctx, conn, "sample "+table.Key()); err != nil {
		return catalog.Sample{}, err
	}

	rows, err := e.query(ctx, conn, e.sampleQuery(table, n))
	if err != nil {
		return catalog.Sample{}, err
	}
	sample.Rows = rows
	return sample, nil
}
