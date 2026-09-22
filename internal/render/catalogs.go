// Package render writes the index files an agent reads, and reads the next
// build's staleness state back out of them.
//
// Shape, per environment and database:
//
//	tables.tsv                     one row per table: size, trigger count
//	views.tsv                      one row per view
//	procedures.tsv                 one row per procedure, parameters inline
//	functions.tsv                  one row per function, return type inline
//	synonyms.tsv                   one row per synonym and what it points at
//	columns/<schema>.<name>.tsv    per table and view: every column, the
//	                               primary key and the indexes
//
// Every catalog carries the modify signal and the fingerprint beside the
// description, so the next build reads its own staleness state straight out of
// the files it wrote and there is no second file to drift out of step — and the
// modify signal earns its place for a reader too, since a procedure untouched
// since 2018 says something a description cannot. Nothing derived is stored: a
// table's sample depth is min(rows, 25) and rows is already there.
//
// Columns get a file each because a reader needs all of them; procedures and
// functions do not, because their parameters fit on one line and a body is not
// something the index reproduces. There is no foreign key catalog: three
// measured databases declare two foreign keys between them, so a join here is a
// naming convention and the index does not pretend otherwise.
package render

import (
	"strconv"

	"github.com/branow/dbmap/internal/catalog"
)

// Trailer is the staleness tail every catalog row ends with.
var Trailer = []string{"modified", "fingerprint", "description"}

// Catalog is one row of the catalog table: which file a kind is written to,
// the columns specific to that kind, and how to read them off an entry.
type Catalog struct {
	File string
	Kind catalog.Kind
	// Facts are the column headings between the name and the shared trailer.
	Facts []string
	// Values reads those facts off an entry, in Facts order.
	Values func(catalog.Entry) []string
	// Detail marks a kind whose columns are worth a file of their own.
	Detail bool
}

// Catalogs is the output shape as data. Adding a kind is a row here, not a
// branch somewhere.
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

// rowsIndex is where a row count sits in a catalog's row, for the reader that
// needs it back, or -1 for a catalog that stores none.
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
