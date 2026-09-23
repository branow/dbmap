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

// pgx hooks GSSAPI authentication but ships no implementation, so it is three
// methods over the Kerberos stack this build already carries for SQL Server.
// Both engines then share one credential cache, remedy and realm limitation.

// DefaultConfig is where the realm configuration is read from when KRB5_CONFIG
// names nothing.
const DefaultConfig = "/etc/krb5.conf"

// The GSS provider is process-global and its factory sees no connection, so the
// cache lives here beside it. With two Kerberos Postgres connections open
// against different caches the most recent wins; this tool indexes one per run.
var (
	gssOnce  sync.Once
	gssMu    sync.Mutex
	gssCache string
)

// useGSS points pgx at this package's provider and names the credential cache.
// Safe to call repeatedly.
func useGSS(cache string) {
	gssMu.Lock()
	gssCache = cache
	gssMu.Unlock()

	gssOnce.Do(func() {
		pgconn.RegisterGSSProvider(func() (pgconn.GSS, error) { return newGSS() })
	})
}

// newGSS binds to the most recently resolved cache, read at exchange time so a
// ticket refreshed since pool construction wins.
func newGSS() (pgconn.GSS, error) {
	gssMu.Lock()
	cache := gssCache
	gssMu.Unlock()

	return &gss{
		open: func(spn string) (exchange, error) { return negotiate(cache, spn, ambient()) },
	}, nil
}

// exchange trades the cache's ticket-granting ticket for a service ticket. An
// interface because it is the only part here needing a live KDC.
type exchange interface {
	Init() ([]byte, error)
}

type negotiation struct {
	client *spnego.SPNEGO
}

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

func configPath(env environment) string {
	if path := strings.TrimSpace(env.getenv("KRB5_CONFIG")); path != "" {
		return path
	}
	return DefaultConfig
}

// gss implements pgconn.GSS, holding one exchange per connection.
type gss struct {
	open    func(spn string) (exchange, error)
	started bool
}

// GetInitToken builds the service principal from the host pgx was configured
// with, deliberately not reverse-resolved: rdns is off in modern Kerberos
// deployments, and resolving makes the principal depend on the DNS answer.
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
// mechanism this build does not offer.
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

func canonical(host string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
}

var _ pgconn.GSS = (*gss)(nil)
