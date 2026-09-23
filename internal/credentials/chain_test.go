package credentials

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// assembled builds a chain with a scripted keychain, an injected environment
// and, under the plaintext policy, a file in the test's temporary directory.
func assembled(t *testing.T, policy Policy, keychain *Keychain, env map[string]string) *chain {
	t.Helper()
	c := &chain{
		env:      NewEnv(func(name string) string { return env[name] }),
		keychain: keychain,
	}
	if policy == PolicyPlaintext {
		c.file = NewFile(filepath.Join(t.TempDir(), FileName))
	}
	return c
}

// TestNoSilentPlaintextFallback is the rule the whole package exists to keep: a
// keychain that cannot answer is a hard error, and the error names the way out.
// Nothing reaches disk unless the policy said so.
func TestNoSilentPlaintextFallback(t *testing.T) {
	store := assembled(t, PolicyNever, stub(nil, broken), nil)

	err := store.Set(DBKey("primary"), NewSecret(password))
	var failure *KeychainError
	if !errors.As(err, &failure) {
		t.Fatalf("Set error = %v, want KeychainError", err)
	}
	if failure.Remedy == "" {
		t.Error("the refusal does not name a fix")
	}
	if _, err := store.Get(DBKey("primary")); !errors.As(err, &failure) {
		t.Fatalf("Get error = %v, want KeychainError", err)
	}
}

func TestPlaintextIsOptIn(t *testing.T) {
	store := assembled(t, PolicyPlaintext, stub(nil, broken), nil)
	if err := store.Set(DBKey("primary"), NewSecret(password)); err != nil {
		t.Fatalf("Set under the plaintext policy: %v", err)
	}
	secret, err := store.Get(DBKey("primary"))
	if err != nil || secret.Reveal() != password {
		t.Fatalf("secret = %v, err = %v", secret, err)
	}
	if _, err := os.Stat(store.file.Path); err != nil {
		t.Fatalf("the plaintext file was not written: %v", err)
	}
	if err := store.Delete(DBKey("primary")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(DBKey("primary")); err == nil {
		t.Error("the secret survived a delete")
	}
}

// TestEnvironmentAnswersFirst is what makes the default policy safe: a headless
// run supplies its secrets through the environment and never needs a keychain.
func TestEnvironmentAnswersFirst(t *testing.T) {
	store := assembled(t, PolicyNever, stub(map[string]string{DBKey("primary"): "from-keychain"},
		nil), map[string]string{"DBMAP_SECRET_DB_PRIMARY": "from-env"})
	secret, err := store.Get(DBKey("primary"))
	if err != nil || secret.Reveal() != "from-env" {
		t.Fatalf("secret = %v, err = %v", secret, err)
	}
}

func TestKeychainAnswersWhenTheEnvironmentIsSilent(t *testing.T) {
	store := assembled(t, PolicyNever,
		stub(map[string]string{DBKey("primary"): "from-keychain"}, nil), nil)
	secret, err := store.Get(DBKey("primary"))
	if err != nil || secret.Reveal() != "from-keychain" {
		t.Fatalf("secret = %v, err = %v", secret, err)
	}
	var missing *NotFoundError
	if _, err := store.Get(DBKey("absent")); !errors.As(err, &missing) {
		t.Fatalf("error = %v, want NotFoundError", err)
	}
}

// TestDefaultOptionsNeverPrompt guards the zero value: whatever else Options
// grows, a store built without an explicit Interactive must refuse the
// keychain's dialog. It fails the day someone makes prompting the default.
func TestDefaultOptionsNeverPrompt(t *testing.T) {
	tests := []struct {
		name    string
		options Options
		want    ui
	}{
		{name: "zero value", options: Options{}, want: noUI},
		{name: "never", options: Options{Policy: PolicyNever}, want: noUI},
		{name: "plaintext", options: Options{Policy: PolicyPlaintext, File: "x.yml"},
			want: noUI},
		{name: "opted in", options: Options{Interactive: true}, want: allowUI},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, err := New(tt.options)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if got := store.(*chain).keychain.ui; got != tt.want {
				t.Errorf("keychain ui = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		options Options
		wantErr bool
	}{
		{name: "default policy", options: Options{}},
		{name: "never", options: Options{Policy: PolicyNever}},
		{name: "plaintext needs a path", options: Options{Policy: PolicyPlaintext},
			wantErr: true},
		{name: "plaintext", options: Options{Policy: PolicyPlaintext, File: "x.yml"}},
		{name: "unknown policy", options: Options{Policy: "sure-why-not"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, err := New(tt.options)
			if tt.wantErr {
				if err == nil {
					t.Fatal("want an error")
				}
				return
			}
			if err != nil || store == nil {
				t.Fatalf("New: %v", err)
			}
		})
	}
}
