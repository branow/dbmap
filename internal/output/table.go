package output

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

// table renders for a person: aligned columns for a list, aligned key/value
// pairs for a single record.
type table struct{ out io.Writer }

func (t *table) List(recs []Record) error {
	if len(recs) == 0 {
		return nil
	}
	w := tabwriter.NewWriter(t.out, 0, 4, 2, ' ', 0)
	header := make([]string, len(recs[0]))
	for i, f := range recs[0] {
		header[i] = strings.ToUpper(f.Name)
	}
	fmt.Fprintln(w, strings.Join(header, "\t"))
	for _, rec := range recs {
		cells := make([]string, len(rec))
		for i, f := range rec {
			cells[i] = cell(f.Value)
		}
		fmt.Fprintln(w, strings.Join(cells, "\t"))
	}
	return w.Flush()
}

func (t *table) Show(rec Record) error {
	w := tabwriter.NewWriter(t.out, 0, 4, 2, ' ', 0)
	for _, f := range rec {
		fmt.Fprintf(w, "%s\t%s\n", f.Name, cell(f.Value))
	}
	return w.Flush()
}

func (t *table) Value(v any) error {
	_, err := fmt.Fprintln(t.out, cell(v))
	return err
}

func (t *table) Note(text string) error {
	_, err := fmt.Fprintln(t.out, text)
	return err
}

// cell flattens a value to one column. A nil is blank rather than "<nil>",
// because a table is read by a person.
func cell(v any) string {
	switch value := v.(type) {
	case nil:
		return ""
	case string:
		return value
	case []string:
		return strings.Join(value, ",")
	case map[string]string:
		return joinPairs(value)
	default:
		return fmt.Sprint(v)
	}
}

func joinPairs(m map[string]string) string {
	pairs := make([]string, 0, len(m))
	for _, k := range sortedKeys(m) {
		pairs = append(pairs, k+"="+m[k])
	}
	return strings.Join(pairs, ",")
}
