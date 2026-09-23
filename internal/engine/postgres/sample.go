package postgres

import (
	"context"
	"strconv"
	"strings"

	"github.com/branow/dbmap/internal/catalog"
	"github.com/branow/dbmap/internal/engine"
)

// CellChars caps one sampled cell.
const CellChars = 200

// sampleQuery reads the first n rows of one table. LIMIT n must never gain an
// ORDER BY: a sort ranks the whole table before returning a row. Cells cast to
// text, which Postgres allows from any type, so no column can fail the sample.
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

// Sample reads the first n rows of one table. An empty projection opens no
// query at all.
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
