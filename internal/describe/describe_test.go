package describe

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/branow/dbmap/internal/catalog"
	"github.com/branow/dbmap/llm"
)

// fake replies with whatever each call is scripted to return, so every test
// here runs without a model.
type fake struct {
	replies []any // string (raw json), reply, or error
	prompts []string
	at      int
}

func (f *fake) Name() string { return "fake" }

func (f *fake) Complete(_ context.Context, req llm.Request) (*llm.Response, error) {
	f.prompts = append(f.prompts, req.Prompt)
	if f.at >= len(f.replies) {
		return &llm.Response{Structured: json.RawMessage(`{"objects":[]}`)}, nil
	}
	scripted := f.replies[f.at]
	f.at++

	switch v := scripted.(type) {
	case error:
		return nil, v
	case string:
		return &llm.Response{Structured: json.RawMessage(v)}, nil
	case reply:
		data, _ := json.Marshal(v)
		return &llm.Response{Structured: data}, nil
	}
	return nil, errors.New("bad script")
}

func entry(kind catalog.Kind, name string, columns ...catalog.Column) catalog.Entry {
	return catalog.Entry{
		Object:    catalog.Object{Schema: "dbo", Name: name, Kind: kind, Rows: int64(len(columns))},
		Structure: catalog.Structure{Columns: columns},
	}
}

func input(kind catalog.Kind, name string) Input {
	return Input{Entry: entry(kind, name, catalog.Column{Name: "ID", Type: "int"}), Database: "AppCore"}
}

// A field named `description` whose schema text reads like a noun phrase gets
// filled with a description OF THE FIELD. This test stops the wording drifting
// back.
func TestTheOutputFieldIsSentenceAndItsTextIsAnOrder(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal(Schema, &schema); err != nil {
		t.Fatalf("Schema is not valid JSON: %v", err)
	}

	items := schema["properties"].(map[string]any)["objects"].(map[string]any)["items"].(map[string]any)
	props := items["properties"].(map[string]any)

	if _, wrong := props["description"]; wrong {
		t.Fatal("the output schema has a field named `description`; models fill it with a description of the field")
	}
	field, ok := props["sentence"].(map[string]any)
	if !ok {
		t.Fatal("the output schema has no `sentence` field")
	}

	text := field["description"].(string)
	// The text must read as an order, and must carry the prohibition that is
	// the half that actually did the work.
	for _, required := range []string{"verbatim", "Never describe what the sentence would say"} {
		if !strings.Contains(text, required) {
			t.Fatalf("the `sentence` schema text lost %q; it now reads: %q", required, text)
		}
	}

	required := items["required"].([]any)
	if !contains(required, "name") {
		t.Fatal("`name` is not required; answers could not be matched by name")
	}
}

// The second lesson: listing requirements invites a small model to restate the
// list. A verb-first order plus worked examples fixed it. Pinned by parsing the
// source, so the constant cannot be quietly emptied.
func TestTheVerbFirstInstructionCannotBeWeakened(t *testing.T) {
	for _, required := range []string{
		"starts with a verb",
		"Do not restate this instruction",
		"do not write the word summary or analysis",
	} {
		if !strings.Contains(verbFirst, required) {
			t.Fatalf("the verb-first instruction lost %q; it now reads: %q", required, verbFirst)
		}
	}

	// Every describable kind must carry the instruction and at least one
	// worked example, because the examples are what stop the restating.
	for kind, prompt := range Prompts {
		rendered := prompt(input(kind, "Thing"))
		if !strings.Contains(rendered, verbFirst) {
			t.Errorf("the %s prompt does not carry the verb-first instruction", kind)
		}
		if !strings.Contains(rendered, "Good: ") {
			t.Errorf("the %s prompt carries no worked example", kind)
		}
	}
}

// The comment recording why the field is named `sentence` must survive, because
// the next person to touch this file will otherwise see only an odd name.
func TestTheReasonForTheFieldNameIsRecordedInSource(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "describe.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	var text strings.Builder
	ast.Inspect(file, func(n ast.Node) bool {
		if group, ok := n.(*ast.CommentGroup); ok {
			text.WriteString(group.Text())
		}
		return true
	})
	// Collapse whitespace first: a comment group joins its lines with newlines,
	// so a phrase that happens to wrap would otherwise fail this test and send
	// the reader looking for a deletion that never happened.
	prose := strings.Join(strings.Fields(text.String()), " ")
	for _, required := range []string{"description of the field", "sentence"} {
		if !strings.Contains(prose, required) {
			t.Fatalf("the comment explaining the field name lost %q", required)
		}
	}
}

