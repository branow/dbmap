package config

// Setting is one scalar key reachable through `dbmap config`. The keys are a
// table, so `config list` and `config set` cannot drift apart.
type Setting struct {
	Key string
	Doc string
	get func(*Config) string
	set func(*Config, string) error
}

var settings = []Setting{
	{
		Key: "current_profile",
		Doc: "the profile commands use when --profile is not given",
		get: func(c *Config) string { return c.CurrentProfile },
		set: func(c *Config, v string) error {
			if v == "" {
				c.CurrentProfile = ""
				return nil
			}
			return c.Switch(v)
		},
	},
	{
		Key: "output",
		Doc: "the default output format when -o is not given",
		get: func(c *Config) string { return c.DefaultOutput },
		set: func(c *Config, v string) error { c.DefaultOutput = v; return nil },
	},
	{
		Key: "secrets.fallback",
		Doc: "never, or plaintext to allow a 0600 file when the keychain fails",
		get: func(c *Config) string { return string(c.Secrets.Fallback) },
		set: func(c *Config, v string) error {
			if err := validateFallback(v); err != nil {
				return err
			}
			c.Secrets.Fallback = Fallback(v)
			return nil
		},
	},
}

// Settings lists the keys `dbmap config` exposes, in display order.
func Settings() []Setting {
	out := make([]Setting, len(settings))
	copy(out, settings)
	return out
}

// Get reads one setting's stored value, before any flag or environment layer.
func (c *Config) Get(key string) (string, error) {
	s, err := setting(key)
	if err != nil {
		return "", err
	}
	return s.get(c), nil
}

// Set writes one setting. Writing current_profile is a switch, so it validates
// that the profile exists.
func (c *Config) Set(key, value string) error {
	s, err := setting(key)
	if err != nil {
		return err
	}
	return s.set(c, value)
}

func setting(key string) (Setting, error) {
	for _, s := range settings {
		if s.Key == key {
			return s, nil
		}
	}
	return Setting{}, &NotFoundError{Kind: KindSetting, Name: key}
}
