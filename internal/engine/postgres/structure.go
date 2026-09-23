package postgres

import (
	"context"
	"strings"

	"github.com/branow/dbmap/internal/catalog"
	"github.com/branow/dbmap/internal/engine"
)

// source is one metadata query and how to fold its rows into a structure.
type source struct {
	Name string
	SQL  func() string
	// Width is the narrowest row this source can still shape.
	Width int
	Fold  func(target *parsed, row []string)
}

// parsed is a structure under construction. Index rows arrive one per column,
// so they are held flat until every row is in.
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

// sources is the structure stage, reading pg_catalog only. Postgres has no
// synonyms, hence one source fewer than SQL Server.
var sources = []source{
	{
		Name:  "columns",
		Width: 7,
		SQL: func() string {
			return `SELECT n.nspname, c.relname, a.attname,
  pg_catalog.format_type(a.atttypid, a.atttypmod),
  CASE WHEN a.attnotnull THEN '0' ELSE '1' END,
  CASE WHEN a.attidentity <> ''
         OR pg_catalog.pg_get_expr(d.adbin, d.adrelid) LIKE 'nextval(%'
       THEN '1' ELSE '0' END,
  CASE WHEN a.attgenerated <> '' THEN '1' ELSE '0' END
FROM pg_catalog.pg_attribute a
JOIN pg_catalog.pg_class c ON c.oid = a.attrelid
JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
LEFT JOIN pg_catalog.pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
WHERE a.attnum > 0 AND NOT a.attisdropped
  AND c.relkind IN (` + RelKinds() + `)
  AND ` + schemaScope("n.nspname") + `
ORDER BY n.nspname, c.relname, a.attnum`
		},
		Fold: func(target *parsed, row []string) {
			kind, length := splitType(row[3])
			target.structure.Columns = append(target.structure.Columns, catalog.Column{
				Name:     row[2],
				Type:     kind,
				Length:   length,
				Nullable: flag(row[4]),
				Identity: flag(row[5]),
				Computed: flag(row[6]),
			})
		},
	},
	{
		Name:  "indexes",
		Width: 6,
		SQL: func() string {
			return `SELECT n.nspname, c.relname, ic.relname,
  CASE WHEN i.indisprimary THEN '1' ELSE '0' END,
  CASE WHEN i.indisunique THEN '1' ELSE '0' END,
  a.attname
FROM pg_catalog.pg_index i
JOIN pg_catalog.pg_class c ON c.oid = i.indrelid
JOIN pg_catalog.pg_class ic ON ic.oid = i.indexrelid
JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
JOIN LATERAL unnest(i.indkey::int2[]) WITH ORDINALITY AS k(attnum, ord) ON TRUE
JOIN pg_catalog.pg_attribute a ON a.attrelid = c.oid AND a.attnum = k.attnum
WHERE k.ord <= i.indnkeyatts
  AND ` + schemaScope("n.nspname") + `
ORDER BY n.nspname, c.relname, ic.relname, k.ord`
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
			return `SELECT n.nspname, c.relname, a.attname, fn.nspname, fc.relname, fa.attname
FROM pg_catalog.pg_constraint k
JOIN pg_catalog.pg_class c ON c.oid = k.conrelid
JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
JOIN pg_catalog.pg_class fc ON fc.oid = k.confrelid
JOIN pg_catalog.pg_namespace fn ON fn.oid = fc.relnamespace
JOIN LATERAL unnest(k.conkey, k.confkey) WITH ORDINALITY AS col(parent, ref, ord) ON TRUE
JOIN pg_catalog.pg_attribute a ON a.attrelid = k.conrelid AND a.attnum = col.parent
JOIN pg_catalog.pg_attribute fa ON fa.attrelid = k.confrelid AND fa.attnum = col.ref
WHERE k.contype = 'f'
  AND ` + schemaScope("n.nspname") + `
ORDER BY n.nspname, c.relname, col.ord`
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
		Width: 5,
		SQL: func() string {
			return routineCTE() + `
SELECT r.schema, r.name, '` + catalog.Returns + `',
  pg_catalog.format_type(p.prorettype, NULL), '0', 0
FROM routine r
JOIN pg_catalog.pg_proc p ON p.oid = r.oid
UNION ALL
SELECT r.schema, r.name,
  COALESCE(arg.name, '$' || arg.ord),
  pg_catalog.format_type(arg.type, NULL),
  CASE WHEN COALESCE(arg.mode, 'i') IN ('o', 'b', 't') THEN '1' ELSE '0' END,
  arg.ord
FROM routine r
JOIN pg_catalog.pg_proc p ON p.oid = r.oid
JOIN LATERAL unnest(
    COALESCE(p.proallargtypes, p.proargtypes::oid[]),
    p.proargnames,
    p.proargmodes
  ) WITH ORDINALITY AS arg(type, name, mode, ord) ON TRUE
ORDER BY 1, 2, 6`
		},
		Fold: func(target *parsed, row []string) {
			target.structure.Parameters = append(target.structure.Parameters, catalog.Param{
				Name:   row[2],
				Type:   row[3],
				Output: flag(row[4]),
			})
		},
	},
}

// splitType separates what format_type renders into a type name and its width.
// The parenthetical is not always at the end: "timestamp(3) without time zone".
func splitType(rendered string) (kind, length string) {
	open := strings.Index(rendered, "(")
	if open < 0 {
		return rendered, ""
	}
	close := strings.Index(rendered[open:], ")")
	if close < 0 {
		return rendered, ""
	}
	close += open
	length = rendered[open : close+1]
	kind = strings.TrimSpace(rendered[:open] + rendered[close+1:])
	return kind, length
}

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
// lifts the primary key out.
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
// each.
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
