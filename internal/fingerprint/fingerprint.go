// Package fingerprint decides what "changed" means for each kind of object.
//
// Two different signals guard two different costs. A modify signal decides
// whether to FETCH: free, never misses a real change, but over-reports, because
// a release that ALTERs 58 procedures bumps all 58 dates even when most are
// byte-identical to what was already deployed. A content fingerprint decides
// whether to DESCRIBE. Fetching is seconds; describing is an LLM call per
// object, so the exact check guards the expensive stage and the loose one
// guards the cheap stage.
//
// A module fingerprints over its definition. A table has no definition, so it
// fingerprints over the structure a reader would need — columns with their
// types and nullability, keys, indexes, trigger count — plus its sample depth.
//
// Sample row VALUES are deliberately absent. They change on every run against a
// live database and would leave every table permanently dirty.
package fingerprint

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"unicode"

	"github.com/branow/dbmap/internal/catalog"
)

// SampleRows is how many rows the describer is sent, and therefore the row
// count above which a table growing tells the describer nothing new: the first
// 25 rows of a 40-row table and of a 60-row table are the same 25 rows.
//
// 25 because 148 of the 491 non-empty measured tables hold 25 rows or fewer —
// the lookup and enum tables, where every row is a value worth naming and the
// sample is the whole table. Past 25 the distribution flattens: raising it to
// 30 covers five more tables.
const SampleRows = 25

// digits is how much of the hash is kept. 16 hex characters is 64 bits, which
// is collision-proof enough for a few thousand objects and short enough to sit
// in a TSV row a human reads.
const digits = 16

// SampleDepth is how many rows the describer actually gets to see. Below
// SampleRows a table growing by one row changes what the model reads, so the
// description is stale; at or above it, growth changes nothing.
func SampleDepth(rows int64) int64 {
	if rows < 0 {
		return 0
	}
	if rows > SampleRows {
		return SampleRows
	}
	return rows
}

// Sum is the hash every fingerprint here is taken with.
func Sum(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])[:digits]
}

// Normalize strips the differences deployment tools introduce without the code
// differing: line endings, trailing whitespace per line, and surrounding blank
// space. A tool rewriting a file to CRLF must not read as an edit.
func Normalize(definition string) string {
	lines := strings.Split(strings.ReplaceAll(definition, "\r\n", "\n"), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRightFunc(line, unicode.IsSpace)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// Module fingerprints a view, procedure or function over its body.
func Module(definition string) string { return Sum(Normalize(definition)) }

// CanonicalTable is the exact text a table's fingerprint is taken over. It is
// built as explicit lines rather than by hashing a struct or its JSON, so the
// hash cannot move when an unrelated field is added to the structure the fetch
// stage happens to return.
func CanonicalTable(object catalog.Object, structure catalog.Structure) string {
	lines := make([]string, 0, len(structure.Columns)+len(structure.Indexes)+4)
	for _, c := range structure.Columns {
		line := "c " + c.Name + " " + c.Type + c.Length + " " + nullability(c.Nullable)
		if c.Identity {
			line += " id"
		}
		lines = append(lines, line)
	}
	for _, k := range structure.PrimaryKey {
		lines = append(lines, "pk "+k)
	}
	for _, f := range structure.ForeignKeys {
		lines = append(lines, "fk "+f.Column+" -> "+f.References)
	}
	for _, i := range structure.Indexes {
		line := "ix " + i.Name + " " + strings.Join(i.Columns, ",")
		if i.Unique {
			line += " u"
		}
		lines = append(lines, line)
	}
	lines = append(lines, "tg "+strconv.Itoa(object.Triggers))
	lines = append(lines, "rows "+strconv.FormatInt(SampleDepth(object.Rows), 10))
	return strings.Join(lines, "\n")
}

func nullability(nullable bool) string {
	if nullable {
		return "null"
	}
	return "notnull"
}

// Table fingerprints a table over its canonical structure.
func Table(object catalog.Object, structure catalog.Structure) string {
	return Sum(CanonicalTable(object, structure))
}

func module(_ catalog.Object, structure catalog.Structure) string {
	return Module(structure.Definition)
}

func synonym(_ catalog.Object, structure catalog.Structure) string {
	return Sum(structure.Target)
}

// fingerprinters dispatches on kind so callers never branch on one themselves.
var fingerprinters = map[catalog.Kind]func(catalog.Object, catalog.Structure) string{
	catalog.Table:     Table,
	catalog.View:      module,
	catalog.Procedure: module,
	catalog.Function:  module,
	catalog.Synonym:   synonym,
}

// Of returns the content fingerprint for one object. A kind with no rule is
// refused rather than guessed at: a wrong hash would either redescribe an
// object on every build or never describe it again.
func Of(object catalog.Object, structure catalog.Structure) (string, error) {
	fn, ok := fingerprinters[object.Kind]
	if !ok {
		return "", UnknownKindError{Kind: object.Kind}
	}
	return fn(object, structure), nil
}

// UnknownKindError reports a kind the fingerprint table has no rule for.
type UnknownKindError struct {
	Kind catalog.Kind
}

func (e UnknownKindError) Error() string {
	return "fingerprint: no rule for kind " + string(e.Kind)
}
