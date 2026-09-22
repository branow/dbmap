package iostreams

import (
	"errors"
	"strings"
	"testing"
)

func TestPrompt(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		def     string
		tty     bool
		noInput bool
		want    string
		wantErr error
	}{
		{name: "answer", input: "example.internal\n", tty: true, want: "example.internal"},
		{name: "default on empty answer", input: "\n", def: "5432", tty: true, want: "5432"},
		{name: "no terminal", input: "", tty: false, wantErr: ErrNoInput},
		{name: "no input mode", input: "x\n", tty: true, noInput: true, wantErr: ErrNoInput},
		{name: "stream ends", input: "", tty: true, wantErr: ErrCancelled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			streams, in, _, _ := Test()
			in.WriteString(tt.input)
			streams.SetStdinTTY(tt.tty)
			streams.SetStdoutTTY(tt.tty)
			streams.SetNeverPrompt(tt.noInput)

			got, err := streams.Prompt("host", tt.def)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("answer = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPromptPasswordDoesNotEcho(t *testing.T) {
	streams, in, out, errOut := Test()
	in.WriteString("s3cret\n")
	streams.SetStdinTTY(true)
	streams.SetStdoutTTY(true)

	got, err := streams.PromptPassword("password")
	if err != nil {
		t.Fatalf("PromptPassword: %v", err)
	}
	if got != "s3cret" {
		t.Errorf("secret = %q", got)
	}
	if strings.Contains(out.String()+errOut.String(), "s3cret") {
		t.Error("the secret was written to a stream")
	}
}

func TestPromptPasswordRefusesWithoutTerminal(t *testing.T) {
	streams, _, _, _ := Test()
	if _, err := streams.PromptPassword("password"); !errors.Is(err, ErrNoInput) {
		t.Fatalf("error = %v, want ErrNoInput", err)
	}
}

func TestConfirm(t *testing.T) {
	tests := []struct {
		input string
		def   bool
		want  bool
	}{
		{input: "y\n", want: true},
		{input: "YES\n", want: true},
		{input: "n\n", def: true, want: false},
		{input: "\n", def: true, want: true},
		{input: "\n", def: false, want: false},
	}
	for _, tt := range tests {
		t.Run(strings.TrimSpace(tt.input)+"/"+boolName(tt.def), func(t *testing.T) {
			streams, in, _, _ := Test()
			in.WriteString(tt.input)
			streams.SetStdinTTY(true)
			streams.SetStdoutTTY(true)
			got, err := streams.Confirm("sure", tt.def)
			if err != nil {
				t.Fatalf("Confirm: %v", err)
			}
			if got != tt.want {
				t.Errorf("answer = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestReadAllTrimsNewline(t *testing.T) {
	streams, in, _, _ := Test()
	in.WriteString("from-stdin\n")
	got, err := streams.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if got != "from-stdin" {
		t.Errorf("value = %q", got)
	}
}

func TestCanPrompt(t *testing.T) {
	tests := []struct{ in, out, never, want bool }{
		{in: true, out: true, want: true},
		{in: false, out: true, want: false},
		{in: true, out: false, want: false},
		{in: true, out: true, never: true, want: false},
	}
	for _, tt := range tests {
		streams, _, _, _ := Test()
		streams.SetStdinTTY(tt.in)
		streams.SetStdoutTTY(tt.out)
		streams.SetNeverPrompt(tt.never)
		if got := streams.CanPrompt(); got != tt.want {
			t.Errorf("CanPrompt(in=%v out=%v never=%v) = %v", tt.in, tt.out, tt.never, got)
		}
	}
}

func TestPaletteRespectsColorSetting(t *testing.T) {
	streams, _, _, _ := Test()
	streams.SetColorEnabled(false)
	if got := streams.Color().Apply(Bold, "text"); got != "text" {
		t.Errorf("colorless output = %q", got)
	}
	streams.SetColorEnabled(true)
	if got := streams.Color().Apply(Bold, "text"); got == "text" {
		t.Error("color enabled but no escape written")
	}
	if got := streams.Color().Apply(Style("unknown"), "text"); got != "text" {
		t.Errorf("unknown style = %q, want the text untouched", got)
	}
}

func boolName(v bool) string {
	if v {
		return "default-yes"
	}
	return "default-no"
}
