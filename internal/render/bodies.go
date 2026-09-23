package render

import (
	"strings"

	"github.com/branow/dbmap/internal/catalog"
)

// Truncated marks a cut body: a procedure's writes are usually at the bottom,
// so a reader must know the tail is missing.
const Truncated = "-- truncated by dbmap"

// Body renders one module file: a header, then the definition verbatim. The
// definition arrives already redacted.
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
