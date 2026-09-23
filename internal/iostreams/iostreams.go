// Package iostreams owns a command's streams. Commands never touch
// os.Stdin/os.Stdout, so every one of them is testable with buffers.
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

// ErrCancelled reports input that ended before an answer arrived.
var ErrCancelled = errors.New("input cancelled")

// ErrNoInput reports a prompt needed while prompting is unavailable, so a
// command fails instead of blocking on a stream nobody will write to.
var ErrNoInput = errors.New("input disabled")

// IOStreams carries the three streams and what depends on them.
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

// System wires the streams to the process, detecting the terminal once.
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

// Test returns buffer-backed streams and their buffers. Neither reports a
// terminal until a test says otherwise.
func Test() (*IOStreams, *bytes.Buffer, *bytes.Buffer, *bytes.Buffer) {
	in, out, errOut := &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{}
	s := &IOStreams{In: in, Out: out, ErrOut: errOut, in: bufio.NewReader(in)}
	// A hidden prompt reads a plain line: a unit test opens no terminal.
	s.readPassword = func(int) ([]byte, error) {
		line, err := s.in.ReadString('\n')
		return []byte(strings.TrimRight(line, "\r\n")), err
	}
	return s, in, out, errOut
}

func (s *IOStreams) IsStdinTTY() bool { return s.inTTY }

func (s *IOStreams) IsStdoutTTY() bool { return s.outTTY }

func (s *IOStreams) SetStdinTTY(v bool) { s.inTTY = v }

func (s *IOStreams) SetStdoutTTY(v bool) { s.outTTY = v }

func (s *IOStreams) ColorEnabled() bool { return s.color }

func (s *IOStreams) SetColorEnabled(v bool) { s.color = v }

// SetNeverPrompt disables prompting for the whole process (--no-input).
func (s *IOStreams) SetNeverPrompt(v bool) { s.neverPrompt = v }

// CanPrompt reports whether a question may be asked.
func (s *IOStreams) CanPrompt() bool {
	return s.inTTY && s.outTTY && !s.neverPrompt
}

// Color returns the palette in force; its methods are identity functions when
// color is off, so callers never branch on the setting.
func (s *IOStreams) Color() *Palette { return &Palette{enabled: s.color} }

// Prompt asks for a value and returns the answer, or def when it is empty.
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

// PromptPassword asks for a secret without echoing it.
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

// ReadAll drains the input stream and trims the trailing newline. It backs
// --password-stdin, which keeps a secret out of a process argument.
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
