package sqlserver

import (
	"context"
	"strconv"
	"strings"

	"github.com/branow/dbmap/internal/catalog"
	"github.com/branow/dbmap/internal/engine"
)

// CellChars caps one sampled cell.
const CellChars = 200

// sampleQuery reads the first n rows of one table. TOP (n) must never gain an
// ORDER BY: a sort ranks the whole table before returning anything.
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
