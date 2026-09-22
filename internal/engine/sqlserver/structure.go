package sqlserver

import (
	"context"
	"strconv"
	"strings"

	"github.com/branow/dbmap/internal/catalog"
	"github.com/branow/dbmap/internal/engine"
)

// source is one metadata query and how to fold its rows into a structure.
// Sources are data so that adding one is a row rather than a branch, and so a
// test can assert what this engine would send with no server behind it.
type source struct {
	Name string
	SQL  func() string
	// Width is the narrowest row this source can still shape. A shorter row is
	// dropped.
	Width int
	// Fold applies one row to the structure it belongs to.
	Fold func(target *parsed, row []string)
}

// parsed is a structure under construction. Index rows arrive one per column,
// so they are held flat until every row is in and then folded.
type parsed struct {
	structure catalog.Structure
	indexRows []indexRow
}

type indexRow struct {
	name    string
	primary bool
	unique  bool
	column  string
}

// sources is the structure stage: five bounded queries, around 8,800 narrow
// metadata rows across the three measured databases. Every one reads sys.*
// catalog views only, so no user data page is touched and none of them needs a
// memory grant worth the name.
var sources = []source{
	{
		Name:  "columns",
		Width: 10,
		SQL: func() string {
			return `SELECT s.name, o.name, c.name, t.name, c.max_length, c.precision, c.scale,
  c.is_nullable, c.is_identity, c.is_computed
FROM sys.columns c
JOIN sys.objects o ON o.object_id = c.object_id
JOIN sys.schemas s ON s.schema_id = o.schema_id
JOIN sys.types t ON t.user_type_id = c.user_type_id
WHERE ` + inScope() + `
ORDER BY s.name, o.name, c.column_id`
		},
		Fold: func(target *parsed, row []string) {
			target.structure.Columns = append(target.structure.Columns, catalog.Column{
				Name:     row[2],
				Type:     row[3],
				Length:   width(row[3], row[4], row[5], row[6]),
				Nullable: flag(row[7]),
				Identity: flag(row[8]),
				Computed: flag(row[9]),
			})
		},
	},
	{
		Name:  "indexes",
		Width: 6,
		SQL: func() string {
			return `SELECT s.name, o.name, i.name, i.is_primary_key, i.is_unique, c.name
FROM sys.indexes i
JOIN sys.objects o ON o.object_id = i.object_id
JOIN sys.schemas s ON s.schema_id = o.schema_id
JOIN sys.index_columns ic ON ic.object_id = i.object_id AND ic.index_id = i.index_id
JOIN sys.columns c ON c.object_id = ic.object_id AND c.column_id = ic.column_id
WHERE ` + inScope() + ` AND i.type > 0 AND ic.is_included_column = 0
ORDER BY s.name, o.name, i.index_id, ic.key_ordinal`
		},
		Fold: func(target *parsed, row []string) {
			target.indexRows = append(target.indexRows, indexRow{
				name:    row[2],
				primary: flag(row[3]),
				unique:  flag(row[4]),
				column:  row[5],
			})
		},
	},
	{
		Name:  "foreignKeys",
		Width: 6,
		SQL: func() string {
			return `SELECT ps.name, po.name, pc.name, rs.name, ro.name, rc.name
FROM sys.foreign_key_columns fkc
JOIN sys.objects po ON po.object_id = fkc.parent_object_id
JOIN sys.schemas ps ON ps.schema_id = po.schema_id
JOIN sys.columns pc ON pc.object_id = fkc.parent_object_id
  AND pc.column_id = fkc.parent_column_id
JOIN sys.objects ro ON ro.object_id = fkc.referenced_object_id
JOIN sys.schemas rs ON rs.schema_id = ro.schema_id
JOIN sys.columns rc ON rc.object_id = fkc.referenced_object_id
  AND rc.column_id = fkc.referenced_column_id
WHERE ps.name NOT IN (` + schemaList() + `)
ORDER BY ps.name, po.name, pc.name`
		},
		Fold: func(target *parsed, row []string) {
			target.structure.ForeignKeys = append(target.structure.ForeignKeys, catalog.ForeignKey{
				Column:     row[2],
				References: row[3] + "." + row[4] + "." + row[5],
			})
		},
	},
	{
		Name:  "parameters",
		Width: 6,
		SQL: func() string {
			return `SELECT s.name, o.name, p.name, t.name, p.max_length, p.is_output
FROM sys.parameters p
JOIN sys.objects o ON o.object_id = p.object_id
JOIN sys.schemas s ON s.schema_id = o.schema_id
JOIN sys.types t ON t.user_type_id = p.user_type_id
WHERE ` + inScope() + `
ORDER BY s.name, o.name, p.parameter_id`
		},
		Fold: func(target *parsed, row []string) {
			name := row[2]
			if name == "" {
				name = catalog.Returns
			}
			target.structure.Parameters = append(target.structure.Parameters, catalog.Param{
				Name:   name,
				Type:   row[3] + width(row[3], row[4], "0", "0"),
				Output: flag(row[5]),
			})
		},
	},
	{
		Name:  "synonyms",
		Width: 3,
		SQL: func() string {
			return `SELECT s.name, sn.name, sn.base_object_name
FROM sys.synonyms sn
JOIN sys.schemas s ON s.schema_id = sn.schema_id
WHERE s.name NOT IN (` + schemaList() + `)
ORDER BY s.name, sn.name`
		},
		Fold: func(target *parsed, row []string) { target.structure.Target = row[2] },
	},
}

