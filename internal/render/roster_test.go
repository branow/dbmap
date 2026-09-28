package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func built(day int) time.Time {
	return time.Date(2026, 3, day, 9, 0, 0, 0, time.UTC)
}

func rosterOf(t *testing.T, root string) map[string][]string {
	t.Helper()
	rows, err := ReadRoster(root)
	if err != nil {
		t.Fatalf("reading roster: %v", err)
	}
	return rows
}

func writeRoster(t *testing.T, root string, entry Roster) {
	t.Helper()
	if err := WriteRoster(root, entry); err != nil {
		t.Fatalf("writing roster: %v", err)
	}
}

// The roster is the only file in the tree that spans databases, and each one is
// built on its own run. A build that rewrote the whole file would erase the
// counts and the stamp of every database it did not touch, leaving a reader
// with a tree whose contents are invisible until every database is rebuilt.
func TestABuildLeavesTheOtherDatabasesRosterRowsStanding(t *testing.T) {
	root := t.TempDir()
	writeRoster(t, root, Roster{
		Connection: "stage", Database: "AppCore", Engine: "sqlserver",
		Catalogs: []Written{{File: "tables.tsv", Rows: 12}},
		Built:    built(1),
	})
	writeRoster(t, root, Roster{
		Connection: "stage", Database: "Reporting", Engine: "sqlserver",
		Catalogs: []Written{{File: "tables.tsv", Rows: 4}},
		Built:    built(2),
	})

	rows := rosterOf(t, root)
	if len(rows) != 2 {
		t.Fatalf("roster rows = %d, want 2: %v", len(rows), rows)
	}
	first := rows["stage/AppCore"]
	if first[len(first)-1] != "2026-03-01T09:00:00Z" {
		t.Errorf("AppCore build stamp = %q, want the first build's", first[len(first)-1])
	}
}

func TestRebuildingADatabaseReplacesItsOwnRow(t *testing.T) {
	root := t.TempDir()
	writeRoster(t, root, Roster{
		Connection: "stage", Database: "AppCore", Engine: "sqlserver",
		Catalogs: []Written{{File: "tables.tsv", Rows: 12}},
		Built:    built(1),
	})
	writeRoster(t, root, Roster{
		Connection: "stage", Database: "AppCore", Engine: "sqlserver",
		Catalogs: []Written{{File: "tables.tsv", Rows: 14}, {File: "views.tsv", Rows: 3}},
		Built:    built(5),
	})

	rows := rosterOf(t, root)
	if len(rows) != 1 {
		t.Fatalf("roster rows = %d, want 1: %v", len(rows), rows)
	}
	row := rows["stage/AppCore"]
	header := RosterHeader()
	got := map[string]string{}
	for i, name := range header {
		got[name] = row[i]
	}
	for name, want := range map[string]string{
		"engine": "sqlserver", "tables": "14", "views": "3", "procedures": "0",
		"built": "2026-03-05T09:00:00Z",
	} {
		if got[name] != want {
			t.Errorf("%s = %q, want %q", name, got[name], want)
		}
	}
}

// Two connections may hold a database of the same name - a stage and a prod
// copy of the same schema is the ordinary case - and they are different rows.
func TestTheSameDatabaseUnderTwoConnectionsAreSeparateRows(t *testing.T) {
	root := t.TempDir()
	writeRoster(t, root, Roster{Connection: "stage", Database: "AppCore", Built: built(1)})
	writeRoster(t, root, Roster{Connection: "dev", Database: "AppCore", Built: built(1)})

	if rows := rosterOf(t, root); len(rows) != 2 {
		t.Fatalf("roster rows = %d, want 2: %v", len(rows), rows)
	}
}

// A roster written before a kind existed is short by a column. Dropping those
// rows would silently empty the table of contents on the first build after a
// release that adds one.
func TestARosterFromAnOlderShapeStillMerges(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, RosterFile)
	if err := os.WriteFile(path, []byte("connection\tdatabase\tengine\n"+
		"stage\tReporting\tpostgres\n"), 0o644); err != nil {
		t.Fatalf("seeding roster: %v", err)
	}

	writeRoster(t, root, Roster{Connection: "stage", Database: "AppCore", Built: built(1)})

	rows := rosterOf(t, root)
	if len(rows) != 2 {
		t.Fatalf("roster rows = %d, want 2: %v", len(rows), rows)
	}
	if got := rows["stage/Reporting"][2]; got != "postgres" {
		t.Errorf("engine of the older row = %q, want postgres", got)
	}
}

func TestReadingARosterThatIsNotThereIsNotAnError(t *testing.T) {
	rows, err := ReadRoster(t.TempDir())
	if err != nil {
		t.Fatalf("reading an absent roster: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("rows = %v, want none", rows)
	}
}

// The guide is the reader's whole contract with the tree: it is read by agents
// that have never heard of this tool, so it must name the roster and the two
// directories a question actually lands in.
func TestTheGuideNamesWhatAReaderNeedsToFind(t *testing.T) {
	root := t.TempDir()
	if err := WriteGuide(root); err != nil {
		t.Fatalf("writing guide: %v", err)
	}
	text := read(t, filepath.Join(root, GuideFile))
	for _, want := range []string{RosterFile, "columns/", "bodies/", "grep"} {
		if !strings.Contains(text, want) {
			t.Errorf("guide does not mention %q", want)
		}
	}
}
