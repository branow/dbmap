// Package output renders command results. A command builds records and never
// decides how they look.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Format names a rendering.
type Format string

const (
	Table Format = "table"
	JSON  Format = "json"
)

// UnknownFormatError reports a format name no writer answers to.
type UnknownFormatError struct {
	Value   string
	Allowed []string
}

func (e *UnknownFormatError) Error() string {
	return fmt.Sprintf("unknown output format %q, want one of %s",
		e.Value, strings.Join(e.Allowed, ", "))
}

// Field is one named value of a record. Fields are ordered, so columns and JSON
// keys follow the order the command declared.
type Field struct {
	Name  string
	Value any
}

// Record is one result row.
type Record []Field

func (r Record) Get(name string) (any, bool) {
	for _, f := range r {
		if f.Name == name {
			return f.Value, true
		}
	}
	return nil, false
}

// MarshalJSON keeps the declared field order, which a map would lose.
func (r Record) MarshalJSON() ([]byte, error) {
	var b strings.Builder
	b.WriteByte('{')
	for i, f := range r {
		if i > 0 {
			b.WriteByte(',')
		}
		key, err := json.Marshal(f.Name)
		if err != nil {
			return nil, err
		}
		value, err := json.Marshal(f.Value)
		if err != nil {
			return nil, err
		}
		b.Write(key)
		b.WriteByte(':')
		b.Write(value)
	}
	b.WriteByte('}')
	return []byte(b.String()), nil
}

// Writer renders results in one format. Every command speaks only this.
type Writer interface {
	List(recs []Record) error
	Show(rec Record) error
	Value(v any) error
	// Note renders a status line. A machine-readable format drops it.
	Note(text string) error
}

// writers is the format table; New walks it rather than a switch.
var writers = map[Format]func(io.Writer) Writer{
	Table: func(w io.Writer) Writer { return &table{out: w} },
	JSON:  func(w io.Writer) Writer { return &jsonw{out: w} },
}

// New returns the writer for a format already validated by ParseFormat.
func New(f Format, out io.Writer) Writer {
	build, ok := writers[f]
	if !ok {
		build = writers[Table]
	}
	return build(out)
}

// ParseFormat validates a format name.
func ParseFormat(s string) (Format, error) {
	if _, ok := writers[Format(s)]; ok {
		return Format(s), nil
	}
	return "", &UnknownFormatError{Value: s, Allowed: Formats()}
}

// Formats lists the supported format names in stable order.
func Formats() []string {
	names := make([]string, 0, len(writers))
	for f := range writers {
		names = append(names, string(f))
	}
	sort.Strings(names)
	return names
}
