// Command dbmap reads a database catalog and writes a compact index an agent
// can read in one pass. It is the only place that ends the process, always
// through cmdutil.ExitCode: an error's type, never its text, picks the status.
package main

import (
	"fmt"
	"os"

	"github.com/branow/dbmap/cmd"
	"github.com/branow/dbmap/internal/cmdutil"
)

// Build information, stamped by the linker at release time.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() { os.Exit(run()) }

func run() int {
	factory, err := cmd.NewFactory()
	if err != nil {
		fmt.Fprintln(os.Stderr, "dbmap:", err)
		return cmdutil.ExitCode(err)
	}
	root := cmd.NewRoot(factory, cmd.Build{Version: version, Commit: commit, Date: date})
	if err := root.Execute(); err != nil {
		fmt.Fprintln(factory.IO.ErrOut, "dbmap:", err)
		return cmdutil.ExitCode(err)
	}
	return cmdutil.ExitOK
}
