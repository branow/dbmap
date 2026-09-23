package render

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/branow/dbmap/internal/catalog"
)

const (
	columnsDir = "columns"
	bodiesDir  = "bodies"
)

const (
	dirMode  fs.FileMode = 0o755
	fileMode fs.FileMode = 0o644
)

// Written is one catalog file the build produced.
type Written struct {
	File string
	Rows int
}

// Result is what a write produced, for the build summary.
type Result struct {
	Catalogs    []Written
	ColumnFiles int
	BodyFiles   int
}

// Write renders the whole index tree for one database. A catalog with no
// objects is not written at all, so a database with no views has no views.tsv
// rather than an empty one.
func Write(dir string, entries []catalog.Entry) (Result, error) {
	for _, sub := range []string{columnsDir, bodiesDir} {
		if err := os.MkdirAll(filepath.Join(dir, sub), dirMode); err != nil {
			return Result{}, err
		}
	}

	var result Result
	for _, c := range Catalogs {
		var rows [][]string
		for _, entry := range entries {
			if entry.Object.Kind == c.Kind {
				rows = append(rows, row(c, entry))
			}
		}
		if len(rows) == 0 {
			continue
		}
		path := filepath.Join(dir, c.File)
		if err := os.WriteFile(path, []byte(TSV(Header(c), rows)), fileMode); err != nil {
			return Result{}, err
		}
		result.Catalogs = append(result.Catalogs, Written{File: c.File, Rows: len(rows)})
	}

	for _, entry := range entries {
		if !detailed[entry.Object.Kind] || len(entry.Structure.Columns) == 0 {
			continue
		}
		path := filepath.Join(dir, columnsDir, entry.Key()+".tsv")
		if err := os.WriteFile(path, []byte(Columns(entry)), fileMode); err != nil {
			return Result{}, err
		}
		result.ColumnFiles++
	}

	// A module's body is written for the same reason a table's columns are: one
	// sentence cannot answer "what writes to this table", and grepping the
	// definitions can, with no database connection.
	for _, entry := range entries {
		spec, ok := catalog.Lookup(entry.Object.Kind)
		if !ok || !spec.Module || strings.TrimSpace(entry.Structure.Definition) == "" {
			continue
		}
		path := filepath.Join(dir, bodiesDir, entry.Key()+".sql")
		if err := os.WriteFile(path, []byte(Body(entry)), fileMode); err != nil {
			return Result{}, err
		}
		result.BodyFiles++
	}

	return result, nil
}

// ParseCatalog reads one catalog file back into the state the planner expects.
// Row counts come back only for the catalog that stores them, which is the only
// one whose objects are ever sampled.
func ParseCatalog(content string, c Catalog) map[string]catalog.State {
	state := make(map[string]catalog.State)
	at := rowsIndex(c)
	width := len(Header(c))

	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		cells := strings.Split(line, tab)
		// A row one cell short is a row whose description was empty and got
		// trimmed; anything shorter than that is not a row.
		if cells[0] == "name" || cells[0] == "" || len(cells) < width-1 {
			continue
		}
		rows := int64(0)
		if at != -1 {
			rows, _ = strconv.ParseInt(cell(cells, at), 10, 64)
		}
		state[cells[0]] = catalog.State{
			Modified:    catalog.Signal(cell(cells, width-3)),
			Fingerprint: cell(cells, width-2),
			Description: cell(cells, width-1),
			Rows:        rows,
		}
	}
	return state
}

func cell(cells []string, i int) string {
	if i < 0 || i >= len(cells) {
		return ""
	}
	return cells[i]
}

// ReadState reads the whole index's staleness state back out of the catalogs it
// was written to. A missing catalog is not an error: a kind the database does
// not have never got a file.
func ReadState(dir string) (map[string]catalog.State, error) {
	state := make(map[string]catalog.State)
	for _, c := range Catalogs {
		content, err := os.ReadFile(filepath.Join(dir, c.File))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for key, value := range ParseCatalog(string(content), c) {
			state[key] = value
		}
	}
	return state, nil
}

// Remove deletes the detail files of dropped objects. Their catalog rows go by
// being rewritten without them; the per-object files have to be deleted, and
// the layout of those files is this package's knowledge, not a caller's.
func Remove(dir string, keys []string) error {
	for _, key := range keys {
		for _, path := range []string{
			filepath.Join(dir, columnsDir, key+".tsv"),
			filepath.Join(dir, bodiesDir, key+".sql"),
		} {
			if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}
