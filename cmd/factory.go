package cmd

import (
	"github.com/branow/dbmap/internal/cmdutil"
	"github.com/branow/dbmap/internal/config"
	"github.com/branow/dbmap/internal/credentials"
	"github.com/branow/dbmap/internal/iostreams"
)

// NewFactory builds the real dependency bundle: the process's streams and the
// user's config file. The secret store is opened later, in the root command's
// pre-run, because which store it is depends on a flag.
func NewFactory() (*cmdutil.Factory, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	return &cmdutil.Factory{IO: iostreams.System(), Config: cfg}, nil
}

// openStore assembles the secret store the resolved policy selects. The default
// policy refuses a plaintext file outright, which is why the environment path
// exists: it is what lets a headless run work with no keychain at all.
func openStore(c *config.Config, o config.Overrides) (credentials.Store, error) {
	fallback, err := c.Fallback(o)
	if err != nil {
		return nil, err
	}
	file := ""
	if c.Path() != "" {
		file = credentials.FilePath(c.Path())
	}
	return credentials.New(credentials.Options{
		Policy:  credentials.Policy(fallback),
		Service: credentials.Service,
		File:    file,
	})
}
