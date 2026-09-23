// Package describe turns a fetched object into one sentence, using a model with
// no tools and everything it needs in the prompt. Two wordings here are pinned
// by tests: the output field name (see sentenceField in describe.go) and the
// verb-first instruction below.
package describe

import (
	"fmt"
	"strings"

	"github.com/branow/dbmap/internal/catalog"
)

// BodyChars caps a module body on its way into a prompt; the cache keeps more.
const BodyChars = 16000

// Truncated marks a body the prompt could not carry whole, so the model does
// not read the missing tail as absent.
const Truncated = "\n-- truncated"

// verbFirst stops a small model restating the task; naming the failure mode is
// what fixed it, and the per-kind examples do the rest.
const verbFirst = "Answer in one sentence that starts with a verb, present tense, under 25 words.\n" +
	"Do not restate this instruction, do not write the word summary or analysis, " +
	"and do not repeat the object name."

// Prompt builds the prompt for one object.
type Prompt func(Input) string

// Input is everything a prompt may read about one object.
type Input struct {
	Entry    catalog.Entry
	Database string
	// Definition is the object's body, already redacted. Empty for a table.
	Definition string
	// Sample is the rendered sample block, empty when nothing was readable.
	Sample string
}

// Key is the name answers are matched back by.
func (i Input) Key() string { return i.Entry.Key() }

// Prompts is the prompt policy, one row per kind. A synonym has no row: it is
// indexed for its target.
var Prompts = map[catalog.Kind]Prompt{
	catalog.Table: func(in Input) string {
		parts := []string{
			fmt.Sprintf("Table %s in the %s database.", in.Key(), in.Database),
			fmt.Sprintf("%d rows.", in.Entry.Object.Rows),
			"",
			"Columns:",
			columns(in.Entry),
			keys(in.Entry),
		}
		if in.Sample != "" {
			parts = append(parts, "", "Sample rows:", in.Sample)
		}
		return join(append(parts,
			"",
			"Describe what this table holds, naming the grain — what one row is.",
			"If it is a lookup of codes and their meanings, say so and set lookup=true.",
			"Do not list the columns back; they are already indexed.",
			verbFirst,
			`Good: "Maps order status codes to their descriptions, one row per status."`,
			`Good: "Holds one row per placed order, keyed by OrderID."`,
		))
	},

	catalog.View: func(in Input) string {
		return join([]string{
			fmt.Sprintf("View %s in the %s database.", in.Key(), in.Database),
			"",
			"Columns:",
			columns(in.Entry),
			"",
			"Definition:",
			Body(in.Definition),
			"",
			"Write one sentence saying what this view exposes and from where.",
			verbFirst,
			`Good: "Exposes open orders joined to their customer, one row per order."`,
		})
	},

	catalog.Procedure: func(in Input) string {
		return join([]string{
			fmt.Sprintf("Stored procedure %s in the %s database.", in.Key(), in.Database),
			parameters(in.Entry),
			"",
			"Body:",
			Body(in.Definition),
			"",
			"Describe what this procedure does, naming the tables it touches.",
			verbFirst,
			`Good: "Inserts a discount code into dbo.Discounts, replacing any existing row for the same code."`,
			`Good: "Reads order totals from dbo.Orders and dbo.OrderLines for a single customer."`,
			`Bad: "Stored procedure analysis: functionality, operation type, and data sources."`,
		})
	},

	catalog.Function: func(in Input) string {
		return join([]string{
			fmt.Sprintf("Function %s in the %s database.", in.Key(), in.Database),
			parameters(in.Entry),
			"",
			"Body:",
			Body(in.Definition),
			"",
			"Describe what this function returns.",
			verbFirst,
			`Good: "Returns the tax rate applying to a given region and effective date."`,
		})
	},
}

// Describable reports whether this kind is sent to a model at all.
func Describable(kind catalog.Kind) bool {
	_, ok := Prompts[kind]
	return ok
}

// Build renders the prompt for one input, or reports that its kind has none.
func Build(in Input) (string, error) {
	prompt, ok := Prompts[in.Entry.Object.Kind]
	if !ok {
		return "", &UnpromptedKindError{Kind: in.Entry.Object.Kind, Key: in.Key()}
	}
	return prompt(in), nil
}

// UnpromptedKindError reports a kind with no prompt row.
type UnpromptedKindError struct {
	Kind catalog.Kind
	Key  string
}

func (e *UnpromptedKindError) Error() string {
	return fmt.Sprintf("no prompt defined for kind %q", string(e.Kind))
}

// Body renders a definition for a prompt, capped and marked when cut. An absent
// body is stated, never left blank: a model shown nothing there invents one.
func Body(definition string) string {
	text := strings.TrimSpace(definition)
	if text == "" {
		return "(no body available — compiled or encrypted)"
	}
	runes := []rune(text)
	if len(runes) <= BodyChars {
		return text
	}
	return string(runes[:BodyChars]) + Truncated
}

func columns(entry catalog.Entry) string {
	if len(entry.Structure.Columns) == 0 {
		return "  (none recorded)"
	}
	lines := make([]string, 0, len(entry.Structure.Columns))
	for _, c := range entry.Structure.Columns {
		line := "  " + c.Name + " " + c.Type + c.Length
		if !c.Nullable {
			line += " not null"
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func keys(entry catalog.Entry) string {
	if len(entry.Structure.PrimaryKey) == 0 {
		return ""
	}
	return "\nPrimary key: " + strings.Join(entry.Structure.PrimaryKey, ", ")
}

func parameters(entry catalog.Entry) string {
	var names []string
	for _, p := range entry.Structure.Parameters {
		if p.Name == catalog.Returns {
			continue
		}
		names = append(names, p.Name+" "+p.Type)
	}
	if len(names) == 0 {
		return "No parameters."
	}
	return "Parameters: " + strings.Join(names, ", ")
}

func join(parts []string) string {
	return strings.Join(parts, "\n")
}
