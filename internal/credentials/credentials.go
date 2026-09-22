// Package credentials is the only place in dbmap that touches a secret. A
// password or an api key is fetched here at the moment it is needed and never
// reaches the config file, a log line, an error message or a test fixture.
package credentials

import (
	"encoding/json"
	"strings"
)

// Key prefixes. The two namespaces are independent: one llm key is shared by
// every connection, and one database password belongs to exactly one.
const (
	dbPrefix  = "db:"
	llmPrefix = "llm:"
)

// Service is the keychain service every dbmap entry is filed under.
const Service = "dbmap"

// DBKey is the store key holding a connection's password.
func DBKey(name string) string { return dbPrefix + name }

// LLMKey is the store key holding a backend's api key.
func LLMKey(name string) string { return llmPrefix + name }

// Secret is an opaque credential. Printing it, formatting it, marshalling it to
// JSON or to YAML all yield a placeholder, so a secret cannot leak through an
// accidental log line or a wrapped error. Reveal is the single way out.
type Secret struct{ value string }

// NewSecret wraps a raw credential value.
func NewSecret(v string) Secret { return Secret{value: v} }

// Reveal returns the raw value. Every call site should be one an auditor would
// expect: assembling a DSN, or setting an api key header.
func (s Secret) Reveal() string { return s.value }

// Empty reports whether there is no secret to store.
func (s Secret) Empty() bool { return s.value == "" }

// String hides the value.
func (s Secret) String() string { return redacted }

// GoString hides the value from %#v.
func (s Secret) GoString() string { return redacted }

// MarshalJSON hides the value from any structure that gets serialised.
func (s Secret) MarshalJSON() ([]byte, error) { return json.Marshal(redacted) }

// MarshalYAML hides the value from any structure written to config.yml.
func (s Secret) MarshalYAML() (any, error) { return redacted, nil }

const redacted = "[redacted]"

// Store holds secrets by key. Every implementation is interchangeable, so a
// test runs against an in-memory fake and CI against environment variables.
type Store interface {
	// Get returns the secret for a key, or a NotFoundError.
	Get(key string) (Secret, error)
	// Set stores a secret under a key, replacing any previous value.
	Set(key string, secret Secret) error
	// Delete removes a key. Deleting an absent key is not an error.
	Delete(key string) error
}

// envName maps a store key to the environment variable that can supply it:
// db:main becomes DBMAP_SECRET_DB_MAIN.
func envName(key string) string {
	clean := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		default:
			return '_'
		}
	}, key)
	return "DBMAP_SECRET_" + strings.ToUpper(clean)
}