func TestBatchingIsByCostNotCount(t *testing.T) {
	long := Item{Key: "dbo.Big", Prompt: strings.Repeat("x", 30000)}
	small := func(n int) Item { return Item{Key: "dbo.S", Prompt: strings.Repeat("y", n)} }

	t.Run("the character budget cuts before the count cap", func(t *testing.T) {
		batches := Batch([]Item{long, long, long}, BatchChars, BatchMax)
		if len(batches) != 3 {
			t.Fatalf("got %d batches, want 3: three 30k prompts cannot share a 40k budget", len(batches))
		}
	})

	t.Run("the count cap cuts before the character budget", func(t *testing.T) {
		items := make([]Item, BatchMax+3)
		for i := range items {
			items[i] = small(10)
		}
		batches := Batch(items, BatchChars, BatchMax)
		if len(batches) != 2 || len(batches[0]) != BatchMax {
			t.Fatalf("count cap not applied: %d batches, first holds %d", len(batches), len(batches[0]))
		}
	})

	t.Run("an over-budget object goes alone rather than being dropped", func(t *testing.T) {
		huge := Item{Key: "dbo.Huge", Prompt: strings.Repeat("z", BatchChars*2)}
		batches := Batch([]Item{small(10), huge, small(10)}, BatchChars, BatchMax)

		var found bool
		for _, b := range batches {
			for _, item := range b {
				if item.Key != "dbo.Huge" {
					continue
				}
				found = true
				if len(b) != 1 {
					t.Fatalf("the over-budget object shares a batch with %d others", len(b)-1)
				}
			}
		}
		if !found {
			t.Fatal("the over-budget object was dropped")
		}
	})

	t.Run("nothing is lost or duplicated", func(t *testing.T) {
		items := make([]Item, 37)
		for i := range items {
			items[i] = Item{Key: string(rune('a' + i%26)), Prompt: strings.Repeat("q", 5000)}
		}
		total := 0
		for _, b := range Batch(items, BatchChars, BatchMax) {
			total += len(b)
		}
		if total != len(items) {
			t.Fatalf("batches hold %d items, want %d", total, len(items))
		}
	})
}

// A model that reorders its reply must still land every sentence on the right
// object. Matching by position would silently mislabel a whole database.
func TestAnswersAreMatchedByNameNotPosition(t *testing.T) {
	inputs := []Input{input(catalog.Table, "Orders"), input(catalog.Table, "Customers")}
	f := &fake{replies: []any{reply{Objects: []answer{
		{Name: "dbo.Customers", Sentence: "Holds one row per customer."},
		{Name: "dbo.Orders", Sentence: "Holds one row per placed order."},
	}}}}

	result, err := All(context.Background(), f, inputs, Options{})
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if got := result.Sentences["dbo.Orders"]; !strings.Contains(got, "order") {
		t.Fatalf("dbo.Orders got the wrong sentence: %q", got)
	}
	if got := result.Sentences["dbo.Customers"]; !strings.Contains(got, "customer") {
		t.Fatalf("dbo.Customers got the wrong sentence: %q", got)
	}
}

// A skipped object is reported. A blank description in the index would look
// like a described object with nothing to say.
func TestASkippedObjectIsReportedNotBlank(t *testing.T) {
	inputs := []Input{input(catalog.Table, "Orders"), input(catalog.Table, "Customers")}
	f := &fake{replies: []any{reply{Objects: []answer{
		{Name: "dbo.Orders", Sentence: "Holds one row per placed order."},
	}}}}

	result, _ := All(context.Background(), f, inputs, Options{})

	if len(result.Missing) != 1 || result.Missing[0] != "dbo.Customers" {
		t.Fatalf("missing = %v, want [dbo.Customers]", result.Missing)
	}
	if _, present := result.Sentences["dbo.Customers"]; present {
		t.Fatal("a skipped object got a sentence")
	}
}

