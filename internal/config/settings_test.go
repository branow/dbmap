package config

import (
	"errors"
	"io/fs"
	"os"
	"testing"
)

func TestSettings(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		value   string
		want    string
		wantErr func(error) bool
	}{
		{name: "output", key: "output", value: "json", want: "json"},
		{name: "fallback", key: "secrets.fallback", value: "plaintext", want: "plaintext"},
		{name: "fallback refuses an unknown policy", key: "secrets.fallback", value: "maybe",
			wantErr: isInvalid},
		{name: "current profile must exist", key: "current_profile", value: "absent",
			wantErr: isNotFound},
		{name: "current profile", key: "current_profile", value: "work", want: "work"},
		{name: "unknown key", key: "nope", value: "x", wantErr: isNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := loaded(t)
			err := c.Set(tt.key, tt.value)
			if tt.wantErr != nil {
				if !tt.wantErr(err) {
					t.Fatalf("error = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Set: %v", err)
			}
			got, err := c.Get(tt.key)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got != tt.want {
				t.Errorf("value = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSettingsListsEveryKey(t *testing.T) {
	c := New()
	for _, setting := range Settings() {
		if _, err := c.Get(setting.Key); err != nil {
			t.Errorf("listed key %q is not readable: %v", setting.Key, err)
		}
		if setting.Doc == "" {
			t.Errorf("key %q has no documentation", setting.Key)
		}
	}
}

func isInvalid(err error) bool {
	var invalid *InvalidError
	return errors.As(err, &invalid)
}

func isNotFound(err error) bool {
	var missing *NotFoundError
	return errors.As(err, &missing)
}

func stat(path string) (fs.FileInfo, error) { return os.Stat(path) }

func write(path, content string) error { return os.WriteFile(path, []byte(content), 0o600) }
