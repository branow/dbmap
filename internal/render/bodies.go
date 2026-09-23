package render

import (
	"strings"

	"github.com/branow/dbmap/internal/catalog"
)

// Truncated marks a body the engine could not carry whole, so a reader knows it
// is looking at a fragment rather than a short procedure.
const Truncated = "-- truncated by dbmap"

// Body renders one module file: a header naming the object, its staleness and
// its description, then the definition verbatim.
//
// Bodies are written for the same reason columns are. A description is one
// sentence and names an object's main tables, not all of them, so it cannot
// answer "what writes to this table" — grepping the definitions can, without a
// database connection, which is the whole premise of the index.
//
// The text is already redacted: it passed through the redactor on arrival from
// the engine, so a credential never reaches this file.
func Body(entry catalog.Entry) string {
	lines := []string{"-- " + entry.Key() + " @" + entry.Fingerprint}
	if entry.Object.Modified.Present() {
		lines = append(lines, "-- modified "+string(entry.Object.Modified))
	}
	if entry.Description != "" {
		lines = append(lines, "-- "+clean(entry.Description))
	}

	definition := strings.TrimRight(entry.Structure.Definition, " \t\n\r")
	lines = append(lines, "", definition)
	if len(definition) >= catalog.MaxDefinition {
		lines = append(lines, Truncated)
	}
	return strings.Join(lines, "\n") + "\n"
}
