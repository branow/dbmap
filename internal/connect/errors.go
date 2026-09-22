package connect

import (
	"fmt"
	"strings"

	"github.com/branow/dbmap/internal/config"
)

// ProductionError refuses a connection the config marks as production. This
// tool reads a catalog and samples rows; neither belongs against a production
// instance, and the refusal is here rather than in a command so that no future
// caller can reach a production database by taking a different route.
type ProductionError struct {
	Name string
}

func (e *ProductionError) Error() string {
	return fmt.Sprintf("connection %q is marked production and will not be opened", e.Name)
}

// CredentialCacheError reports a Kerberos credential cache the pure-Go driver
// cannot use. It is the failure everyone hits first on macOS, where the system
// default cache type is API: — keychain-backed, readable by the system GSSAPI
// and by nothing else — while gokrb5 reads FILE: caches only.
//
// The remedy is spelled out because the diagnosis is otherwise impossible from
// the driver's own message, which reports only that no credentials were found.
type CredentialCacheError struct {
	// Path is the cache this connection would use.
	Path string
	// Type is the cache type found when the type is the problem: "API",
	// "KEYRING", "KCM". Empty when the cache is simply missing.
	Type string
	Err  error
}

func (e *CredentialCacheError) Error() string {
	var what string
	if e.Type != "" {
		what = fmt.Sprintf("the Kerberos credential cache is a %s: cache, "+
			"which the pure-Go driver cannot read", e.Type)
	} else {
		what = fmt.Sprintf("the Kerberos credential cache %q cannot be read", e.Path)
	}
	return fmt.Sprintf("%s; run %s and point the connection at that file", what, e.Remedy())
}

// Remedy is the command that fixes this, as a string a command can print on its
// own line.
func (e *CredentialCacheError) Remedy() string {
	return fmt.Sprintf("kinit -c FILE:%s", e.Path)
}

func (e *CredentialCacheError) Unwrap() error { return e.Err }

// UnsupportedError reports a configuration this build cannot serve, named
// precisely rather than surfaced as a generic authentication failure. A
// cross-realm Kerberos setup fails with "Cannot generate SSPI context", which
// says nothing about realms and sends the reader looking at the wrong thing;
// naming it is the whole point of this type.
type UnsupportedError struct {
	// Configuration is what is not supported, in the user's terms.
	Configuration string
	// Reason is why, in one clause.
	Reason string
	// Remedy is what can be done instead, when anything can.
	Remedy string
}

func (e *UnsupportedError) Error() string {
	parts := []string{fmt.Sprintf("%s is not supported", e.Configuration)}
	if e.Reason != "" {
		parts = append(parts, e.Reason)
	}
	if e.Remedy != "" {
		parts = append(parts, e.Remedy)
	}
	return strings.Join(parts, ": ")
}

// ConnectError reports a connection that could not be opened or reached. It
// carries the connection NAME and the engine, never the data source name: a
// DSN holds the password, and an error is the most widely copied string a
// program produces.
type ConnectError struct {
	Name   string
	Engine config.Engine
	Err    error
}

func (e *ConnectError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("connection %q could not be opened", e.Name)
	}
	return fmt.Sprintf("connection %q could not be opened: %v", e.Name, e.Err)
}

func (e *ConnectError) Unwrap() error { return e.Err }
