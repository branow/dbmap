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

// Stage names where in the Kerberos exchange something failed. It is a field
// rather than a sentence because the three stages fail for entirely different
// reasons: a missing realm file, an expired ticket, and a server that said no
// are three problems with three fixes.
type Stage string

// The stages of the exchange, in the order they happen.
const (
	StageConfig     Stage = "reading the Kerberos configuration"
	StageCredential Stage = "reading the credential cache"
	StageTicket     Stage = "requesting a service ticket"
	StageReply      Stage = "reading the server's answer"
)

// KerberosError reports a failure inside the GSSAPI exchange this package
// performs itself, which is the Postgres path: pgx provides the hook and no
// implementation, so the exchange is ours and so are its failures.
type KerberosError struct {
	Stage Stage
	// Reason is what went wrong, when this package knows better than the
	// underlying error does.
	Reason string
	Err    error
}

func (e *KerberosError) Error() string {
	switch {
	case e.Reason != "" && e.Err != nil:
		return fmt.Sprintf("kerberos failed %s: %s: %v", e.Stage, e.Reason, e.Err)
	case e.Reason != "":
		return fmt.Sprintf("kerberos failed %s: %s", e.Stage, e.Reason)
	case e.Err != nil:
		return fmt.Sprintf("kerberos failed %s: %v", e.Stage, e.Err)
	default:
		return fmt.Sprintf("kerberos failed %s", e.Stage)
	}
}

func (e *KerberosError) Unwrap() error { return e.Err }

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

// CrossRealmError reports a Kerberos setup whose host lives in a realm other
// than the ticket's.
//
// It is NOT a refusal. Measured against a live estate: gokrb5 cannot obtain a
// cross-realm service ticket — it asks its own realm's KDC and is told the
// principal is unknown — but it authenticates perfectly with a ticket already in
// the cache. Both facts matter, because the driver's own message
// ("KDC_ERR_S_PRINCIPAL_UNKNOWN: Server not found in Kerberos database") reads
// like a wrong hostname and sends the reader looking at DNS.
//
// So the remedy is the two commands that put the ticket there, and neither asks
// for a password. The realm must be spelled on the SPN: without it the tool
// assumes the local realm and fails the same way.
type CrossRealmError struct {
	// Host is the server the ticket is needed for.
	Host string
	// Realm is the configured realm, when one was configured.
	Realm string
}

func (e *CrossRealmError) Error() string {
	return fmt.Sprintf("no Kerberos service ticket for %s, and the pure-Go driver "+
		"cannot follow a realm referral to fetch one; prime the cache first", e.host())
}

// Remedy is the priming sequence, as lines a command can print verbatim.
func (e *CrossRealmError) Remedy() string {
	realm := e.Realm
	if realm == "" {
		realm = "<HOST_REALM>"
	}
	return fmt.Sprintf("kgetcred \"MSSQLSvc/%s:1433@%s\"\n"+
		"kcc copy_cred_cache FILE:<path>\n"+
		"then point the connection's krb5-credcachefile parameter at that file",
		e.host(), realm)
}

func (e *CrossRealmError) host() string {
	if e.Host == "" {
		return "<host>"
	}
	return e.Host
}
