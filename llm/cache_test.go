package llm

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sampled stands in for the sampled database rows a describe prompt carries.
const sampled = "CustomerID=4711 email=user@example.internal"

func request() Request {
	return Request{
		System: "you are a database cartographer",
		Prompt: "describe dbo.Example\n" + sampled,
		Schema: json.RawMessage(`{"type":"object"}`),
		Model:  "test-model",
	}
}

func TestCacheMissThenHit(t *testing.T) {
	inner := &fake{steps: []step{{resp: ok()}}}
	client := WithCache(inner, t.TempDir())

	first, err := client.Complete(context.Background(), request())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	second, err := client.Complete(context.Background(), request())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if inner.count() != 1 {
		t.Fatalf("calls = %d, want 1", inner.count())
	}
	if string(first.Structured) != string(second.Structured) {
		t.Fatalf("hit returned %s, want %s", second.Structured, first.Structured)
	}
	if second.Model != first.Model {
		t.Fatalf("hit model = %q, want %q", second.Model, first.Model)
	}
}

func TestCacheMissesOnAnyChangedField(t *testing.T) {
	base := request()
	cases := []struct {
		name  string
		apply func(*Request)
	}{
		{"prompt", func(r *Request) { r.Prompt += " more" }},
		{"system", func(r *Request) { r.System += " more" }},
		{"schema", func(r *Request) { r.Schema = json.RawMessage(`{"type":"array"}`) }},
		{"model", func(r *Request) { r.Model = "other-model" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			inner := &fake{}
			client := WithCache(inner, t.TempDir())
			if _, err := client.Complete(context.Background(), base); err != nil {
				t.Fatal(err)
			}
			changed := base
			c.apply(&changed)
			if _, err := client.Complete(context.Background(), changed); err != nil {
				t.Fatal(err)
			}
			if inner.count() != 2 {
				t.Fatalf("calls = %d, want 2", inner.count())
			}
		})
	}
}

func TestCacheKeyDependsOnTheProvider(t *testing.T) {
	a := CacheKey("anthropic", request())
	b := CacheKey("openai", request())
	if a == b {
		t.Fatal("two providers shared a cache key")
	}
	if a != CacheKey("anthropic", request()) {
		t.Fatal("the key is not stable")
	}
}

// The prompt may carry sampled database rows. Only the response is ever
// written to disk.
func TestCacheStoresNoPartOfThePrompt(t *testing.T) {
	dir := t.TempDir()
	client := WithCache(&fake{steps: []step{{resp: ok()}}}, dir)
	if _, err := client.Complete(context.Background(), request()); err != nil {
		t.Fatal(err)
	}

	req := request()
	files := 0
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		files++
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, secret := range []string{sampled, req.Prompt, req.System} {
			if strings.Contains(string(data), secret) {
				t.Fatalf("%s leaked into %s", secret, path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files != 1 {
		t.Fatalf("cache holds %d files, want 1", files)
	}
}

func TestCacheDoesNotStoreFailures(t *testing.T) {
	inner := &fake{steps: []step{
		{err: &Error{Class: ClassUnavailable}},
		{resp: ok()},
	}}
	client := WithCache(inner, t.TempDir())

	if _, err := client.Complete(context.Background(), request()); err == nil {
		t.Fatal("expected an error")
	}
	if _, err := client.Complete(context.Background(), request()); err != nil {
		t.Fatalf("err = %v", err)
	}
	if inner.count() != 2 {
		t.Fatalf("calls = %d, want 2", inner.count())
	}
}

func TestCacheFallsThroughWhenTheDirectoryIsUnusable(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	inner := &fake{}
	client := WithCache(inner, blocker)

	for i := 0; i < 2; i++ {
		if _, err := client.Complete(context.Background(), request()); err != nil {
			t.Fatalf("err = %v", err)
		}
	}
	if inner.count() != 2 {
		t.Fatalf("calls = %d, want 2", inner.count())
	}
}

func TestCacheLeavesTheClientAloneWithoutADirectory(t *testing.T) {
	inner := &fake{}
	if client := WithCache(inner, ""); client != Client(inner) {
		t.Fatal("an empty directory wrapped the client")
	}
}

func TestCachePassesTheNameThrough(t *testing.T) {
	if got := WithCache(&fake{name: "inner"}, t.TempDir()).Name(); got != "inner" {
		t.Fatalf("Name = %q", got)
	}
}
