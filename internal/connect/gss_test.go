package connect

import (
	"errors"
	"strings"
	"testing"

	"github.com/jcmturner/gofork/encoding/asn1"
	"github.com/jcmturner/gokrb5/v8/gssapi"
	"github.com/jcmturner/gokrb5/v8/spnego"

	"github.com/branow/dbmap/internal/config"
)

// The exchange that needs a KDC is the one thing here that cannot be tested
// without one, so it is the one thing behind an interface. Everything else —
// the principal this build asks for, and what it makes of each answer the
// server can give — is driven against scripted tokens.
type scripted struct {
	spn   string
	token []byte
	err   error
}

func (s *scripted) open(spn string) (exchange, error) {
	s.spn = spn
	if s.err != nil {
		return nil, s.err
	}
	return s, nil
}

func (s *scripted) Init() ([]byte, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.token, nil
}

// reply marshals the answer a server gives at the end of the negotiation, so
// the state machine is fed real bytes rather than a stub.
func reply(t *testing.T, state spnego.NegState) []byte {
	t.Helper()
	token := spnego.NegTokenResp{
		NegState:      asn1.Enumerated(state),
		SupportedMech: gssapi.OIDKRB5.OID(),
	}
	marshalled, err := token.Marshal()
	if err != nil {
		t.Fatalf("marshalling a negotiation reply: %v", err)
	}
	return marshalled
}

// started returns a provider that has already sent its ticket, which is the
// only state in which a server answer means anything.
func started(t *testing.T) (*gss, *scripted) {
	t.Helper()
	script := &scripted{token: []byte("init-token")}
	provider := &gss{open: script.open}
	if _, err := provider.GetInitTokenFromSPN("postgres/db.example.internal"); err != nil {
		t.Fatalf("GetInitTokenFromSPN: %v", err)
	}
	return provider, script
}

// The service principal is built from the host as given, lowercased. It is
// deliberately not resolved through DNS: reverse resolution is the rdns
// behaviour modern deployments turn off, and it would make the principal depend
// on whichever answer arrived.
func TestTheServicePrincipalIsBuiltFromTheHostAsGiven(t *testing.T) {
	cases := []struct {
		host    string
		service string
		want    string
	}{
		{"db.example.internal", "postgres", "postgres/db.example.internal"},
		{"DB.Example.Internal", "postgres", "postgres/db.example.internal"},
		{"db.example.internal.", "postgres", "postgres/db.example.internal"},
		{"db.example.internal", "POSTGRES", "POSTGRES/db.example.internal"},
	}

	for _, c := range cases {
		t.Run(c.host, func(t *testing.T) {
			script := &scripted{token: []byte("init-token")}
			provider := &gss{open: script.open}

			token, err := provider.GetInitToken(c.host, c.service)
			if err != nil {
				t.Fatalf("GetInitToken: %v", err)
			}
			if script.spn != c.want {
				t.Errorf("asked for %q, want %q", script.spn, c.want)
			}
			if string(token) != "init-token" {
				t.Errorf("token = %q", token)
			}
		})
	}
}

// A deployment whose principal does not follow service/host sets krbspn, and
// that name must reach the KDC untouched.
func TestAnExplicitPrincipalIsPassedThroughUnchanged(t *testing.T) {
	script := &scripted{token: []byte("init-token")}
	provider := &gss{open: script.open}

	if _, err := provider.GetInitTokenFromSPN("POSTGRES/alias@EXAMPLE.LOCAL"); err != nil {
		t.Fatalf("GetInitTokenFromSPN: %v", err)
	}
	if script.spn != "POSTGRES/alias@EXAMPLE.LOCAL" {
		t.Errorf("asked for %q, want the principal unchanged", script.spn)
	}
}