// A partial index beats none.
func TestAFailedBatchIsSkippedAndTheRestSurvive(t *testing.T) {
	var inputs []Input
	for _, name := range []string{"A", "B", "C", "D"} {
		inputs = append(inputs, input(catalog.Table, name))
	}
	f := &fake{replies: []any{
		errors.New("rate limited"),
		reply{Objects: []answer{{Name: "dbo.C", Sentence: "Holds C."}, {Name: "dbo.D", Sentence: "Holds D."}}},
	}}

	result, err := All(context.Background(), f, inputs, Options{Max: 2})
	if err != nil {
		t.Fatalf("a failed batch aborted the run: %v", err)
	}
	if len(result.Failed) != 1 {
		t.Fatalf("failed batches = %d, want 1", len(result.Failed))
	}
	if len(result.Sentences) != 2 {
		t.Fatalf("got %d sentences, want the 2 from the surviving batch", len(result.Sentences))
	}
}

func TestUnreadableOutputIsAFailedBatchNotACrash(t *testing.T) {
	f := &fake{replies: []any{`{"objects": "not an array"}`}}

	result, err := All(context.Background(), f, []Input{input(catalog.Table, "Orders")}, Options{})
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(result.Failed) != 1 {
		t.Fatalf("failed = %d, want 1", len(result.Failed))
	}
	if len(result.Missing) != 1 {
		t.Fatal("the object in the unreadable batch was not reported missing")
	}
}

func TestBodyIsCappedAndTheCutIsVisible(t *testing.T) {
	long := strings.Repeat("a", BodyChars+500)

	got := Body(long)

	if !strings.HasSuffix(got, Truncated) {
		t.Fatal("a truncated body does not say so; the model would read a fragment as whole")
	}
	if n := len([]rune(strings.TrimSuffix(got, Truncated))); n != BodyChars {
		t.Fatalf("body kept %d runes, want %d", n, BodyChars)
	}
	if short := "SELECT 1"; Body(short) != short {
		t.Fatalf("a short body was altered: %q", Body(short))
	}
}

// Empty tables are the canary for prompt bugs: a table with rows gets a sample
// that papers over a missing-structure bug, an empty one produces visible
// garbage. So an empty table must still yield a usable prompt.
func TestAnEmptyTableStillProducesAUsablePrompt(t *testing.T) {
	in := Input{
		Entry:    entry(catalog.Table, "Empty"),
		Database: "AppCore",
	}

	prompt, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(prompt, "dbo.Empty") {
		t.Fatal("prompt does not name the object")
	}
	if !strings.Contains(prompt, "Columns:") {
		t.Fatal("prompt carries no column section; a model would invent a shape")
	}
	if strings.Contains(prompt, "Sample rows:") {
		t.Fatal("prompt claims sample rows for a table with none")
	}
}

// A missing body is stated, never left blank, for the same reason.
func TestAnAbsentBodyIsStatedNotBlank(t *testing.T) {
	if got := Body("   "); got == "" || !strings.Contains(got, "no body available") {
		t.Fatalf("an absent body rendered as %q", got)
	}
}

func TestSynonymsAreNeverDescribed(t *testing.T) {
	if Describable(catalog.Synonym) {
		t.Fatal("a synonym is describable; it is indexed for its target, not for a sentence")
	}
	items, err := Prepare([]Input{input(catalog.Synonym, "Legacy"), input(catalog.Table, "Orders")})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if len(items) != 1 || items[0].Key != "dbo.Orders" {
		t.Fatalf("Prepare returned %+v, want only the table", items)
	}
}

func TestLookupTablesAreRecorded(t *testing.T) {
	f := &fake{replies: []any{reply{Objects: []answer{
		{Name: "dbo.OrderStatuses", Sentence: "Maps order status codes to their descriptions.", Lookup: true},
	}}}}

	result, _ := All(context.Background(), f, []Input{input(catalog.Table, "OrderStatuses")}, Options{})

	if !result.Lookups["dbo.OrderStatuses"] {
		t.Fatal("a lookup table was not recorded as one")
	}
}

func contains(values []any, want string) bool {
	for _, v := range values {
		if s, ok := v.(string); ok && s == want {
			return true
		}
	}
	return false
}
