package output

import (
	"encoding/json"
	"io"
	"sort"
)

// jsonw renders one indented JSON document per call.
type jsonw struct{ out io.Writer }

func (j *jsonw) List(recs []Record) error {
	if recs == nil {
		recs = []Record{}
	}
	return j.encode(recs)
}

func (j *jsonw) Show(rec Record) error { return j.encode(rec) }

func (j *jsonw) Value(v any) error { return j.encode(v) }

// Note drops the line: it is not part of the document a program parses.
func (j *jsonw) Note(string) error { return nil }

func (j *jsonw) encode(v any) error {
	enc := json.NewEncoder(j.out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
