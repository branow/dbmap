// Package describe turns a fetched object into one sentence, using a model with
// no tools and everything it needs in the prompt.
//
// Two decisions in here were expensive to learn and are pinned by tests, not
// only by comments. Read them before changing any wording.
//
//   - The output field is named `sentence`, never `description`. With a field
//     called `description` whose schema text read like a noun phrase, every
//     model filled it with a description OF THE FIELD — "One-sentence summary of
//     what dbo.PromoGet does" — and the better a model followed instructions the
//     more faithfully it did so.
//   - The instruction is verb-first and carries worked good and bad examples.
//     Listing requirements invites a small model to restate the list instead of
//     answering it.
//
// The small model is sufficient here. That is measured, not assumed, and this
// package does not default to a larger one.
package describe

import (
	"fmt"
	"strings"

	"github.com/branow/dbmap/internal/catalog"
)

// BodyChars caps a module body on its way into a prompt. It sends 90% of a
// measured corpus whole; the cap exists only for the tail. The cache keeps more,
// deliberately, so the stored copy is always the fuller one.
//
// A procedure is not summarisable from its opening — the writes are usually at
// the bottom — so this is a cap, never a preview.
const BodyChars = 16000

// Truncated marks a body the prompt could not carry whole, so the model can see
// that it is reading a fragment rather than silently treating the tail as absent.
const Truncated = "\n-- truncated"

// verbFirst is the instruction that stops a small model restating the task.
// Naming the failure mode is what fixed it; the good/bad examples per kind do
// the rest.
const verbFirst = "Answer in one sentence that starts with a verb, present tense, under 25 words.\n" +
	"Do not restate this instruction, do not write the word summary or analysis, " +
	"and do not repeat the object name."

// Prompt builds the prompt for one object. Prompts live in a table keyed by
// kind, so a new kind is a row rather than a branch.
type Prompt func(Input) string

// Input is everything a prompt may read about one object.
type Input struct {
	Entry catalog.Entry
	// Database names the database the object lives in, for context only.
	Database string
	// Definition is the object's body, already redacted. Empty for a table.
	Definition string
	// Sample is the rendered sample block, empty when the table was not
	// sampled or nothing in it could be read.
	Sample string
}

// Key is the name answers are matched back by.
func (i Input) Key() string { return i.Entry.Key() }

// Prompts is the whole prompt policy, one row per kind. A synonym has no row:
// it is indexed for its target, not for a generated sentence about it.
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
			`Good: "Inserts a promo code into dbo.PromoConfigurations, replacing any existing row for the same code."`,
			`Good: "Reads order totals from dbo.Orders and dbo.OrderDetails for a single customer."`,
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
			`Good: "Returns the commission run id covering a given period and period type."`,
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

// Body renders a definition for a prompt, capped and marked when it is cut.
//
// An absent body is stated rather than left blank: a model shown nothing where
// a body should be will invent one, and an empty table is the canary for
// exactly this class of prompt bug.
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
