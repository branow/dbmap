package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func record() Record {
	return Record{
		{Name: "name", Value: "primary"},
		{Name: "engine", Value: "sqlserver"},
		{Name: "port", Value: 1433},
		{Name: "production", Value: false},
		{Name: "database", Value: nil},
	}
}

func TestParseFormat(t *testing.T) {
	tests := []struct {
		value   string
		want    Format
		wantErr bool
	}{
		{value: "table", want: Table},
		{value: "json", want: JSON},
		{value: "tsv", wantErr: true},
		{value: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			got, err := ParseFormat(tt.value)
			if tt.wantErr {
				var unknown *UnknownFormatError
				if !errors.As(err, &unknown) {
					t.Fatalf("error = %v, want UnknownFormatError", err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("format = %v, err = %v", got, err)
			}
		})
	}
}

func TestTableList(t *testing.T) {
	var buf bytes.Buffer
	if err := New(Table, &buf).List([]Record{record()}); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	for _, want := range []string{"NAME", "ENGINE", "primary", "sqlserver", "1433", "false"} {
		if !strings.Contains(got, want) {
			t.Errorf("table output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "<nil>") {
		t.Errorf("a nil rendered as <nil>:\n%s", got)
	}
}

func TestTableListOfNothingWritesNothing(t *testing.T) {
	var buf bytes.Buffer
	if err := New(Table, &buf).List(nil); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Errorf("empty list wrote %q", buf.String())
	}
}

func TestJSONKeepsFieldOrder(t *testing.T) {
	var buf bytes.Buffer
	if err := New(JSON, &buf).Show(record()); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if !json.Valid([]byte(got)) {
		t.Fatalf("invalid json: %s", got)
	}
	order := []string{"name", "engine", "port", "production", "database"}
	at := -1
	for _, key := range order {
		next := strings.Index(got, `"`+key+`"`)
		if next <= at {
			t.Fatalf("key %q out of order in %s", key, got)
		}
		at = next
	}
}

func TestJSONListOfNothingIsAnEmptyArray(t *testing.T) {
	var buf bytes.Buffer
	if err := New(JSON, &buf).List(nil); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(buf.String()) != "[]" {
		t.Errorf("empty list = %q, want []", buf.String())
	}
}

func TestNoteIsHumanOnly(t *testing.T) {
	var human, machine bytes.Buffer
	if err := New(Table, &human).Note("done"); err != nil {
		t.Fatal(err)
	}
	if err := New(JSON, &machine).Note("done"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(human.String(), "done") {
		t.Error("table writer dropped the note")
	}
	if machine.Len() != 0 {
		t.Errorf("json writer wrote a note: %q", machine.String())
	}
}

func TestValue(t *testing.T) {
	var human, machine bytes.Buffer
	if err := New(Table, &human).Value("json"); err != nil {
		t.Fatal(err)
	}
	if err := New(JSON, &machine).Value("json"); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(human.String()) != "json" {
		t.Errorf("table value = %q", human.String())
	}
	if strings.TrimSpace(machine.String()) != `"json"` {
		t.Errorf("json value = %q", machine.String())
	}
}
