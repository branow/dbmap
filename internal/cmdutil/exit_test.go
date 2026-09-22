package cmdutil

import (
	"errors"
	"fmt"
	"testing"

	"github.com/branow/dbmap/internal/config"
	"github.com/branow/dbmap/internal/credentials"
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
