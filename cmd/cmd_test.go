package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/branow/dbmap/internal/cmdutil"
	"github.com/branow/dbmap/internal/config"
	"github.com/branow/dbmap/internal/credentials"
	"github.com/branow/dbmap/internal/iostreams"
)

// harness is a whole dbmap with nothing real behind it: buffer streams, a
// config file inside the test's temporary directory, and an in-memory secret
// store. No terminal, no keychain, no network.
type harness struct {
	factory *cmdutil.Factory
	in      *bytes.Buffer
	out     *bytes.Buffer
	errOut  *bytes.Buffer
	store   *credentials.Fake
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	streams, in, out, errOut := iostreams.Test()
	cfg := config.NewAt(filepath.Join(t.TempDir(), config.Name))
	cfg.SetEnv(func(string) string { return "" })
	store := credentials.NewFake()
	return &harness{
		factory: &cmdutil.Factory{IO: streams, Config: cfg, Store: store},
		in:      in,
		out:     out,
		errOut:  errOut,
		store:   store,
	}
}

// interactive makes the harness look like a terminal, so prompts run.
func (h *harness) interactive(answers string) {
	h.factory.IO.SetStdinTTY(true)
	h.factory.IO.SetStdoutTTY(true)
	h.in.WriteString(answers)
}

// run executes the command tree exactly as main does.
func (h *harness) run(args ...string) error {
	h.out.Reset()
	root := NewRoot(h.factory, Build{Version: "test", Commit: "none", Date: "unknown"})
	root.SetArgs(args)
	root.SetOut(h.out)
	root.SetErr(h.errOut)
	return root.Execute()
}

// seed defines one connection, one backend and one profile without activating
// any of them, which is the state most commands are exercised from.
func (h *harness) seed(t *testing.T) {
	t.Helper()
	h.in.WriteString("s3cret\n")
	if err := h.run("connection", "add", "primary", "--engine", "sqlserver",
		"--host", "example.internal", "--auth", "sqllogin", "--username", "reader",
		"--password-stdin", "--no-input"); err != nil {
		t.Fatalf("seeding a connection: %v", err)
	}
	if err := h.run("backend", "add", "main", "--provider", "claudecode",
		"--model", "model-a", "--no-input"); err != nil {
		t.Fatalf("seeding a backend: %v", err)
	}
	if err := h.run("profile", "create", "work", "--connection", "primary",
		"--backend", "main", "--no-input"); err != nil {
		t.Fatalf("seeding a profile: %v", err)
	}
}

// read returns a file's contents, for asserting on what was written.
func read(path string) (string, error) {
	raw, err := os.ReadFile(path)
	return string(raw), err
}
