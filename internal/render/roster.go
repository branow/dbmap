package render

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// RosterFile is the tree's own table of contents, written at the root rather
// than inside one database's directory: one row per database the tree holds.
//
// It exists because the reader is not the builder. Whoever reads the index has
// no dbmap installed, no connection and no way to ask what was built - so the
// roster, and the guide beside it, are what let a tree explain itself to a
// reader who arrives with nothing but the files.
const RosterFile = "databases.tsv"

// GuideFile is the reader's copy of the layout, written beside the roster for
// the same reason: an agent that has never heard of this tool must be able to
// use the tree from the tree alone.
const GuideFile = "README.md"

// Roster is one database's row: which connection built it, what it holds, and
// when. Counts come from the catalogs the build actually wrote, so the roster
// and the files it points at cannot disagree.
type Roster struct {
	Connection string
	Database   string
	Engine     string
	Catalogs   []Written
	Built      time.Time
}

// rosterLead is the roster's identity columns; the object counts follow, one
// per catalog, and `built` closes every row.
var rosterLead = []string{"connection", "database", "engine"}

// RosterHeader is the first line of the roster: the lead columns, one column
// per catalog kind, then the build stamp. Adding a kind to Catalogs adds its
// column here, which is why the counts are not a fixed list.
func RosterHeader() []string {
	header := make([]string, 0, len(rosterLead)+len(Catalogs)+1)
	header = append(header, rosterLead...)
	for _, c := range Catalogs {
		header = append(header, strings.TrimSuffix(c.File, ".tsv"))
	}
	return append(header, "built")
}

// WriteRoster merges one database's row into the roster, leaving every other
// row exactly as it was. A tree holds several databases built at different
// times by different people, so a build that rewrote the whole file would
// erase the stamps of the databases it did not touch.
func WriteRoster(root string, entry Roster) error {
	if err := os.MkdirAll(root, dirMode); err != nil {
		return err
	}
	path := filepath.Join(root, RosterFile)
	header := RosterHeader()

	rows, err := readRoster(path, len(header))
	if err != nil {
		return err
	}
	rows[rosterKey(entry.Connection, entry.Database)] = rosterRow(entry)

	keys := make([]string, 0, len(rows))
	for key := range rows {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	ordered := make([][]string, 0, len(rows))
	for _, key := range keys {
		ordered = append(ordered, rows[key])
	}
	return os.WriteFile(path, []byte(TSV(header, ordered)), fileMode)
}

// ReadRoster reads the roster back as rows keyed by connection and database.
// A tree with no roster yet is not an error: it is a tree built before the
// roster existed, or one that has not finished its first build.
func ReadRoster(root string) (map[string][]string, error) {
	return readRoster(filepath.Join(root, RosterFile), len(RosterHeader()))
}

func rosterKey(connection, database string) string {
	return connection + "/" + database
}

func rosterRow(entry Roster) []string {
	written := make(map[string]int, len(entry.Catalogs))
	for _, file := range entry.Catalogs {
		written[file.File] = file.Rows
	}
	cells := make([]string, 0, len(rosterLead)+len(Catalogs)+1)
	cells = append(cells, entry.Connection, entry.Database, entry.Engine)
	for _, c := range Catalogs {
		cells = append(cells, strconv.Itoa(written[c.File]))
	}
	return append(cells, entry.Built.UTC().Format(time.RFC3339))
}

// readRoster parses the roster, padding short rows so a file written before a
// kind was added still merges rather than being dropped.
func readRoster(path string, width int) (map[string][]string, error) {
	rows := map[string][]string{}
	content, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return rows, nil
	case err != nil:
		return nil, err
	}

	for _, line := range strings.Split(string(content), "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		cells := strings.Split(line, tab)
		if len(cells) < 2 || cells[0] == "connection" || cells[0] == "" || cells[1] == "" {
			continue
		}
		for len(cells) < width {
			cells = append(cells, "")
		}
		rows[rosterKey(cells[0], cells[1])] = cells[:width]
	}
	return rows, nil
}

// WriteGuide writes the layout the reader needs, overwriting whatever is there
// so a tree carries the format of the build that last touched it rather than
// the format of the build that created it.
func WriteGuide(root string) error {
	if err := os.MkdirAll(root, dirMode); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, GuideFile), []byte(guide), fileMode)
}

// guide is addressed to whoever reads the tree, which is usually a coding agent
// that has never heard of this tool. It says where to start, what each file
// holds, and the one question the descriptions cannot answer - deliberately
// short, because it is read in full on every session that opens the tree.
const guide = "# Database index\n" +
	"\n" +
	"A read-only index of one or more databases, written by\n" +
	"[dbmap](https://github.com/branow/dbmap). Plain text: no database\n" +
	"connection, no credential and no tool are needed to read it.\n" +
	"\n" +
	"## Where to start\n" +
	"\n" +
	"`" + RosterFile + "` lists every database here, with its object counts and when it\n" +
	"was built. Each row is a directory: `<connection>/<database>/`.\n" +
	"\n" +
	"## What a database directory holds\n" +
	"\n" +
	"Five catalogs, one row per object, `<schema>.<name>` as the name. Each row\n" +
	"ends with `modified`, `fingerprint` and `description`; the first two are the\n" +
	"build's staleness state, and reading them is not useful.\n" +
	"\n" +
	"- `tables.tsv` - `name, rows, size, triggers, cols`\n" +
	"- `views.tsv` - `name, cols`\n" +
	"- `procedures.tsv` - `name, params`\n" +
	"- `functions.tsv` - `name, returns, params`\n" +
	"- `synonyms.tsv` - `name, target`\n" +
	"\n" +
	"Then one file per object:\n" +
	"\n" +
	"- `columns/<schema>.<name>.tsv` - every table and view: `column, type, null,\n" +
	"  extra`, with `# pk`, `# index` and `# fk` lines beneath\n" +
	"- `bodies/<schema>.<name>.sql` - every view, procedure and function: its own\n" +
	"  SQL, credentials redacted, under a header carrying its description\n" +
	"\n" +
	"## Reading it\n" +
	"\n" +
	"1. Read the whole catalog for the kind you want. It is a list of one-sentence\n" +
	"   descriptions, which is how you find an object whose name you do not know.\n" +
	"2. Read that object's detail file: `columns/` for a table, `bodies/` for a\n" +
	"   procedure or function, either for a view.\n" +
	"3. For a question about what touches an object, grep the bodies. A\n" +
	"   description names an object's main tables, not all of them, so it cannot\n" +
	"   answer this and the bodies can:\n" +
	"\n" +
	"```sh\n" +
	"# everything that mentions this table\n" +
	"grep -rl \"dbo.Orders\" <connection>/<database>/bodies/\n" +
	"\n" +
	"# of those, the ones that write to it\n" +
	"grep -rliE \"(insert|update|delete|merge)[^;]{0,40}dbo\\.Orders\" \\\n" +
	"  <connection>/<database>/bodies/\n" +
	"```\n" +
	"\n" +
	"## What it cannot answer\n" +
	"\n" +
	"The index describes structure, not data: it holds no rows, and no sampled\n" +
	"value ever reaches it. A question about actual values, or about an object\n" +
	"created after the `built` stamp in `" + RosterFile + "`, needs the database\n" +
	"itself.\n"
