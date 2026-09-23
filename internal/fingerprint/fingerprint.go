// Package fingerprint decides what "changed" means for each kind of object.
//
// Staleness is two-tier because two signals guard two very different costs. A
// modify signal gates FETCH: free and never misses a change, but over-reports.
// A content fingerprint gates DESCRIBE, which is an LLM call per object, so the
// exact check guards the expensive stage and the loose one the cheap stage.
//
// A module fingerprints over its definition; a table has none, so it
// fingerprints over the structure a reader would need plus its sample depth.
// Sample row VALUES are deliberately absent: they change on every run against a
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

// SampleRows is how many rows the describer is sent, and so the row count above
// which a table growing tells it nothing new. 25 is chosen from the measured
// row-count distribution (see docs/DESIGN.md).
const SampleRows = 25

// digits keeps 64 bits of the hash: collision-proof enough for a few thousand
// objects, short enough to sit in a TSV row a human reads.
const digits = 16

// SampleDepth is how many rows the describer actually gets to see. Below
// SampleRows growth changes what the model reads; at or above it, growth
// changes nothing, which is what keeps a growing table from redescribing.
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

// Normalize strips line endings, per-line trailing whitespace and surrounding
// blank space, so a deployment tool rewriting a file to CRLF does not read as
// an edit.
func Normalize(definition string) string {
	lines := strings.Split(strings.ReplaceAll(definition, "\r\n", "\n"), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRightFunc(line, unicode.IsSpace)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// Module fingerprints a view, procedure or function over its body.
func Module(definition string) string { return Sum(Normalize(definition)) }

// CanonicalTable is the exact text a table's fingerprint is taken over. Built
// as explicit lines rather than by hashing the struct, so the hash cannot move
// when an unrelated field is added to Structure.
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
