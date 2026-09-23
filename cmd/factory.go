package cmd

import (
	"context"
	"encoding/json"
	"time"

	"github.com/branow/dbmap/internal/cmdutil"
	"github.com/branow/dbmap/internal/config"
	"github.com/branow/dbmap/internal/connect"
	"github.com/branow/dbmap/internal/credentials"
	"github.com/branow/dbmap/internal/iostreams"
	"github.com/branow/dbmap/llm"
	"github.com/branow/dbmap/llm/provider"
)

// probeTimeout bounds a verification: generous for a round trip to a slow
// instance, short enough that a host silently dropping packets returns.
const probeTimeout = 10 * time.Second

// backendProbeTimeout is longer: a model call is not a handshake, and the
// claudecode provider starts a process before it answers.
const backendProbeTimeout = 90 * time.Second

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
		Probes: cmdutil.Probes{Connection: verifyConnection, Backend: verifyBackend},
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

// verifyBackend is the cheapest real proof that a backend answers: build the
// client and make one tiny schema-constrained call. Without this, `backend add`
// stored anything at all while advertising a --no-verify flag.
func verifyBackend(ctx context.Context, name string, entry config.Backend,
	secret credentials.Secret) error {
	client, err := provider.New(llm.Config{
		Provider: string(entry.Provider),
		Model:    entry.Model,
		BaseURL:  entry.BaseURL,
		APIKey:   secret.Reveal(),
	})
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, backendProbeTimeout)
	defer cancel()
	_, err = client.Complete(ctx, llm.Request{
		Prompt: "Reply with the word ok.",
		Schema: json.RawMessage(`{"type":"object","additionalProperties":false,` +
			`"required":["ok"],"properties":{"ok":{"type":"string"}}}`),
		MaxTokens: 64,
	})
	return err
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
