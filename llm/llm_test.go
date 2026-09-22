package llm

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func TestMiddlewareComposesInAnyOrder(t *testing.T) {
	dir := t.TempDir()
	orders := []struct {
		name  string
		build func(Client, *Usage) Client
	}{
		{"cache outermost", func(c Client, u *Usage) Client {
			return WithCache(WithConcurrency(WithRetry(WithUsage(c, u), RetryConfig{}), 2), dir)
		}},
		{"retry outermost", func(c Client, u *Usage) Client {
			return WithRetry(WithUsage(WithConcurrency(WithCache(c, dir), 2), u), RetryConfig{})
		}},
	}
	for _, order := range orders {
		t.Run(order.name, func(t *testing.T) {
			inner := &fake{name: "anthropic", steps: []step{
				{err: &Error{Class: ClassUnavailable}},
				{resp: &Response{Structured: ok().Structured, Usage: Usage{InputTokens: 3}}},
			}}
			var total Usage
			client := order.build(inner, &total)
			if client.Name() != "anthropic" {
				t.Fatalf("Name = %q", client.Name())
			}
			if _, err := client.Complete(context.Background(), request()); err != nil {
				t.Fatalf("err = %v", err)
			}
			if total.InputTokens != 3 {
				t.Fatalf("input tokens = %d, want 3", total.InputTokens)
			}
		})
	}
}

func TestUsageAdd(t *testing.T) {
	total := Usage{InputTokens: 1, OutputTokens: 2, CacheReadTokens: 3, Cost: 0.5}
	total.Add(Usage{InputTokens: 4, OutputTokens: 5, CacheReadTokens: 6, Cost: 0.25})
	want := Usage{InputTokens: 5, OutputTokens: 7, CacheReadTokens: 9, Cost: 0.75}
	if total != want {
		t.Fatalf("total = %+v, want %+v", total, want)
	}
}

func TestDefaultModelIsTheSmallOne(t *testing.T) {
	if DefaultModel != "claude-haiku-4-5" {
		t.Fatalf("DefaultModel = %q; DESIGN.md measured the small model sufficient", DefaultModel)
	}
}

// DESIGN.md's first hard-won prompt lesson: a structured-output field named
// "description" gets filled with a description OF THE FIELD. The rule is
// enforced where the schemas are built; this module's job is to keep the
// warning attached to the field a caller writes a schema into, so nobody
// reintroduces it. That doc comment is load-bearing, so it is pinned here.
func TestSchemaFieldCarriesTheDescriptionWarning(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "llm.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	var doc string
	ast.Inspect(file, func(n ast.Node) bool {
		field, isField := n.(*ast.Field)
		if !isField || len(field.Names) != 1 || field.Names[0].Name != "Schema" {
			return true
		}
		if field.Doc != nil {
			doc = field.Doc.Text()
		}
		return false
	})

	if doc == "" {
		t.Fatal("Request.Schema has no doc comment")
	}
	for _, phrase := range []string{`"description"`, "sentence"} {
		if !strings.Contains(doc, phrase) {
			t.Fatalf("Request.Schema doc does not mention %s:\n%s", phrase, doc)
		}
	}
}
