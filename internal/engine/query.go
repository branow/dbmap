package engine

import (
	"context"
	"database/sql"
	"strconv"
	"strings"

	"github.com/branow/dbmap/internal/catalog"
)

// MaxDefinition caps one module body leaving the database. It is catalog's
// constant because the writer and the engine must agree on when a body was cut.
const MaxDefinition = catalog.MaxDefinition

// ModuleBatch is how many objects one body fetch asks for.
const ModuleBatch = 40

// Query is the one path from an engine to a database, and the only place the
// safety contract is applied.
//
// Rows come back as strings so parsing is a pure function over cells. A NULL
// reads as the empty string.
func Query(
	ctx context.Context,
	conn Conn,
	guard Guard,
	statement string,
	args ...any,
) ([][]string, error) {
	rows, err := conn.Query(ctx, guard.Session, guard.Apply(statement), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return collect(rows)
}

func collect(rows Rows) ([][]string, error) {
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	cells := make([]sql.NullString, len(columns))
	dest := make([]any, len(columns))
	for i := range cells {
		dest[i] = &cells[i]
	}

	var out [][]string
	for rows.Next() {
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		row := make([]string, len(cells))
		for i, cell := range cells {
			row[i] = cell.String
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// Batch splits keys into fetch-sized groups.
func Batch(keys []string, size int) [][]string {
	if size < 1 {
		size = 1
	}
	var batches [][]string
	for start := 0; start < len(keys); start += size {
		end := start + size
		if end > len(keys) {
			end = len(keys)
		}
		batches = append(batches, keys[start:end])
	}
	return batches
}

// Placeholders renders n ordinal placeholders in an engine's own dialect, so a
// key list rides as parameters rather than interpolated text.
func Placeholders(prefix string, n int) string {
	var out strings.Builder
	for i := 1; i <= n; i++ {
		if i > 1 {
			out.WriteString(", ")
		}
		out.WriteString(prefix)
		out.WriteString(strconv.Itoa(i))
	}
	return out.String()
}

// Args widens a key slice into driver arguments.
func Args(keys []string) []any {
	args := make([]any, len(keys))
	for i, key := range keys {
		args[i] = key
	}
	return args
}
