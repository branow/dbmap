package config

import (
	"regexp"
	"sort"
	"strconv"
)

// auths lists which authentication modes each engine accepts.
var auths = map[Engine][]Auth{
	SQLServer: {SQLLogin, Kerberos},
	Postgres:  {SCRAM, Kerberos},
}

// providers lists the llm providers and what each one takes. ClaudeCode runs a
// local binary, so it has neither a key nor an endpoint.
var providers = map[Provider]struct{ NeedsKey, HasEndpoint bool }{
	Anthropic:  {NeedsKey: true, HasEndpoint: true},
	OpenAI:     {NeedsKey: true, HasEndpoint: true},
	ClaudeCode: {NeedsKey: false, HasEndpoint: false},
}

var fallbacks = []Fallback{Never, Plaintext}

// secretless lists the auth modes where the ticket or the OS supplies the
// credential.
var secretless = map[Auth]bool{Kerberos: true}

// name is the shape of every entry name: safe to type, and safe in a keychain
// key.
var name = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Engines lists the supported engines.
func Engines() []string {
	out := make([]string, 0, len(auths))
	for e := range auths {
		out = append(out, string(e))
	}
	sort.Strings(out)
	return out
}

// Providers lists the supported llm providers.
func Providers() []string {
	out := make([]string, 0, len(providers))
	for p := range providers {
		out = append(out, string(p))
	}
	sort.Strings(out)
	return out
}

// Fallbacks lists the secret-storage policies.
func Fallbacks() []string {
	out := make([]string, len(fallbacks))
	for i, f := range fallbacks {
		out[i] = string(f)
	}
	return out
}

// Auths lists the authentication modes an engine accepts.
func Auths(e Engine) []string {
	modes := auths[e]
	out := make([]string, len(modes))
	for i, m := range modes {
		out[i] = string(m)
	}
	return out
}

// NeedsPassword reports whether an auth mode requires a stored password.
func NeedsPassword(a Auth) bool { return !secretless[a] }

// NeedsAPIKey reports whether a provider requires a stored api key.
func NeedsAPIKey(p Provider) bool { return providers[p].NeedsKey }

// UsesBaseURL reports whether a provider has an endpoint at all.
func UsesBaseURL(p Provider) bool { return providers[p].HasEndpoint }

// Validate checks every entry is well formed and every profile binds names
// that exist.
func (c *Config) Validate() error {
	for entry, value := range c.Connections {
		if err := validateConnection(entry, value); err != nil {
			return err
		}
	}
	for entry, value := range c.Backends {
		if err := validateBackend(entry, value); err != nil {
			return err
		}
	}
	// A profile pointing at an entry that no longer exists is not a reason to
	// refuse the whole file: that would make one removal brick every command,
	// including the ones that would repair it. A dangling binding fails when the
	// profile is actually used, and nowhere else.
	for entry := range c.Profiles {
		if err := validateName(KindProfile, entry); err != nil {
			return err
		}
	}
	if c.Secrets.Fallback != "" {
		if err := validateFallback(string(c.Secrets.Fallback)); err != nil {
			return err
		}
	}
	return nil
}

func validateConnection(entry string, value Connection) error {
	if err := validateName(KindConnection, entry); err != nil {
		return err
	}
	modes, ok := auths[value.Engine]
	if !ok {
		return &InvalidError{Field: "engine", Value: string(value.Engine), Allowed: Engines()}
	}
	if value.Host == "" {
		return &InvalidError{Field: "host", Value: "", Reason: "must not be empty"}
	}
	if value.Port < 0 || value.Port > 65535 {
		return &InvalidError{Field: "port", Value: strconv.Itoa(value.Port),
			Reason: "must be between 0 and 65535"}
	}
	for _, mode := range modes {
		if mode == value.Auth {
			return nil
		}
	}
	return &InvalidError{Field: "auth", Value: string(value.Auth), Allowed: Auths(value.Engine)}
}

func validateBackend(entry string, value Backend) error {
	if err := validateName(KindBackend, entry); err != nil {
		return err
	}
	if _, ok := providers[value.Provider]; !ok {
		return &InvalidError{Field: "provider", Value: string(value.Provider),
			Allowed: Providers()}
	}
	return nil
}

func validateName(kind Kind, entry string) error {
	if name.MatchString(entry) {
		return nil
	}
	return &InvalidError{Field: string(kind) + " name", Value: entry,
		Reason: "must start with a letter or digit and hold only letters, digits, dot, dash " +
			"or underscore"}
}

func validateFallback(value string) error {
	for _, f := range fallbacks {
		if string(f) == value {
			return nil
		}
	}
	return &InvalidError{Field: "secrets.fallback", Value: value, Allowed: Fallbacks()}
}

// ParseEngine validates an engine name coming from a flag or a prompt.
func ParseEngine(value string) (Engine, error) {
	if _, ok := auths[Engine(value)]; ok {
		return Engine(value), nil
	}
	return "", &InvalidError{Field: "engine", Value: value, Allowed: Engines()}
}

// ParseAuth validates an auth mode against the engine that must accept it.
func ParseAuth(e Engine, value string) (Auth, error) {
	for _, mode := range auths[e] {
		if string(mode) == value {
			return mode, nil
		}
	}
	return "", &InvalidError{Field: "auth", Value: value, Allowed: Auths(e)}
}

// ParseProvider validates an llm provider name.
func ParseProvider(value string) (Provider, error) {
	if _, ok := providers[Provider(value)]; ok {
		return Provider(value), nil
	}
	return "", &InvalidError{Field: "provider", Value: value, Allowed: Providers()}
}

// NeedsUsername reports whether an auth mode requires a login name.
func NeedsUsername(a Auth) bool { return !secretless[a] }
