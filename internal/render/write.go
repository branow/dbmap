package render

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
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

// Write renders the index tree for one database, MERGING entries into whatever
// the last build left there. A narrowed build covers only the objects it
// selected, so replacing a catalog wholesale would delete the rows it did not
// look at - and with them the staleness state the next build reads back, which
// turns every unselected object into a new one and buys its description again.
//
// dropped are objects the database no longer has: their rows and their detail
// files go. Everything else present and unmentioned stays exactly as it was.
func Write(dir string, entries []catalog.Entry, dropped []string) (Result, error) {
	for _, sub := range []string{columnsDir, bodiesDir} {
		if err := os.MkdirAll(filepath.Join(dir, sub), dirMode); err != nil {
			return Result{}, err
		}
	}

	gone := make(map[string]bool, len(dropped))
	for _, key := range dropped {
		gone[key] = true
	}

	var result Result
	for _, c := range Catalogs {
		written, err := merge(dir, c, entries, gone)
		if err != nil {
			return Result{}, err
		}
		if written != nil {
			result.Catalogs = append(result.Catalogs, *written)
		}
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

	if err := Remove(dir, dropped); err != nil {
		return Result{}, err
	}
	return result, nil
}

// merge rewrites one catalog file from the rows already there plus the rows
// this build produced. Rows are keyed by name and sorted, so the file does not
// depend on which objects a run happened to select.
func merge(dir string, c Catalog, entries []catalog.Entry, gone map[string]bool) (*Written, error) {
	path := filepath.Join(dir, c.File)
	width := len(Header(c))

	rows := map[string][]string{}
	previous, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		for key, cells := range parseRows(string(previous), width) {
			rows[key] = cells
		}
	}

	for _, entry := range entries {
		if entry.Object.Kind != c.Kind {
			continue
		}
		rows[entry.Key()] = row(c, entry)
	}
	for key := range gone {
		delete(rows, key)
	}

	if len(rows) == 0 {
		// The kind is empty now. Leaving the old file would leave rows for
		// objects the catalog no longer has.
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		return nil, nil
	}

	ordered := make([][]string, 0, len(rows))
	for _, key := range sortedKeys(rows) {
		ordered = append(ordered, rows[key])
	}
	if err := os.WriteFile(path, []byte(TSV(Header(c), ordered)), fileMode); err != nil {
		return nil, err
	}
	return &Written{File: c.File, Rows: len(ordered)}, nil
}

// parseRows reads a catalog file back as raw cells, padded to the current
// width so a file written by an older shape still merges.
func parseRows(content string, width int) map[string][]string {
	rows := map[string][]string{}
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		cells := strings.Split(line, tab)
		if cells[0] == "name" || cells[0] == "" || len(cells) < width-1 {
			continue
		}
		for len(cells) < width {
			cells = append(cells, "")
		}
		rows[cells[0]] = cells[:width]
	}
	return rows
}

func sortedKeys(rows map[string][]string) []string {
	keys := make([]string, 0, len(rows))
	for key := range rows {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// ParseCatalog reads one catalog file back into the state the planner expects.
func ParseCatalog(content string, c Catalog) map[string]catalog.State {
	state := make(map[string]catalog.State)
	at := rowsIndex(c)
	width := len(Header(c))

	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		cells := strings.Split(line, tab)
		// One cell short means an empty description was trimmed; shorter is
		// not a row.
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

// ReadState reads staleness state back out of the catalogs. A missing catalog
// is not an error: a kind the database lacks never got a file.
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

// Remove deletes the per-object files of dropped objects; their catalog rows
// are dropped by the merge that rewrote the catalog.
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
