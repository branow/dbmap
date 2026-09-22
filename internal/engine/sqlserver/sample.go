package sqlserver

import (
	"context"
	"strconv"
	"strings"

	"github.com/branow/dbmap/internal/catalog"
	"github.com/branow/dbmap/internal/engine"
)

// CellChars caps one sampled cell. A describer needs to see the shape of a
// value, not all of it, and one wide column must not decide the size of the
// result.
const CellChars = 200

// sampleQuery reads the first n rows of one table.
//
// TOP (n) with NO ORDER BY is the whole reason a 616,802,364-row table is as
// cheap to sample as an 11-row lookup: a sort would rank the entire table
// before returning anything, while without one the engine stops reading after
// n rows. An ORDER BY must never appear here.
//
// The projection is explicit for the same reason it is not SELECT *: the caller
// has already dropped the columns that must not be read, and a star would drag
// them back in.
func (e *Engine) sampleQuery(table engine.Table, n int) string {
	cells := make([]string, len(table.Columns))
	for i, column := range table.Columns {
		cells[i] = "LEFT(CAST(" + e.Quote(column) + " AS nvarchar(" +
			strconv.Itoa(CellChars) + ")), " + strconv.Itoa(CellChars) + ")"
	}
	return "SELECT TOP (" + strconv.Itoa(n) + ")\n  " +
		strings.Join(cells, ",\n  ") + "\nFROM " +
		e.Quote(table.Schema) + "." + e.Quote(table.Name)
}

// Sample reads the first n rows of one table for the describer. A table with an
// empty projection — every column withheld as personal data, say — is never
// queried at all: there is nothing to learn from it and the row would be thrown
// away.
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