// sized are the types whose width a reader needs spelled out.
var sized = []string{"varchar", "nvarchar", "char", "nchar", "varbinary", "binary"}

// scaled are the types that carry a precision and a scale instead.
var scaled = []string{"decimal", "numeric"}

// width renders the width a reader needs — "(50)", "(18,2)", "(max)" — rather
// than the raw catalog numbers. max_length is in BYTES, so a national type
// halves it, and -1 is the (max) sentinel.
func width(kind, maxLength, precision, scale string) string {
	if contains(scaled, kind) {
		return "(" + precision + "," + scale + ")"
	}
	if !contains(sized, kind) {
		return ""
	}
	bytes, err := strconv.Atoi(maxLength)
	if err != nil {
		return ""
	}
	if bytes == -1 {
		return "(max)"
	}
	if strings.HasPrefix(kind, "n") {
		bytes /= 2
	}
	return "(" + strconv.Itoa(bytes) + ")"
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// fold applies one source's rows to the structures under construction. It is
// pure, so the whole of the structure stage's shaping is testable without a
// connection.
func fold(target map[string]*parsed, source source, rows [][]string) {
	for _, raw := range rows {
		if len(raw) < source.Width {
			continue
		}
		row := cells(raw)
		key := row[0] + "." + row[1]
		entry, ok := target[key]
		if !ok {
			entry = &parsed{}
			target[key] = entry
		}
		source.Fold(entry, row)
	}
}

// foldIndexes turns one-row-per-column index rows into one entry per index and
// lifts the primary key out, because a reader wants it named separately.
func foldIndexes(rows []indexRow) ([]string, []catalog.Index) {
	var order []string
	byName := map[string]*catalog.Index{}
	primary := map[string]bool{}

	for _, row := range rows {
		entry, ok := byName[row.name]
		if !ok {
			entry = &catalog.Index{Name: row.name, Unique: row.unique}
			byName[row.name] = entry
			primary[row.name] = row.primary
			order = append(order, row.name)
		}
		entry.Columns = append(entry.Columns, row.column)
	}

	var key []string
	var indexes []catalog.Index
	for _, name := range order {
		if primary[name] {
			if key == nil {
				key = byName[name].Columns
			}
			continue
		}
		indexes = append(indexes, *byName[name])
	}
	return key, indexes
}

// Structure runs every source against one database, asking for room between
// each, and returns a structure per object key.
func (e *Engine) Structure(
	ctx context.Context,
	conn engine.Conn,
) (map[string]catalog.Structure, error) {
	target := map[string]*parsed{}

	for _, source := range sources {
		if err := e.halt(ctx, conn, "structure/"+source.Name); err != nil {
			return nil, err
		}
		rows, err := e.query(ctx, conn, source.SQL())
		if err != nil {
			return nil, err
		}
		fold(target, source, rows)
	}

	structures := make(map[string]catalog.Structure, len(target))
	for key, entry := range target {
		entry.structure.PrimaryKey, entry.structure.Indexes = foldIndexes(entry.indexRows)
		structures[key] = entry.structure
	}
	return structures, nil
}
