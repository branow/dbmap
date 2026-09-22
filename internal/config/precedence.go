package config

import "strconv"

// Environment variable names read by the precedence chain. They are listed
// together because they are the tool's public environment contract.
const (
	EnvProfile  = "DBMAP_PROFILE"
	EnvOutput   = "DBMAP_OUTPUT"
	EnvNoInput  = "DBMAP_NO_INPUT"
	EnvQuiet    = "DBMAP_QUIET"
	EnvFallback = "DBMAP_SECRETS_FALLBACK"
)

// DefaultOutputFormat is the built-in bottom of the output chain.
const DefaultOutputFormat = "table"

// Overrides is the flag layer of the precedence chain. A zero string and a nil
// pointer both mean "the flag was not given", so the next layer decides.
type Overrides struct {
	Profile string
	Output  string
	NoInput *bool
	Quiet   *bool
	Plain   *bool
}

// ProfileName resolves which profile is active:
// flag, DBMAP_PROFILE, current_profile, then none.
func (c *Config) ProfileName(o Overrides) string {
	if o.Profile != "" {
		return o.Profile
	}
	if v := c.lookup(EnvProfile); v != "" {
		return v
	}
	return c.CurrentProfile
}

// Output resolves the output format name:
// flag, DBMAP_OUTPUT, the active profile's preference, the file default, then
// the built-in "table". The name is validated by the caller that renders it.
func (c *Config) Output(o Overrides) string {
	if o.Output != "" {
		return o.Output
	}
	if v := c.lookup(EnvOutput); v != "" {
		return v
	}
	if profile, err := c.Profile(c.ProfileName(o)); err == nil && profile.Output != "" {
		return profile.Output
	}
	if c.DefaultOutput != "" {
		return c.DefaultOutput
	}
	return DefaultOutputFormat
}

// NoInput resolves whether prompting is disabled: flag, DBMAP_NO_INPUT, then
// false. It is deliberately not a file setting; it describes one invocation.
func (c *Config) NoInput(o Overrides) bool {
	return c.flag(o.NoInput, EnvNoInput)
}

// Quiet resolves whether status notes are suppressed.
func (c *Config) Quiet(o Overrides) bool {
	return c.flag(o.Quiet, EnvQuiet)
}

// Fallback resolves the secret-storage policy:
// --allow-plaintext, DBMAP_SECRETS_FALLBACK, the file, then never. The default
// is never so a keychain failure can not quietly write a password to disk.
func (c *Config) Fallback(o Overrides) (Fallback, error) {
	if o.Plain != nil && *o.Plain {
		return Plaintext, nil
	}
	if v := c.lookup(EnvFallback); v != "" {
		if err := validateFallback(v); err != nil {
			return "", err
		}
		return Fallback(v), nil
	}
	if c.Secrets.Fallback != "" {
		if err := validateFallback(string(c.Secrets.Fallback)); err != nil {
			return "", err
		}
		return c.Secrets.Fallback, nil
	}
	return Never, nil
}

// Active returns the profile in force and its bound entries. It reports
// NotFoundError when nothing is current, which is the state a first run is in.
func (c *Config) Active(o Overrides) (string, Profile, error) {
	selected := c.ProfileName(o)
	if selected == "" {
		return "", Profile{}, &NotFoundError{Kind: KindProfile, Name: ""}
	}
	profile, err := c.Profile(selected)
	if err != nil {
		return "", Profile{}, err
	}
	return selected, profile, nil
}

// flag resolves one boolean through flag then environment. Any value other than
// a false-looking one turns the switch on, because `DBMAP_QUIET=1` must work.
func (c *Config) flag(override *bool, env string) bool {
	if override != nil {
		return *override
	}
	if v := c.lookup(env); v != "" {
		on, err := strconv.ParseBool(v)
		return err != nil || on
	}
	return false
}
