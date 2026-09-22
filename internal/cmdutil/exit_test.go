package cmdutil

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/branow/dbmap/internal/config"
	"github.com/branow/dbmap/internal/connect"
	"github.com/branow/dbmap/internal/credentials"
	"github.com/branow/dbmap/internal/engine"
	"github.com/branow/dbmap/internal/iostreams"
	"github.com/branow/dbmap/internal/output"
)

// TestExitCode covers every class in the documented table, and covers each one
// through a wrapped error too: the mapping is errors.Is and errors.As, so a
// wrapped cause must still decide the status.
func TestExitCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "success", err: nil, want: ExitOK},
		{name: "unclassified", err: errors.New("something broke"), want: ExitError},
		{name: "cancelled", err: ErrCancelled, want: ExitCancelled},
		{name: "input ended at a prompt", err: iostreams.ErrCancelled, want: ExitCancelled},
		{name: "prompting disabled", err: iostreams.ErrNoInput, want: ExitValidation},
		{name: "validation", err: &ValidationError{Field: "--host", Reason: "required"},
			want: ExitValidation},
		{name: "invalid config value",
			err:  &config.InvalidError{Field: "engine", Value: "oracle"},
			want: ExitValidation},
		{name: "entry still in use",
			err:  &config.InUseError{Kind: config.KindConnection, Name: "primary"},
			want: ExitValidation},
		{name: "unknown output format",
			err:  &output.UnknownFormatError{Value: "tsv"},
			want: ExitValidation},
		{name: "auth", err: &AuthError{Subject: "primary"}, want: ExitAuth},
		{name: "not configured", err: &NotConfiguredError{What: "current profile"},
			want: ExitNotFound},
		{name: "unknown entry",
			err:  &config.NotFoundError{Kind: config.KindProfile, Name: "work"},
			want: ExitNotFound},
		{name: "no stored secret", err: &credentials.NotFoundError{Key: "db:primary"},
			want: ExitNotFound},
		{name: "production target refused",
			err:  &connect.ProductionError{Name: "primary"},
			want: ExitValidation},
		{name: "unsupported configuration",
			err: &connect.UnsupportedError{Configuration: "cross-realm kerberos",
				Reason: "the pure-Go driver cannot follow a referral"},
			want: ExitValidation},
		{name: "unusable credential cache",
			err:  &connect.CredentialCacheError{Path: "/tmp/krb5cc", Type: "API"},
			want: ExitAuth},
		{name: "kerberos exchange",
			err:  &connect.KerberosError{Stage: connect.StageTicket},
			want: ExitAuth},
		{name: "connection could not be opened",
			err:  &connect.ConnectError{Name: "primary", Engine: config.Postgres},
			want: ExitUnavailable},
		{name: "server has no room",
			err:  &engine.UnhealthyError{Stage: "sampling", Reason: "no memory grant"},
			want: ExitUnavailable},
		{name: "our own sql was refused",
			err:  &engine.RefusedError{Refusal: "is not a read", Statement: "SELECT 1"},
			want: ExitError},
		{name: "unavailable", err: &UnavailableError{Subject: "database"},
			want: ExitUnavailable},
		{name: "keychain", err: &credentials.KeychainError{Op: "read", Key: "db:primary"},
			want: ExitUnavailable},
		{name: "read-only store", err: &credentials.ReadOnlyError{Store: "environment"},
			want: ExitUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExitCode(tt.err); got != tt.want {
				t.Errorf("ExitCode(%v) = %d, want %d", tt.err, got, tt.want)
			}
			if tt.err == nil {
				return
			}
			wrapped := fmt.Errorf("while adding a connection: %w", tt.err)
			if got := ExitCode(wrapped); got != tt.want {
				t.Errorf("ExitCode(wrapped %v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}

func TestErrorMessages(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "validation with a reason",
			err:  &ValidationError{Field: "--host", Reason: "required"},
			want: `invalid --host: required`},
		{name: "validation with a value",
			err:  &ValidationError{Field: "shell", Value: "csh", Allowed: []string{"bash", "zsh"}},
			want: `invalid shell "csh", want one of bash, zsh`},
		{name: "not configured names the fix",
			err:  &NotConfiguredError{What: "current profile", Fix: "run dbmap profile switch"},
			want: "current profile is not configured: run dbmap profile switch"},
		{name: "auth", err: &AuthError{Subject: "backend main"},
			want: "authentication failed for backend main"},
		{name: "unavailable", err: &UnavailableError{Subject: "keychain"},
			want: "keychain is unavailable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("message = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestTheCauseDecidesNotTheWrapper pins the ordering of the rule table. connect
// wraps: a ping failure comes back as a ConnectError carrying what actually
// went wrong, and the cause is the more actionable of the two.
func TestTheCauseDecidesNotTheWrapper(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "a credential cache inside a connect failure",
			err: &connect.ConnectError{Name: "primary", Engine: config.Postgres,
				Err: &connect.CredentialCacheError{Path: "/tmp/krb5cc", Type: "API"}},
			want: ExitAuth},
		{name: "an unsupported setup inside a connect failure",
			err: &connect.ConnectError{Name: "primary", Engine: config.SQLServer,
				Err: &connect.UnsupportedError{Configuration: "cross-realm kerberos"}},
			want: ExitValidation},
		{name: "a kerberos stage inside a connect failure",
			err: &connect.ConnectError{Name: "primary", Engine: config.Postgres,
				Err: &connect.KerberosError{Stage: connect.StageReply}},
			want: ExitAuth},
		{name: "a refused statement is never disguised",
			err: &UnavailableError{Subject: "database",
				Err: &engine.RefusedError{Refusal: "is not a read"}},
			want: ExitError},
		{name: "a connect failure with an unclassified cause stays unavailable",
			err: &connect.ConnectError{Name: "primary", Engine: config.Postgres,
				Err: errors.New("dial tcp: connection refused")},
			want: ExitUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExitCode(tt.err); got != tt.want {
				t.Errorf("ExitCode = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestTheRemedySurvives: these two are the failures a macOS user hits first,
// and each carries the one fact that makes it fixable - the kinit command, and
// which stage of the exchange failed. main prints err.Error() as it stands, so
// asserting on it here is asserting on what the user reads.
func TestTheRemedySurvives(t *testing.T) {
	cache := &connect.CredentialCacheError{Path: "/tmp/krb5cc_501", Type: "API"}
	if !strings.Contains(cache.Error(), "kinit -c FILE:/tmp/krb5cc_501") {
		t.Errorf("the credential cache error lost its remedy: %s", cache.Error())
	}
	for _, stage := range []connect.Stage{connect.StageConfig, connect.StageCredential,
		connect.StageTicket, connect.StageReply} {
		err := &connect.KerberosError{Stage: stage}
		if !strings.Contains(err.Error(), string(stage)) {
			t.Errorf("stage %q was flattened out of %q", stage, err.Error())
		}
	}
}

// TestEveryDocumentedCodeIsReachable is the guard against a code that is
// documented and never produced, and against an error type added with no home:
// a class that maps nowhere lands in the generic bucket, and the table below is
// where a new type has to be listed for that not to happen silently.
func TestEveryDocumentedCodeIsReachable(t *testing.T) {
	reached := map[int]bool{
		ExitOK:    ExitCode(nil) == ExitOK,
		ExitError: ExitCode(errors.New("unclassified")) == ExitError,
	}
	for _, rule := range rules {
		reached[rule.code] = true
	}
	for _, code := range Codes() {
		if !reached[code.Value] {
			t.Errorf("exit code %d (%s) is documented but nothing produces it",
				code.Value, code.Name)
		}
		if code.Meaning == "" {
			t.Errorf("exit code %d (%s) is undocumented", code.Value, code.Name)
		}
	}
}

func TestEveryRuleUsesADocumentedCode(t *testing.T) {
	documented := map[int]bool{}
	for _, code := range Codes() {
		documented[code.Value] = true
	}
	for i, rule := range rules {
		if !documented[rule.code] {
			t.Errorf("rule %d maps to undocumented exit code %d", i, rule.code)
		}
	}
}