// A ticket that cannot be obtained must surface as itself rather than as a
// nil token the caller sends anyway.
func TestAFailedTicketRequestStopsTheExchange(t *testing.T) {
	script := &scripted{err: &KerberosError{Stage: StageTicket, Reason: "no service ticket"}}
	provider := &gss{open: script.open}

	token, err := provider.GetInitToken("db.example.internal", "postgres")

	if token != nil {
		t.Error("a token was produced despite the failure")
	}
	var kerberos *KerberosError
	if !errors.As(err, &kerberos) {
		t.Fatalf("error is %T, want *KerberosError", err)
	}
	if provider.started {
		t.Error("the exchange counted itself as started after failing to start")
	}
}

// The whole state machine, one row per answer a server can give.
func TestContinueReadsEveryAnswerTheServerCanGive(t *testing.T) {
	cases := []struct {
		name  string
		state spnego.NegState
		done  bool
		fails bool
		says  string
	}{
		{
			name:  "the ticket was accepted",
			state: spnego.NegStateAcceptCompleted,
			done:  true,
		},
		{
			name:  "the ticket was rejected",
			state: spnego.NegStateReject,
			fails: true,
			says:  "rejected",
		},
		{
			name:  "the server wants to keep negotiating",
			state: spnego.NegStateAcceptIncomplete,
			fails: true,
			says:  "continue the negotiation",
		},
		{
			name:  "the server wants a message integrity check",
			state: spnego.NegStateRequestMIC,
			fails: true,
			says:  "continue the negotiation",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			provider, _ := started(t)

			done, out, err := provider.Continue(reply(t, c.state))

			if c.fails {
				var kerberos *KerberosError
				if !errors.As(err, &kerberos) {
					t.Fatalf("error is %T, want *KerberosError", err)
				}
				if kerberos.Stage != StageReply {
					t.Errorf("stage = %q, want %q", kerberos.Stage, StageReply)
				}
				if !strings.Contains(err.Error(), c.says) {
					t.Errorf("error %q does not say %q", err, c.says)
				}
				if done {
					t.Error("a failed exchange reported itself done")
				}
				return
			}

			if err != nil {
				t.Fatalf("Continue: %v", err)
			}
			if !done {
				t.Error("an accepted ticket did not end the exchange")
			}
			if out != nil {
				t.Errorf("a finished exchange produced a further token: %q", out)
			}
		})
	}
}

// An answer arriving before a ticket was sent is a protocol error, not a state
// to muddle through.
func TestContinueRefusesAnAnswerToAQuestionNeverAsked(t *testing.T) {
	provider := &gss{}

	done, _, err := provider.Continue(reply(t, spnego.NegStateAcceptCompleted))

	if done {
		t.Error("an exchange that never started reported itself done")
	}
	var kerberos *KerberosError
	if !errors.As(err, &kerberos) {
		t.Fatalf("error is %T, want *KerberosError", err)
	}
	if !strings.Contains(err.Error(), "before a ticket was sent") {
		t.Errorf("error = %v", err)
	}
}

// Bytes that are not a negotiation token at all must be refused rather than
// read as a zero state, which is the same number as "accepted".
func TestContinueRefusesBytesThatAreNotATokenAtAll(t *testing.T) {
	provider, _ := started(t)

	cases := map[string][]byte{
		"nothing":     nil,
		"noise":       []byte("this is not asn.1"),
		"empty token": {},
	}

	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			done, _, err := provider.Continue(in)
			if err == nil {
				t.Fatal("unreadable bytes were accepted as an answer")
			}
			if done {
				t.Error("unreadable bytes ended the exchange successfully")
			}
		})
	}
}

// A server that sends an initiation token is not answering, it is starting its
// own exchange, and this build does not accept one.
func TestContinueRefusesAnInitiationToken(t *testing.T) {
	provider, _ := started(t)
	init := spnego.NegTokenInit{MechTypes: []asn1.ObjectIdentifier{gssapi.OIDKRB5.OID()}}
	marshalled, err := init.Marshal()
	if err != nil {
		t.Fatalf("marshalling an initiation token: %v", err)
	}

	done, _, err := provider.Continue(marshalled)

	if err == nil {
		t.Fatal("an initiation token was accepted as an answer")
	}
	if done {
		t.Error("an initiation token ended the exchange successfully")
	}
	if !strings.Contains(err.Error(), "initiation token") {
		t.Errorf("error = %v", err)
	}
}

