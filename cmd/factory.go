package cmd

import (
	"context"
	"time"

	"github.com/branow/dbmap/internal/cmdutil"
	"github.com/branow/dbmap/internal/config"
	"github.com/branow/dbmap/internal/connect"
	"github.com/branow/dbmap/internal/credentials"
	"github.com/branow/dbmap/internal/iostreams"
)

// probeTimeout bounds a verification: generous for a round trip to a slow
// instance, short enough that a host silently dropping packets returns.
const probeTimeout = 10 * time.Second

// NewFactory builds the real dependency bundle. The secret store is opened
// later, in the root command's pre-run, because a flag decides which store.
func NewFactory() (*cmdutil.Factory, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	return &cmdutil.Factory{
		IO:     iostreams.System(),
		Config: cfg,
		Probes: cmdutil.Probes{Connection: verifyConnection},
	}, nil
}

// verifyConnection is the cheapest real proof that a connection works: open the
// pool, ping, close. No catalog is read and no row is returned.
func verifyConnection(ctx context.Context, name string, entry config.Connection,
	secret credentials.Secret) error {
	pool, err := connect.Open(name, entry, secret.Reveal())
	if err != nil {
		return err
	}
	defer pool.Close()

	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	return pool.Verify(ctx)
}

// openStore assembles the secret store the resolved policy selects. The default
// policy refuses a plaintext file outright, so the environment path is what
// lets a headless run work with no keychain at all.
//
// interactive may be true only when a person is watching a terminal: the macOS
// keychain binds an item to the storing binary's code identity, so a rebuilt
// binary is asked to re-authorize, and a dialog nobody can see blocks forever.
func openStore(c *config.Config, o config.Overrides, interactive bool) (credentials.Store, error) {
	fallback, err := c.Fallback(o)
	if err != nil {
		return nil, err
	}
	file := ""
	if c.Path() != "" {
		file = credentials.FilePath(c.Path())
	}
	return credentials.New(credentials.Options{
		Policy:      credentials.Policy(fallback),
		Service:     credentials.Service,
		File:        file,
		Interactive: interactive,
	})
}
