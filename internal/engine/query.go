package engine

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
)

// MaxDefinition caps one module body on the way out of the database. The whole
// measured module corpus of the largest database is 3.7 MB, so there is nothing
// to save by trimming hard; 50,000 characters keeps 99% of bodies byte-complete
// while a single pathological one cannot decide the size of a batch. The prompt
// cap, applied much later, is lower on purpose so the stored copy stays fuller.
const MaxDefinition = 50000

// ModuleBatch is how many objects one body fetch asks for. Bodies average 6.5 KB
// and peak at 88 KB, so a batch is a few hundred KB and a pathological one still
// cannot reach a size worth streaming.
const ModuleBatch = 40

// Query is the one path from an engine to a database, and the only place the
// safety contract is applied. The order is load-bearing:
//
//  1. prove the statement is a read — and refuse before a connection is used,
//     so an unclassifiable query never reaches a server at all
//  2. apply the engine's resource guard, so no statement can be sent without it
//  3. run it, inside whatever session the guard asked for
//
// Rows come back as strings because that is what every consumer of this package
// does with them: the parse step is a pure function over cells, testable with no
// database behind it. A NULL reads as the empty string.
func Query(
	ctx context.Context,
	conn Conn,
	guard Guard,
	statement string,
	args ...any,
) ([][]string, error) {
	if err := AssertReadOnly(statement); err != nil {
		return nil, err
	}

	rows, err := conn.Query(ctx, guard.Session, guard.Apply(statement), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return collect(rows)
}

// collect drains a cursor into string cells.
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
// key list rides as parameters rather than as interpolated text.
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

// Args widens a key slice into the driver argument slice a placeholder list
// expects.
func Args(keys []string) []any {
	args := make([]any, len(keys))
	for i, key := range keys {
		args[i] = key
	}
	return args
}
