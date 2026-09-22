package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	"gopkg.in/yaml.v3"
)

// Name is the file every dbmap installation reads.
const Name = "config.yml"

// dir is the per-user configuration directory, honouring the platform's own
// convention: DBMAP_CONFIG wins, then XDG_CONFIG_HOME, then %AppData% on
// Windows, then ~/.config.
func dir() (string, error) {
	if home := os.Getenv("XDG_CONFIG_HOME"); home != "" {
		return filepath.Join(home, "dbmap"), nil
	}
	if runtime.GOOS == "windows" {
		if appData := os.Getenv("AppData"); appData != "" {
			return filepath.Join(appData, "dbmap"), nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "dbmap"), nil
}

// Path reports where config.yml lives for this user. DBMAP_CONFIG overrides it
// outright, which is how a test or a CI job points at its own file.
func Path() (string, error) {
	if p := os.Getenv("DBMAP_CONFIG"); p != "" {
		return p, nil
	}
	d, err := dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, Name), nil
}

// Load reads the user's config. A missing file is an empty config, not an
// error: a first run has nothing configured yet.
func Load() (*Config, error) {
	p, err := Path()
	if err != nil {
		return nil, err
	}
	return LoadFrom(p)
}

// LoadFrom reads a config from an explicit path and validates it, because a
// file on disk is external input and is checked once, here.
func LoadFrom(path string) (*Config, error) {
	c := NewAt(path)
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return c, nil
		}
		return nil, err
	}
	if err := yaml.Unmarshal(raw, c); err != nil {
		return nil, &InvalidError{Field: "config file", Value: path, Reason: err.Error()}
	}
	c.normalise()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}

// Save writes the file with owner-only permissions. The directory is created on
// demand so `dbmap connection add` works on a machine that has never run it.
func (c *Config) Save() error {
	if c.path == "" {
		return errors.New("config has no path")
	}
	raw, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, c.path); err != nil {
		return fmt.Errorf("replacing %s: %w", filepath.Base(c.path), err)
	}
	return nil
}

// normalise restores the invariants an unmarshalled struct loses: non-nil maps
// and a working environment lookup.
func (c *Config) normalise() {
	if c.Connections == nil {
		c.Connections = map[string]Connection{}
	}
	if c.Backends == nil {
		c.Backends = map[string]Backend{}
	}
	if c.Profiles == nil {
		c.Profiles = map[string]Profile{}
	}
	if c.env == nil {
		c.env = os.Getenv
	}
}