// The provider is registered at pool construction, not at import: a package
// that seizes a process-global on import is rude to whoever imports it next.
func TestKerberosPreparationRegistersTheProvider(t *testing.T) {
	cfg := config.Connection{
		Engine: config.Postgres, Host: "db.example.internal",
		Database: "appcore", Auth: config.Kerberos,
	}
	env := world(
		map[string]string{"KRB5CCNAME": "FILE:/tmp/krb5cc_501"},
		map[string]bool{"/tmp/krb5cc_501": false},
	)

	if err := preparePostgres(cfg, env); err != nil {
		t.Fatalf("preparePostgres: %v", err)
	}

	provider, err := newGSS()
	if err != nil {
		t.Fatalf("newGSS: %v", err)
	}
	if provider == nil {
		t.Fatal("no provider was built")
	}
	gssMu.Lock()
	cache := gssCache
	gssMu.Unlock()
	if cache != "/tmp/krb5cc_501" {
		t.Errorf("the provider reads %q, want the resolved cache", cache)
	}
}

// A scram connection needs no Kerberos preparation and must not be made to
// produce a ticket error.
func TestScramNeedsNoKerberosPreparation(t *testing.T) {
	cfg := config.Connection{
		Engine: config.Postgres, Host: "db.example.internal",
		Database: "appcore", Auth: config.SCRAM, Username: "reader",
	}

	if err := preparePostgres(cfg, world(nil, nil)); err != nil {
		t.Fatalf("a scram connection was asked for a ticket: %v", err)
	}
}

// The ccache constraint is the same on both engines, which is the whole point
// of implementing the provider rather than depending on one: one remedy to
// print, not two.
func TestPostgresKerberosFailsTheSameWayAsSqlserver(t *testing.T) {
	cfg := config.Connection{
		Engine: config.Postgres, Host: "db.example.internal",
		Database: "appcore", Auth: config.Kerberos,
	}

	err := preparePostgres(cfg, world(map[string]string{"KRB5CCNAME": "API:user@EXAMPLE.LOCAL"}, nil))

	var cache *CredentialCacheError
	if !errors.As(err, &cache) {
		t.Fatalf("error is %T, want *CredentialCacheError", err)
	}
	if !strings.Contains(err.Error(), "kinit -c FILE:") {
		t.Errorf("the remedy does not name kinit -c FILE:: %v", err)
	}
}

func TestConfigPathPrefersTheEnvironment(t *testing.T) {
	if got := configPath(world(nil, nil)); got != DefaultConfig {
		t.Errorf("configPath = %q, want %q", got, DefaultConfig)
	}
	env := world(map[string]string{"KRB5_CONFIG": "/etc/custom/krb5.conf"}, nil)
	if got := configPath(env); got != "/etc/custom/krb5.conf" {
		t.Errorf("configPath = %q", got)
	}
}

// A provider that reached the KDC-dependent half with no credential cache must
// name the cache rather than the KDC.
func TestNegotiateNamesTheCacheItCouldNotRead(t *testing.T) {
	_, err := negotiate("/nowhere/krb5cc", "postgres/db.example.internal", world(nil, nil))

	if err == nil {
		t.Fatal("a negotiation opened against a cache that is not there")
	}
	// The realm configuration is read first, so on a machine with no
	// /etc/krb5.conf this is a config failure and on one with a config it is a
	// cache failure. Either is a named KerberosError or CredentialCacheError,
	// and neither is a bare driver message.
	var cache *CredentialCacheError
	var kerberos *KerberosError
	if !errors.As(err, &cache) && !errors.As(err, &kerberos) {
		t.Fatalf("error is %T, want a named Kerberos failure", err)
	}
}
