package render

import (
	"math"
	"strconv"
	"strings"

	"github.com/branow/dbmap/internal/catalog"
)

const tab = "\t"

// breakers are the characters that would end a cell or row early. A
// model-written description can contain any of them, so every cell passes
// through here.
var breakers = strings.NewReplacer("\t", " ", "\n", " ", "\r", " ")

func clean(value string) string { return strings.TrimSpace(breakers.Replace(value)) }

// TSV renders a header and its rows. The header is written as given; every
// other cell is cleaned.
func TSV(header []string, rows [][]string) string {
	var out strings.Builder
	out.WriteString(strings.Join(header, tab))
	for _, cells := range rows {
		out.WriteString("\n")
		for i, cell := range cells {
			if i > 0 {
				out.WriteString(tab)
			}
			out.WriteString(clean(cell))
		}
	}
	out.WriteString("\n")
	return out.String()
}

// Size renders a kilobyte count in the unit a human would use.
func Size(kb int64) string {
	switch {
	case kb >= 1048576:
		return strconv.FormatInt(int64(math.Round(float64(kb)/1048576)), 10) + " GB"
	case kb >= 1024:
		return strconv.FormatInt(int64(math.Round(float64(kb)/1024)), 10) + " MB"
	default:
		return strconv.FormatInt(kb, 10) + " KB"
	}
}

// Params renders a call signature inline — "@CustomerID int, @Since datetime" —
// minus the return row a catalog emits with an empty name.
func Params(entry catalog.Entry) string {
	var rendered []string
	for _, p := range entry.Structure.Parameters {
		if p.Name == catalog.Returns {
			continue
		}
		text := p.Name + " " + p.Type
		if p.Output {
			text += " out"
		}
		rendered = append(rendered, text)
	}
	return strings.Join(rendered, ", ")
}

// Returns lifts a function's return type out of its parameters. A
// table-valued function has no return row, and returns a table.
func Returns(entry catalog.Entry) string {
	for _, p := range entry.Structure.Parameters {
		if p.Name == catalog.Returns {
			return p.Type
		}
	}
	return "table"
}
