// Package config owns config.yml. No struct here has a secret field: a
// password or an api key lives in the keychain, reached only through
// internal/credentials.
package config

import "os"

// Engine names a supported database engine.
type Engine string

const (
	SQLServer Engine = "sqlserver"
	Postgres  Engine = "postgres"
)

// Auth names how a connection authenticates.
type Auth string

const (
	SQLLogin Auth = "sqllogin"
	Kerberos Auth = "kerberos"
	SCRAM    Auth = "scram"
)

// Provider names an llm backend implementation.
type Provider string

const (
	Anthropic  Provider = "anthropic"
	OpenAI     Provider = "openai"
	ClaudeCode Provider = "claudecode"
)

// Fallback is the secret-storage policy when the OS keychain cannot answer.
// Never is the default: plaintext is opt-in only.
type Fallback string

const (
	Never     Fallback = "never"
	Plaintext Fallback = "plaintext"
)

// Connection is one named database connection. Its password, when the auth
// mode needs one, lives in the keychain under db:<name>.
type Connection struct {
	Engine   Engine `yaml:"engine"`
	Host     string `yaml:"host"`
	Port     int    `yaml:"port,omitempty"`
	Database string `yaml:"database,omitempty"`
	Auth     Auth   `yaml:"auth"`
	Username string `yaml:"username,omitempty"`
	// TrustCert accepts the server's TLS certificate without verifying it.
	//
	// Off by default: encryption that verifies nobody encrypts the traffic to
	// whoever answered. It is a per-connection setting rather than a global one
	// because the case it exists for - a server whose certificate is issued by
	// an internal authority this machine does not trust - is a property of that
	// server, and the alternative users reach for otherwise is turning
	// encryption off entirely.
	TrustCert bool `yaml:"trust_server_certificate,omitempty"`
	// Params are extra driver parameters, passed through untouched.
	Params map[string]string `yaml:"params,omitempty"`
}

// Backend is one named llm backend. Its api key, when the provider needs one,
// lives in the keychain under llm:<name>.
type Backend struct {
	Provider Provider `yaml:"provider"`
	Model    string   `yaml:"model,omitempty"`
	BaseURL  string   `yaml:"base_url,omitempty"`
}

// Profile binds one connection to one backend; it never redefines either.
type Profile struct {
	Connection string `yaml:"connection"`
	Backend    string `yaml:"backend"`
	Output     string `yaml:"output,omitempty"`
}

// Secrets holds the secret-storage policy.
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

// New returns an empty config with no file behind it; Save fails until a path
// is given.
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

// SetEnv replaces the environment lookup, so precedence is testable without
// mutating the process.
func (c *Config) SetEnv(lookup func(string) string) { c.env = lookup }

func (c *Config) lookup(name string) string {
	if c.env == nil {
		return ""
	}
	return c.env(name)
}
