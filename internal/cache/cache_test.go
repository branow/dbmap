package cache

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/branow/dbmap/internal/catalog"
	"github.com/branow/dbmap/internal/redact"
)

// scope returns a Scope over a fresh directory, which every test needs and no
// test should have to spell out.
func scope(t *testing.T) *Scope {
	t.Helper()
	return New(t.TempDir()).Scope("dev", "appcore")
}

func TestManifestRoundTrip(t *testing.T) {
	s := scope(t)

	if _, ok, err := s.Manifest(); err != nil || ok {
		t.Fatalf("empty cache: ok = %v, err = %v; want false, nil", ok, err)
	}

	want := Manifest{
		Environment: "dev",
		Database:    "appcore",
		Objects: []catalog.Object{
			{Schema: "dbo", Name: "Orders", Kind: catalog.Table, Rows: 42, Modified: "2026-01-02"},
		},
	}
	if err := s.PutManifest(want); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}

	got, ok, err := s.Manifest()
	if err != nil || !ok {
		t.Fatalf("Manifest: ok = %v, err = %v; want true, nil", ok, err)
	}
	if len(got.Objects) != 1 || got.Objects[0].Name != "Orders" || got.Objects[0].Rows != 42 {
		t.Fatalf("round trip lost data: %+v", got)
	}
}

// A resumed run refetches nothing it holds at the same signal, and everything
// whose signal moved.
func TestResumeRefetchesOnlyWhatMoved(t *testing.T) {
	s := scope(t)
	structure := catalog.Structure{Columns: []catalog.Column{{Name: "OrderID", Type: "int"}}}

	if err := s.PutStructure("dbo.Orders", "v1", structure); err != nil {
		t.Fatalf("PutStructure: %v", err)
	}

	if _, ok, _ := s.Structure("dbo.Orders", "v1"); !ok {
		t.Fatal("same signal missed; a resumed run would refetch what it holds")
	}
	if _, ok, _ := s.Structure("dbo.Orders", "v2"); ok {
		t.Fatal("moved signal hit; a resumed run would serve a stale structure")
	}
}

// An absent modify signal can never prove anything untouched, so it must always
// miss — including against another absent signal. This is what keeps an engine
// with no modify_date correct rather than permanently stale.
func TestAbsentSignalAlwaysMisses(t *testing.T) {
	s := scope(t)

	if err := s.PutStructure("dbo.Orders", "", catalog.Structure{}); err != nil {
		t.Fatalf("PutStructure: %v", err)
	}
	if _, ok, _ := s.Structure("dbo.Orders", ""); ok {
		t.Fatal("absent signal hit; an engine without a modify signal would never refetch")
	}
}

// One object's entry must never answer for another, however the keys collide on
// disk.
func TestEntryNeverAnswersForAnotherKey(t *testing.T) {
	s := scope(t)

	if err := s.PutStructure("dbo.Orders", "v1", catalog.Structure{}); err != nil {
		t.Fatalf("PutStructure: %v", err)
	}
	if _, ok, _ := s.Structure("dbo.Customers", "v1"); ok {
		t.Fatal("a different key hit")
	}
	// Case-folding filesystems must not merge two distinct catalog names.
	if err := s.PutStructure("dbo.ORDERS", "v1", catalog.Structure{
		Columns: []catalog.Column{{Name: "Other"}},
	}); err != nil {
		t.Fatalf("PutStructure: %v", err)
	}
	got, ok, _ := s.Structure("dbo.Orders", "v1")
	if !ok || len(got.Columns) != 0 {
		t.Fatalf("case-different key overwrote the original: %+v", got)
	}
}

func TestModuleRoundTripKeepsRedactionCounts(t *testing.T) {
	s := scope(t)
	body := redact.Text("EXEC sp_send_dbmail @recipients = 'ops@example.com'")

	if err := s.PutModule("dbo.Mailer", "v1", body); err != nil {
		t.Fatalf("PutModule: %v", err)
	}
	got, ok, err := s.Module("dbo.Mailer", "v1")
	if err != nil || !ok {
		t.Fatalf("Module: ok = %v, err = %v", ok, err)
	}
	if strings.Contains(got.Body, "ops@example.com") {
		t.Fatal("cache stored an unredacted value")
	}
	// The tally rides with the body so a resumed run that refetches nothing can
	// still report what was stripped.
	if body.Counts().Total() > 0 && got.Redactions.Total() == 0 {
		t.Fatal("redaction counts lost on the round trip")
	}
}

// PutStructure must drop Definition: a structure arrives from the engine
// unredacted, and only the redactor may produce text this package writes.
func TestStructureCannotSmuggleABody(t *testing.T) {
	s := scope(t)
	secret := "CREATE PROC x AS SELECT 1 -- Password = hunter2"

	if err := s.PutStructure("dbo.Proc", "v1", catalog.Structure{Definition: secret}); err != nil {
		t.Fatalf("PutStructure: %v", err)
	}
	got, ok, _ := s.Structure("dbo.Proc", "v1")
	if !ok {
		t.Fatal("structure missed")
	}
	if got.Definition != "" {
		t.Fatalf("Definition survived: %q", got.Definition)
	}
	walk(t, s.Dir(), func(path string, data []byte) {
		if strings.Contains(string(data), "hunter2") {
			t.Fatalf("an unredacted body reached disk at %s", path)
		}
	})
}

