package connect

import (
	"fmt"
	"strings"

	"github.com/branow/dbmap/internal/config"
)

// CredentialCacheError reports a Kerberos credential cache the pure-Go driver
// cannot use. gokrb5 reads FILE: caches only, while the macOS default is API:.
// The remedy is spelled out because the driver reports only that no credentials
// were found.
type CredentialCacheError struct {
	Path string
	// Type is the offending cache type ("API", "KEYRING", "KCM"), empty when the
	// cache is simply missing.
	Type string
	// Unconvertible marks a cache type this platform ships no tool to convert,
	// which is the only case left that the user has to resolve by hand.
	Unconvertible bool
	Err           error
}

func (e *CredentialCacheError) Error() string {
	if e.Unconvertible {
		return fmt.Sprintf("the Kerberos credential cache is a %s: cache, which the "+
			"driver cannot read and this platform ships no tool to convert; run %s",
			e.Type, e.Remedy())
	}
	if e.Type != "" {
		return fmt.Sprintf("the Kerberos credential cache (%s:) could not be converted "+
			"into one the driver can read; run %s", e.Type, e.Remedy())
	}
	return fmt.Sprintf("no readable Kerberos ticket: %q cannot be read; run %s",
		e.Path, e.Remedy())
}

// Remedy is the command that fixes this. Converting a cache type is dbmap's
// job, so the only thing left to ask for is a ticket.
func (e *CredentialCacheError) Remedy() string {
	if e.Unconvertible {
		return fmt.Sprintf("kinit -c FILE:%s, then set the connection's %s parameter to it",
			e.Path, CredCacheParam)
	}
	return "kinit"
}

func (e *CredentialCacheError) Unwrap() error { return e.Err }

// CertificateError reports a server whose TLS certificate this machine will not
// verify. It is named rather than passed through because the driver's own words
// ("x509: certificate signed by unknown authority") describe the mechanism and
// not the decision: dbmap verifies by default, and an internal certificate
// authority the machine does not trust is the ordinary reason it fails. Without
// this the first error a new user meets names no way out.
type CertificateError struct {
	Host string
	Err  error
}

func (e *CertificateError) Error() string {
	return fmt.Sprintf("the server at %s presented a certificate this machine will not "+
		"verify; %s", e.Host, e.Remedy())
}

// Remedy is the choice the user actually has: prove the server, or say they
// accept not proving it. Turning encryption off is deliberately not offered.
func (e *CertificateError) Remedy() string {
	return "either install the issuing authority's certificate in this machine's trust " +
		"store, or redefine the connection with --trust-server-certificate to encrypt " +
		"without verifying who answered"
}

func (e *CertificateError) Unwrap() error { return e.Err }

// UnsupportedError reports a configuration this build cannot serve, named
// precisely rather than surfaced as the driver's generic authentication failure.
type UnsupportedError struct {
	Configuration string
	Reason        string
	Remedy        string
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

// Stage names where in the Kerberos exchange something failed: a missing realm
// file, an expired ticket and a server that said no have three different fixes.
type Stage string

// The stages of the exchange, in the order they happen.
const (
	StageConfig     Stage = "reading the Kerberos configuration"
	StageCredential Stage = "reading the credential cache"
	StageTicket     Stage = "requesting a service ticket"
	StageReply      Stage = "reading the server's answer"
)

// KerberosError reports a failure inside the GSSAPI exchange this package
// performs itself, which is the Postgres path.
type KerberosError struct {
	Stage  Stage
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
// carries the connection name and engine, never the DSN, which holds the
// password.
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
// than the ticket's. Not a refusal: gokrb5 cannot fetch a cross-realm service
// ticket but uses one already in the cache, so the remedy is the priming
// sequence. The realm must be spelled on the SPN or the local one is assumed.
// Diagnosed rather than passed through because the driver's own message reads
// like a wrong hostname and sends the reader looking at DNS.
type CrossRealmError struct {
	Host  string
	Realm string
	// SPN differs per engine and a remedy naming the wrong one does not work.
	SPN string
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
	return fmt.Sprintf("kgetcred %q\n"+
		"kcc copy_cred_cache FILE:<path>\n"+
		"then point the connection's krb5-credcachefile parameter at that file",
		e.spn()+"@"+realm)
}

func (e *CrossRealmError) host() string {
	if e.Host == "" {
		return "<host>"
	}
	return e.Host
}

func (e *CrossRealmError) spn() string {
	if e.SPN != "" {
		return e.SPN
	}
	return "MSSQLSvc/" + e.host() + ":1433"
}

// RejectedError reports a server refusing something definitively: a login it
// will not accept, a database that is not there. It exists so setup can tell a
// verdict from an outage — an unclassified failure reads as "try again later",
// which is the wrong advice for a wrong password.
type RejectedError struct {
	// Subject is what the server refused, in the user's terms.
	Subject string
}

func (e *RejectedError) Error() string {
	return "the server refused " + e.Subject
}
