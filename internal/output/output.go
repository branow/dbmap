// Package output renders command results. A command builds records and never
// decides how they look; the format chosen by -o picks the writer.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Format names a rendering. It is validated once, where the flag is read.
type Format string

// The formats dbmap ships. Adding one means adding a writer to writers.
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

// Field is one named value of a record. Fields are ordered, so table columns
// and JSON keys always agree with the order the command declared.
type Field struct {
	Name  string
	Value any
}

// Record is one result row.
type Record []Field

// Get returns the value of a named field.
func (r Record) Get(name string) (any, bool) {
	for _, f := range r {
		if f.Name == name {
			return f.Value, true
		}
	}
	return nil, false
}

// MarshalJSON keeps the declared field order, which encoding/json would lose
// through a map.
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
	// List renders zero or more records sharing a shape.
	List(recs []Record) error
	// Show renders a single record.
	Show(rec Record) error
	// Value renders one scalar, for a lookup such as `config get`.
	Value(v any) error
	// Note renders a human-facing status line. A machine-readable format drops
	// it, because a status line is not part of the document.
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

// ParseFormat validates a format name from a flag, an env var or the config
// file, all of which are external input.
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
