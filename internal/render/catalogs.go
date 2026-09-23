// Package render writes the index files an agent reads, and reads the next
// build's staleness state back out of them.
package render

import (
	"strconv"

	"github.com/branow/dbmap/internal/catalog"
)

// Trailer is the staleness tail every catalog row ends with.
var Trailer = []string{"modified", "fingerprint", "description"}

// Catalog describes how one kind is written: its file, the headings between the
// name and the shared trailer, and how to read those off an entry.
type Catalog struct {
	File   string
	Kind   catalog.Kind
	Facts  []string
	Values func(catalog.Entry) []string
	// Detail marks a kind that also gets a per-object column file.
	Detail bool
}

// Catalogs is the output shape as data: adding a kind is a row here.
var Catalogs = []Catalog{
	{
		File:   "tables.tsv",
		Kind:   catalog.Table,
		Facts:  []string{"rows", "size", "triggers", "cols"},
		Detail: true,
		Values: func(e catalog.Entry) []string {
			return []string{
				strconv.FormatInt(e.Object.Rows, 10),
				Size(e.Object.KB),
				strconv.Itoa(e.Object.Triggers),
				strconv.Itoa(len(e.Structure.Columns)),
			}
		},
	},
	{
		File:   "views.tsv",
		Kind:   catalog.View,
		Facts:  []string{"cols"},
		Detail: true,
		Values: func(e catalog.Entry) []string {
			return []string{strconv.Itoa(len(e.Structure.Columns))}
		},
	},
	{
		File:  "procedures.tsv",
		Kind:  catalog.Procedure,
		Facts: []string{"params"},
		Values: func(e catalog.Entry) []string {
			return []string{Params(e)}
		},
	},
	{
		File:  "functions.tsv",
		Kind:  catalog.Function,
		Facts: []string{"returns", "params"},
		Values: func(e catalog.Entry) []string {
			return []string{Returns(e), Params(e)}
		},
	},
	{
		File:  "synonyms.tsv",
		Kind:  catalog.Synonym,
		Facts: []string{"target"},
		Values: func(e catalog.Entry) []string {
			return []string{e.Structure.Target}
		},
	},
}

// Header is the first line of a catalog file.
func Header(c Catalog) []string {
	header := make([]string, 0, len(c.Facts)+len(Trailer)+1)
	header = append(header, "name")
	header = append(header, c.Facts...)
	return append(header, Trailer...)
}

func row(c Catalog, entry catalog.Entry) []string {
	cells := make([]string, 0, len(c.Facts)+len(Trailer)+1)
	cells = append(cells, entry.Key())
	cells = append(cells, c.Values(entry)...)
	return append(cells, entry.Object.Modified.String(), entry.Fingerprint, entry.Description)
}

// rowsIndex is where a row count sits in a catalog's row, or -1 if it has none.
func rowsIndex(c Catalog) int {
	for i, fact := range c.Facts {
		if fact == "rows" {
			return i + 1
		}
	}
	return -1
}

var detailed = func() map[catalog.Kind]bool {
	byKind := make(map[catalog.Kind]bool, len(Catalogs))
	for _, c := range Catalogs {
		byKind[c.Kind] = c.Detail
	}
	return byKind
}()
