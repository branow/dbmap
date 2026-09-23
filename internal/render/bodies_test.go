package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/branow/dbmap/internal/catalog"
)

func module(kind catalog.Kind, name, definition string) catalog.Entry {
	return catalog.Entry{
		Object:      catalog.Object{Schema: "dbo", Name: name, Kind: kind, Modified: "2026-01-02"},
		Structure:   catalog.Structure{Definition: definition},
		Fingerprint: "abc123",
		Description: "Reads order totals.",
	}
}

// The reason bodies are written at all: a one-sentence description names an
// object's main tables, not all of them, so only the definition answers "what
// writes to this table" — and grep can answer it with no database connection.
func TestEveryModuleKindGetsItsBodyOnDisk(t *testing.T) {
	dir := t.TempDir()
	entries := []catalog.Entry{
		module(catalog.Procedure, "OrderStatusSet", "UPDATE dbo.Orders SET StatusID = @s"),
		module(catalog.Function, "OrderTotal", "RETURN (SELECT Total FROM dbo.Orders)"),
		module(catalog.View, "OpenOrders", "SELECT id FROM dbo.Orders WHERE StatusID = 1"),
	}

	result, err := Write(dir, entries)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if result.BodyFiles != len(entries) {
		t.Fatalf("BodyFiles = %d, want %d", result.BodyFiles, len(entries))
	}

	for _, entry := range entries {
		path := filepath.Join(dir, bodiesDir, entry.Key()+".sql")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v", entry.Key(), err)
		}
		if !strings.Contains(string(data), entry.Structure.Definition) {
			t.Errorf("%s does not carry its definition:\n%s", entry.Key(), data)
		}
	}

	// The whole point: the write target is greppable without a connection.
	hit, err := os.ReadFile(filepath.Join(dir, bodiesDir, "dbo.OrderStatusSet.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(hit), "UPDATE dbo.Orders") {
		t.Error("the writer that a description would not have named is not greppable")
	}
}

// A table has no body, and a kind with an empty definition must not leave an
// empty file behind pretending it does.
func TestATableGetsNoBodyFile(t *testing.T) {
	dir := t.TempDir()

	result, err := Write(dir, []catalog.Entry{
		{
			Object:    catalog.Object{Schema: "dbo", Name: "Orders", Kind: catalog.Table},
			Structure: catalog.Structure{Columns: []catalog.Column{{Name: "ID", Type: "int"}}},
		},
		module(catalog.Procedure, "Encrypted", ""),
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if result.BodyFiles != 0 {
		t.Fatalf("BodyFiles = %d, want 0", result.BodyFiles)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, bodiesDir))
	if len(entries) != 0 {
		t.Fatalf("bodies/ holds %d files, want none", len(entries))
	}
}

// A reader landing on a body file from a grep hit needs to know what it is and
// whether it is current, without opening another file.
func TestABodyFileCarriesItsOwnContext(t *testing.T) {
	dir := t.TempDir()
	entry := module(catalog.Procedure, "OrderStatusSet", "UPDATE dbo.Orders SET StatusID = @s")

	if _, err := Write(dir, []catalog.Entry{entry}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, bodiesDir, "dbo.OrderStatusSet.sql"))
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"dbo.OrderStatusSet", "@abc123", "2026-01-02", "Reads order totals."} {
		if !strings.Contains(string(data), want) {
			t.Errorf("body file does not carry %q:\n%s", want, data)
		}
	}
	// The header must be SQL comments, so the file stays valid SQL.
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "dbo.") || strings.HasPrefix(line, "2026-") {
			t.Errorf("header line is not a SQL comment: %q", line)
		}
	}
}

// A body cut at the cap must say so. The tail of a procedure is where the
// writes usually are, so a reader who cannot tell a fragment from a whole
// object would conclude the procedure writes nothing.
func TestATruncatedBodySaysSo(t *testing.T) {
	dir := t.TempDir()
	long := "SELECT 1 " + strings.Repeat("x", catalog.MaxDefinition)

	if _, err := Write(dir, []catalog.Entry{module(catalog.Procedure, "Big", long)}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, bodiesDir, "dbo.Big.sql"))
	if !strings.Contains(string(data), Truncated) {
		t.Error("a body at the cap is not marked as truncated")
	}

	short := module(catalog.Procedure, "Small", "SELECT 1")
	if _, err := Write(dir, []catalog.Entry{short}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	data, _ = os.ReadFile(filepath.Join(dir, bodiesDir, "dbo.Small.sql"))
	if strings.Contains(string(data), Truncated) {
		t.Error("a whole body is marked as truncated")
	}
}

// A dropped object loses its body as well as its columns, or a grep keeps
// hitting a procedure the database no longer has.
func TestRemoveTakesBothDetailFiles(t *testing.T) {
	dir := t.TempDir()
	entries := []catalog.Entry{
		module(catalog.Procedure, "Gone", "SELECT 1"),
		{
			Object:    catalog.Object{Schema: "dbo", Name: "Gone", Kind: catalog.Table},
			Structure: catalog.Structure{Columns: []catalog.Column{{Name: "ID", Type: "int"}}},
		},
	}
	if _, err := Write(dir, entries); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if err := Remove(dir, []string{"dbo.Gone"}); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	for _, path := range []string{
		filepath.Join(dir, bodiesDir, "dbo.Gone.sql"),
		filepath.Join(dir, columnsDir, "dbo.Gone.tsv"),
	} {
		if _, err := os.Stat(path); err == nil {
			t.Errorf("%s survived removal", path)
		}
	}
	// Removing what is not there is not an error: a run may drop an object
	// whose detail file was never written.
	if err := Remove(dir, []string{"dbo.NeverExisted"}); err != nil {
		t.Errorf("Remove on a missing file returned %v", err)
	}
}
