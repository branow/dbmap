//go:build darwin && cgo

package credentials

import (
	"errors"
	"strings"
	"testing"
)

// TestStatusError checks the translation table alone. It builds no query and
// opens no keychain, so it runs in the normal suite: the only thing under test
// is which OSStatus means what to the store above.
func TestStatusError(t *testing.T) {
	tests := []struct {
		name   string
		status int32
		want   error
	}{
		{name: "missing item", status: statusNotFound, want: errMissing},
		// The refusal a silenced call comes back with: the login keychain
		// answers errSecAuthFailed and the data protection keychain
		// errSecInteractionNotAllowed, and both mean nobody authorized the read.
		{name: "interaction not allowed", status: statusNoInteraction, want: errBlocked},
		{name: "authorization refused", status: statusAuthFailed, want: errBlocked},
		{name: "dialog dismissed", status: statusCanceled, want: errBlocked},
		{name: "no keychain", status: statusNotAvailable},
		{name: "unknown code", status: -999999},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := statusError(tt.status)
			if err == nil {
				t.Fatal("a non-success status produced no error")
			}
			if tt.want != nil {
				if !errors.Is(err, tt.want) {
					t.Fatalf("error = %v, want %v", err, tt.want)
				}
				return
			}
			if errors.Is(err, errMissing) || errors.Is(err, errBlocked) {
				t.Fatalf("error = %v, want no sentinel", err)
			}
		})
	}
}

// TestBlockedStatusReachesTheRemedy is the whole path in one step: the status a
// silenced call returns has to come out of Get as the typed error whose remedy
// names the way forward, not as a missing secret and not as a hang.
func TestBlockedStatusReachesTheRemedy(t *testing.T) {
	key := DBKey("primary")
	keychain := NewKeychain("test")
	keychain.get = func(_, _ string, _ ui) (string, error) {
		return "", statusError(statusNoInteraction)
	}

	_, err := keychain.Get(key)
	var failure *KeychainError
	if !errors.As(err, &failure) {
		t.Fatalf("error = %v, want KeychainError", err)
	}
	for _, want := range []string{"re-authorize", "re-add", EnvName(key)} {
		if !strings.Contains(failure.Remedy, want) {
			t.Errorf("remedy %q does not mention %q", failure.Remedy, want)
		}
	}
}
