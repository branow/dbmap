package cmd

import (
	"runtime"

	"github.com/spf13/cobra"

	"github.com/branow/dbmap/internal/cmdutil"
	"github.com/branow/dbmap/internal/output"
)

// newVersion reports what binary is running.
func newVersion(f *cmdutil.Factory, build Build) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return f.Writer().Show(output.Record{
				{Name: "version", Value: build.Version},
				{Name: "commit", Value: build.Commit},
				{Name: "built", Value: build.Date},
				{Name: "go", Value: runtime.Version()},
				{Name: "platform", Value: runtime.GOOS + "/" + runtime.GOARCH},
			})
		},
	}
}
