package connect

import (
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jcmturner/gokrb5/v8/client"
	krb5 "github.com/jcmturner/gokrb5/v8/config"
	"github.com/jcmturner/gokrb5/v8/credentials"
	"github.com/jcmturner/gokrb5/v8/spnego"
)

// pgx ships the hook for GSSAPI authentication but no implementation, pointing
// at an unmaintained third-party package. The interface is three methods over
// the Kerberos stack this build already carries for SQL Server, so it is
// implemented here and both engines share one credential cache, one remedy and
// one single-realm limitation.

// DefaultConfig is where the realm configuration is read from when KRB5_CONFIG
// names nothing.
const DefaultConfig = "/etc/krb5.conf"

// The GSS provider is process-global: RegisterGSSProvider sets one function for
// the program and the factory it calls sees no connection, so the cache lives
// here beside it and registration happens at pool construction rather than in
// an init. The consequence: with two Kerberos Postgres connections open against
// different caches, the most recent wins. This tool indexes one per run.
var (
	gssOnce  sync.Once
	gssMu    sync.Mutex
	gssCache string
)

// useGSS points pgx at this package's provider and tells it which credential
// cache to read. Safe to call repeatedly; the registration happens once.
func useGSS(cache string) {
	gssMu.Lock()
	gssCache = cache
	gssMu.Unlock()

	gssOnce.Do(func() {
		pgconn.RegisterGSSProvider(func() (pgconn.GSS, error) { return newGSS() })
	})
}

// newGSS builds a provider bound to whichever cache was most recently resolved,
// read at exchange time so a ticket refreshed since pool construction wins.
func newGSS() (pgconn.GSS, error) {
	gssMu.Lock()
	cache := gssCache
	gssMu.Unlock()

	return &gss{
		open: func(spn string) (exchange, error) { return negotiate(cache, spn, ambient()) },
	}, nil
}

// exchange trades the credential cache's ticket-granting ticket for a service
// ticket and wraps it in the first token. It is an interface because it is the
// only part of this file that cannot be tested without a live KDC.
type exchange interface {
	Init() ([]byte, error)
}

// negotiation is the real exchange, over gokrb5's SPNEGO client.
type negotiation struct {
	client *spnego.SPNEGO
}

// Init acquires the credential and returns the marshalled initiation token.
func (n negotiation) Init() ([]byte, error) {
	if err := n.client.AcquireCred(); err != nil {
		return nil, &KerberosError{Stage: StageCredential, Err: err}
	}
	token, err := n.client.InitSecContext()
	if err != nil {
		return nil, &KerberosError{Stage: StageTicket, Err: err}
	}
	marshalled, err := token.Marshal()
	if err != nil {
		return nil, &KerberosError{Stage: StageTicket, Err: err}
	}
	return marshalled, nil
}

// negotiate opens a SPNEGO exchange for one service principal, reading the
// identity out of the credential cache rather than asking for a password.
func negotiate(cache, spn string, env environment) (exchange, error) {
	config, err := krb5.Load(configPath(env))
	if err != nil {
		return nil, &KerberosError{Stage: StageConfig, Err: err}
	}
	ccache, err := credentials.LoadCCache(cache)
	if err != nil {
		return nil, &CredentialCacheError{Path: cache, Err: err}
	}
	cl, err := client.NewFromCCache(ccache, config)
	if err != nil {
		return nil, &KerberosError{Stage: StageCredential, Err: err}
	}
	return negotiation{client: spnego.SPNEGOClient(cl, spn)}, nil
}

// configPath is where the realm configuration lives.
func configPath(env environment) string {
	if path := strings.TrimSpace(env.getenv("KRB5_CONFIG")); path != "" {
		return path
	}
	return DefaultConfig
}

// gss implements pgconn.GSS, holding one exchange because a connection
// authenticates once.
type gss struct {
	open    func(spn string) (exchange, error)
	started bool
}

// GetInitToken builds the service principal from the host and service pgx was
// configured with. The host is used as given, lowercased, and deliberately not
// reverse-resolved: rdns is off in modern Kerberos deployments, and resolving
// would make the principal depend on whichever DNS answer arrived.
func (g *gss) GetInitToken(host, service string) ([]byte, error) {
	return g.GetInitTokenFromSPN(service + "/" + canonical(host))
}

// GetInitTokenFromSPN starts the exchange for an explicitly named principal.
func (g *gss) GetInitTokenFromSPN(spn string) ([]byte, error) {
	open := g.open
	if open == nil {
		open = func(name string) (exchange, error) { return negotiate(gssCache, name, ambient()) }
	}

	session, err := open(spn)
	if err != nil {
		return nil, err
	}
	token, err := session.Init()
	if err != nil {
		return nil, err
	}
	g.started = true
	return token, nil
}

// Continue reads the server's answer and says whether the exchange is done.
// Kerberos negotiates in one round, so a server asking to continue wants a
// mechanism this build does not offer; saying so beats looping until a
// timeout.
func (g *gss) Continue(in []byte) (done bool, out []byte, err error) {
	if !g.started {
		return false, nil, &KerberosError{
			Stage:  StageReply,
			Reason: "the server answered before a ticket was sent",
		}
	}

	init, token, err := spnego.UnmarshalNegToken(in)
	if err != nil {
		return false, nil, &KerberosError{
			Stage:  StageReply,
			Reason: "the server's answer is not a negotiation token",
			Err:    err,
		}
	}
	if init {
		return false, nil, &KerberosError{
			Stage:  StageReply,
			Reason: "the server answered with an initiation token rather than a reply",
		}
	}

	reply, ok := token.(spnego.NegTokenResp)
	if !ok {
		return false, nil, &KerberosError{
			Stage:  StageReply,
			Reason: "the server's answer is not a negotiation reply",
		}
	}

	switch reply.State() {
	case spnego.NegStateAcceptCompleted:
		return true, nil, nil
	case spnego.NegStateReject:
		return false, nil, &KerberosError{
			Stage:  StageReply,
			Reason: "the server rejected the ticket",
		}
	default:
		return false, nil, &KerberosError{
			Stage: StageReply,
			Reason: "the server asked to continue the negotiation, which needs a " +
				"mechanism this build does not offer",
		}
	}
}

// canonical renders a host for a service principal.
func canonical(host string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
}

var _ pgconn.GSS = (*gss)(nil)
