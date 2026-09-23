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

// probeTimeout bounds a verification. It is generous enough for a round trip
// to a slow instance and short enough that `connection add` on a host that
// silently drops packets returns rather than hangs.
const probeTimeout = 10 * time.Second

// NewFactory builds the real dependency bundle: the process's streams, the
// user's config file, and the probes that verify an entry before it is stored.
// The secret store is opened later, in the root command's pre-run, because
// which store it is depends on a flag.
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
// pool, ping, close. No catalog is read and no row is returned. It is what
// makes a typo fail at setup instead of on the first real run.
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
// policy refuses a plaintext file outright, which is why the environment path
// exists: it is what lets a headless run work with no keychain at all.
//
// interactive decides whether the keychain may raise a dialog. It must be true
// only when a person is actually watching a terminal: the macOS keychain binds
// an item to the storing binary's code identity, so a rebuilt binary is asked to
// re-authorize, and a dialog nobody can see blocks the process forever.
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