func TestBodyIsCappedAtBodyChars(t *testing.T) {
	s := scope(t)
	long := strings.Repeat("a", BodyChars+500)

	if err := s.PutModule("dbo.Big", "v1", redact.Text(long)); err != nil {
		t.Fatalf("PutModule: %v", err)
	}
	got, _, _ := s.Module("dbo.Big", "v1")
	if n := len([]rune(got.Body)); n != BodyChars {
		t.Fatalf("stored %d runes, want %d", n, BodyChars)
	}
}

// Every way an entry can be unusable collapses to a miss: the caller's answer
// to all of them is to fetch again, and none may deserialize into garbage.
func TestCorruptEntriesReadAsMisses(t *testing.T) {
	cases := []struct {
		name    string
		corrupt func(data []byte) []byte
	}{
		{"truncated mid-write", func(d []byte) []byte { return d[:len(d)/2] }},
		{"empty file", func([]byte) []byte { return nil }},
		{"not json at all", func([]byte) []byte { return []byte("\x00\x01garbage") }},
		{"payload edited under its checksum", func(d []byte) []byte {
			var e envelope
			if err := json.Unmarshal(d, &e); err != nil {
				return d
			}
			e.Payload = json.RawMessage(`{"columns":[{"name":"Injected"}]}`)
			out, _ := json.Marshal(e)
			return out
		}},
		{"checksum removed", func(d []byte) []byte {
			var e envelope
			if err := json.Unmarshal(d, &e); err != nil {
				return d
			}
			e.Checksum = ""
			out, _ := json.Marshal(e)
			return out
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := scope(t)
			if err := s.PutStructure("dbo.Orders", "v1", catalog.Structure{
				Columns: []catalog.Column{{Name: "OrderID"}},
			}); err != nil {
				t.Fatalf("PutStructure: %v", err)
			}

			path := only(t, s.Dir())
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if err := os.WriteFile(path, c.corrupt(data), 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}

			got, ok, err := s.Structure("dbo.Orders", "v1")
			if err != nil {
				t.Fatalf("a corrupt entry must be a miss, not an error: %v", err)
			}
			if ok {
				t.Fatalf("corrupt entry reported as a hit: %+v", got)
			}
		})
	}
}

// A killed write leaves its temp file behind. That leftover must never be
// readable as an entry, or the next run trusts a half-written value.
func TestInterruptedWriteLeavesNothingReadable(t *testing.T) {
	s := scope(t)
	if err := os.MkdirAll(s.Dir(), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	partial, err := os.CreateTemp(s.Dir(), tempPattern)
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	if _, err := partial.WriteString(`{"artifact":"structure","key":"dbo.Orders"`); err != nil {
		t.Fatalf("write: %v", err)
	}
	partial.Close()

	if _, ok, err := s.Structure("dbo.Orders", "v1"); ok || err != nil {
		t.Fatalf("a leftover partial file was read: ok = %v, err = %v", ok, err)
	}
}

func TestCacheFilesAreNotWorldReadable(t *testing.T) {
	s := scope(t)
	if err := s.PutManifest(Manifest{Environment: "dev"}); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}

	info, err := os.Stat(only(t, s.Dir()))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != filePerm {
		t.Fatalf("permissions %04o, want %04o", perm, filePerm)
	}
}

// A catalog name is attacker-adjacent data: it must resolve to exactly one file
// inside the cache, never escape it.
func TestKeysCannotEscapeTheCacheDirectory(t *testing.T) {
	keys := []string{"../../etc/passwd", "dbo/../../..", ".hidden", "a\x00b", "dbo.Ord ers"}

	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			s := scope(t)
			if err := s.PutStructure(key, "v1", catalog.Structure{}); err != nil {
				t.Fatalf("PutStructure: %v", err)
			}
			path := only(t, s.Dir())
			if rel, err := filepath.Rel(s.Dir(), path); err != nil || strings.HasPrefix(rel, "..") {
				t.Fatalf("entry landed outside the scope: %s", path)
			}
			if _, ok, _ := s.Structure(key, "v1"); !ok {
				t.Fatal("key did not round trip")
			}
		})
	}
}

// only returns the single regular file under dir, failing if there is not
// exactly one.
func only(t *testing.T, dir string) string {
	t.Helper()
	var found []string
	walk(t, dir, func(path string, _ []byte) { found = append(found, path) })
	if len(found) != 1 {
		t.Fatalf("want exactly one file under %s, found %d: %v", dir, len(found), found)
	}
	return found[0]
}

// walk visits every regular file under dir, at any depth: entries live in a
// subdirectory per artifact, and a test asserting nothing leaked must see all
// of them.
func walk(t *testing.T, dir string, fn func(path string, data []byte)) {
	t.Helper()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fn(path, data)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
}

// The bug this pins: entries are pretty-printed on the way to disk, so hashing
// the payload as it appears rather than normalised made every entry read as
// corrupt and the cache miss on every lookup, with no error to show for it.
func TestChecksumSurvivesReformatting(t *testing.T) {
	payload := []byte(`{"columns":[{"name":"OrderID"}]}`)
	indented := []byte("{\n  \"columns\": [\n    {\n      \"name\": \"OrderID\"\n    }\n  ]\n}")

	if got, want := payloadSum(indented), payloadSum(payload); got != want {
		t.Fatalf("reformatting changed the digest:\n compact  %s\n indented %s", want, got)
	}
	if payloadSum(payload) == payloadSum([]byte(`{"columns":[{"name":"Injected"}]}`)) {
		t.Fatal("different payloads share a digest")
	}
}
