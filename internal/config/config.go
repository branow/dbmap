// Package config owns config.yml: two independent named namespaces
// (database connections and llm backends), the profiles that bind one of each,
// and the flag > env > file > default precedence chain.
//
// No struct in this package has a secret field. A password or an api key lives
// in the keychain, reached only through internal/credentials.
package config

import "os"

// Engine names a supported database engine.
type Engine string

// The supported engines.
const (
	SQLServer Engine = "sqlserver"
	Postgres  Engine = "postgres"
)

// Auth names how a connection authenticates.
type Auth string

// The supported authentication modes.
const (
	SQLLogin Auth = "sqllogin"
	Kerberos Auth = "kerberos"
	SCRAM    Auth = "scram"
)

// Provider names an llm backend implementation.
type Provider string

// The supported llm providers.
const (
	Anthropic  Provider = "anthropic"
	OpenAI     Provider = "openai"
	ClaudeCode Provider = "claudecode"
)

// Fallback is the secret-storage policy: what happens when the OS keychain
// cannot answer.
type Fallback string

// The storage policies. Never is the default, so a transient keychain failure
// can never persist a password to disk; plaintext is opt-in only.
const (
	Never     Fallback = "never"
	Plaintext Fallback = "plaintext"
)

// Connection is one named database connection, non-secret fields only. Its
// password, when the auth mode needs one, lives under the key db:<name>.
type Connection struct {
	Engine   Engine `yaml:"engine"`
	Host     string `yaml:"host"`
	Port     int    `yaml:"port,omitempty"`
	Database string `yaml:"database,omitempty"`
	Auth     Auth   `yaml:"auth"`
	Username string `yaml:"username,omitempty"`
	// Params are extra driver parameters, passed through untouched.
	Params map[string]string `yaml:"params,omitempty"`
}

// Backend is one named llm backend, non-secret fields only. The api key, when
// the provider needs one, lives under the key llm:<name>.
type Backend struct {
	Provider Provider `yaml:"provider"`
	Model    string   `yaml:"model,omitempty"`
	BaseURL  string   `yaml:"base_url,omitempty"`
}

// Profile binds one connection to one backend and carries output preferences.
// It selects; it never redefines either.
type Profile struct {
	Connection string `yaml:"connection"`
	Backend    string `yaml:"backend"`
	Output     string `yaml:"output,omitempty"`
}

// Secrets holds the secret-storage policy, which is itself not a secret.
type Secrets struct {
	Fallback Fallback `yaml:"fallback,omitempty"`
}

// Config is the whole file plus the precedence rules that read it.
type Config struct {
	CurrentProfile string                `yaml:"current_profile,omitempty"`
	DefaultOutput  string                `yaml:"output,omitempty"`
	Secrets        Secrets               `yaml:"secrets,omitempty"`
	Connections    map[string]Connection `yaml:"connections,omitempty"`
	Backends       map[string]Backend    `yaml:"backends,omitempty"`
	Profiles       map[string]Profile    `yaml:"profiles,omitempty"`

	path string
	env  func(string) string
}

// New returns an empty config with no file behind it. Save fails until a path
// is given, which keeps a test from writing outside its temporary directory.
func New() *Config {
	return &Config{
		Connections: map[string]Connection{},
		Backends:    map[string]Backend{},
		Profiles:    map[string]Profile{},
		env:         os.Getenv,
	}
}

// NewAt returns an empty config bound to a file path.
func NewAt(path string) *Config {
	c := New()
	c.path = path
	return c
}

// Path reports the file this config loads from and saves to.
func (c *Config) Path() string { return c.path }

// SetPath binds the config to a file.
func (c *Config) SetPath(p string) { c.path = p }

// SetEnv replaces the environment lookup, so the precedence chain is testable
// without mutating the process.
func (c *Config) SetEnv(lookup func(string) string) { c.env = lookup }

// lookup reads one DBMAP_* variable through the injected environment.
func (c *Config) lookup(name string) string {
	if c.env == nil {
		return ""
	}
	return c.env(name)
}
