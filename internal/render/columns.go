package render

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/branow/dbmap/internal/catalog"
)

// Columns renders one table or view file: a header, the columns, then key and
// indexes as comments.
func Columns(entry catalog.Entry) string {
	lines := []string{"# " + entry.Key() + " @" + entry.Fingerprint}
	if entry.Object.Kind == catalog.Table {
		lines = append(lines, "# rows "+strconv.FormatInt(entry.Object.Rows, 10)+
			"  size "+Size(entry.Object.KB)+
			"  triggers "+strconv.Itoa(entry.Object.Triggers))
	}
	if entry.Description != "" {
		lines = append(lines, "# "+clean(entry.Description))
	}

	rows := make([][]string, 0, len(entry.Structure.Columns))
	for _, c := range entry.Structure.Columns {
		rows = append(rows, []string{c.Name, c.Type + c.Length, nullability(c.Nullable), extra(c)})
	}
	table := TSV([]string{"column", "type", "null", "extra"}, rows)
	body := strings.TrimRightFunc(table, unicode.IsSpace)
	lines = append(lines, body)

	if len(entry.Structure.PrimaryKey) > 0 {
		lines = append(lines, "# pk "+strings.Join(entry.Structure.PrimaryKey, ", "))
	}
	for _, i := range entry.Structure.Indexes {
		line := "# index " + i.Name + " (" + strings.Join(i.Columns, ", ") + ")"
		if i.Unique {
			line += " unique"
		}
		lines = append(lines, line)
	}
	for _, f := range entry.Structure.ForeignKeys {
		lines = append(lines, "# fk "+f.Column+" -> "+f.References)
	}

	return strings.Join(lines, "\n") + "\n"
}

func nullability(nullable bool) string {
	if nullable {
		return "null"
	}
	return "not null"
}

func extra(c catalog.Column) string {
	var marks []string
	if c.Identity {
		marks = append(marks, "identity")
	}
	if c.Computed {
		marks = append(marks, "computed")
	}
	return strings.Join(marks, " ")
}
