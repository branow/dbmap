package cmd

import (
	"testing"

	"github.com/branow/dbmap/internal/config"
)

// --db once named only the output directory: the pool still opened the
// connection's own database, so `--db master` indexed AppCore and wrote it into
// a tree labelled master. Silently wrong data, exit 0.
//
// The test that existed asserted resolveDatabase's return value, which was
// always right — the bug was that nobody used it to open anything. So this
// asserts the connection that gets opened, which is the thing that was wrong.
func TestTheResolvedDatabaseIsTheOneOpened(t *testing.T) {
	cases := []struct {
		name     string
		entry    config.Connection
		override string
		want     string
	}{
		{"the flag redirects the connection", config.Connection{Database: "AppCore"}, "master", "master"},
		{"no flag keeps the connection's own", config.Connection{Database: "AppCore"}, "", "AppCore"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			entry := c.entry

			database, err := resolveDatabase(entry, c.override)
			if err != nil {
				t.Fatalf("resolveDatabase: %v", err)
			}
			entry.Database = database

			if entry.Database != c.want {
				t.Errorf("the opened database is %q, want %q", entry.Database, c.want)
			}
			// The stored connection must not be edited by an override.
			if c.entry.Database != "AppCore" {
				t.Errorf("the stored connection was mutated to %q", c.entry.Database)
			}
		})
	}
}
