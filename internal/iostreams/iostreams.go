// Package iostreams owns a command's streams: where it reads from, where it
// writes to, whether those streams are a terminal, and how a missing value is
// asked for. Commands never touch os.Stdin/os.Stdout directly, so every one of
// them is testable with buffers.
package iostreams

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// ErrCancelled reports input that ended before an answer arrived, such as EOF
// on a prompt. It is the user aborting, not a failure.
var ErrCancelled = errors.New("input cancelled")

// ErrNoInput reports a value that had to be prompted for while prompting is
// unavailable (no terminal, or --no-input). Commands fail with it instead of
// blocking on a stream nobody is going to write to.
var ErrNoInput = errors.New("input disabled")

// IOStreams carries the three streams plus everything that depends on them:
// terminal detection, color, and prompting.
type IOStreams struct {
	In     io.Reader
	Out    io.Writer
	ErrOut io.Writer

	in           *bufio.Reader
	inTTY        bool
	outTTY       bool
	inFD         int
	color        bool
	neverPrompt  bool
	readPassword func(fd int) ([]byte, error)
}

// System wires the streams to the process and detects the terminal once, at
// startup, because detection is a property of the process and not of a call.
func System() *IOStreams {
	s := &IOStreams{
		In:           os.Stdin,
		Out:          os.Stdout,
		ErrOut:       os.Stderr,
		in:           bufio.NewReader(os.Stdin),
		inFD:         int(os.Stdin.Fd()),
		readPassword: term.ReadPassword,
	}
	s.inTTY = term.IsTerminal(int(os.Stdin.Fd()))
	s.outTTY = term.IsTerminal(int(os.Stdout.Fd()))
	s.color = s.outTTY && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
	return s
}

// Test returns buffer-backed streams and the three buffers, so a test can feed
// input and assert on output. Both streams report "not a terminal" until a test
// says otherwise, which is the shape CI runs in.
func Test() (*IOStreams, *bytes.Buffer, *bytes.Buffer, *bytes.Buffer) {
	in, out, errOut := &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{}
	s := &IOStreams{In: in, Out: out, ErrOut: errOut, in: bufio.NewReader(in)}
	// A hidden prompt reads a plain line from the test input: no terminal is
	// ever opened by a unit test.
	s.readPassword = func(int) ([]byte, error) {
		line, err := s.in.ReadString('\n')
		return []byte(strings.TrimRight(line, "\r\n")), err
	}
	return s, in, out, errOut
}

// IsStdinTTY reports whether input comes from a terminal.
func (s *IOStreams) IsStdinTTY() bool { return s.inTTY }

// IsStdoutTTY reports whether output goes to a terminal.
func (s *IOStreams) IsStdoutTTY() bool { return s.outTTY }

// SetStdinTTY overrides input terminal detection.
func (s *IOStreams) SetStdinTTY(v bool) { s.inTTY = v }

// SetStdoutTTY overrides output terminal detection.
func (s *IOStreams) SetStdoutTTY(v bool) { s.outTTY = v }

// ColorEnabled reports whether escape sequences may be written.
func (s *IOStreams) ColorEnabled() bool { return s.color }

// SetColorEnabled turns color on or off, for --no-color and for tests.
func (s *IOStreams) SetColorEnabled(v bool) { s.color = v }

// SetNeverPrompt disables prompting for the whole process (--no-input).
func (s *IOStreams) SetNeverPrompt(v bool) { s.neverPrompt = v }

// CanPrompt reports whether a question may be asked: both ends of the
// conversation must be a terminal and prompting must not be disabled.
func (s *IOStreams) CanPrompt() bool {
	return s.inTTY && s.outTTY && !s.neverPrompt
}

// Color returns the palette in force. Its methods are identity functions when
// color is off, so callers never branch on the setting.
func (s *IOStreams) Color() *Palette { return &Palette{enabled: s.color} }

// Prompt asks for a value and returns the answer, or def when the answer is
// empty. It never blocks when prompting is unavailable.
func (s *IOStreams) Prompt(label, def string) (string, error) {
	if !s.CanPrompt() {
		return "", fmt.Errorf("%w: %s", ErrNoInput, label)
	}
	if def != "" {
		fmt.Fprintf(s.ErrOut, "%s [%s]: ", label, def)
	} else {
		fmt.Fprintf(s.ErrOut, "%s: ", label)
	}
	line, err := s.line()
	if err != nil {
		return "", err
	}
	if line == "" {
		return def, nil
	}
	return line, nil
}

// PromptPassword asks for a secret without echoing it. The value is returned to
// the caller and never written to any stream.
func (s *IOStreams) PromptPassword(label string) (string, error) {
	if !s.CanPrompt() {
		return "", fmt.Errorf("%w: %s", ErrNoInput, label)
	}
	fmt.Fprintf(s.ErrOut, "%s: ", label)
	raw, err := s.readPassword(s.inFD)
	fmt.Fprintln(s.ErrOut)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return "", ErrCancelled
		}
		return "", err
	}
	return string(raw), nil
}

// Confirm asks a yes/no question and returns def on an empty answer.
func (s *IOStreams) Confirm(label string, def bool) (bool, error) {
	hint := "y/N"
	if def {
		hint = "Y/n"
	}
	answer, err := s.Prompt(fmt.Sprintf("%s (%s)", label, hint), "")
	if err != nil {
		return false, err
	}
	switch strings.ToLower(answer) {
	case "":
		return def, nil
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

// ReadAll drains the input stream and trims the trailing newline. It backs the
// --password-stdin family of flags, which is how a secret reaches a
// non-interactive run without ever appearing in a process argument.
func (s *IOStreams) ReadAll() (string, error) {
	raw, err := io.ReadAll(s.in)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(raw), "\r\n"), nil
}

// line reads one answer, translating a closed stream into a cancellation.
func (s *IOStreams) line() (string, error) {
	text, err := s.in.ReadString('\n')
	if err != nil {
		if errors.Is(err, io.EOF) && strings.TrimSpace(text) == "" {
			return "", ErrCancelled
		}
		if !errors.Is(err, io.EOF) {
			return "", err
		}
	}
	return strings.TrimSpace(text), nil
}
