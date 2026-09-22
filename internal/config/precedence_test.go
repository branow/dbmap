package config

import (
	"errors"
	"testing"
)

// sample builds a config with one of each entry and an injected environment, so
// the precedence chain is exercised without touching a process or a file.
func sample(env map[string]string) *Config {
	c := New()
	c.Connections["primary"] = Connection{Engine: SQLServer, Host: "example.internal",
		Auth: SQLLogin, Username: "reader"}
	c.Backends["main"] = Backend{Provider: Anthropic, Model: "model-a"}
	c.Profiles["work"] = Profile{Connection: "primary", Backend: "main", Output: "json"}
	c.Profiles["plain"] = Profile{Connection: "primary", Backend: "main"}
	c.SetEnv(func(name string) string { return env[name] })
	return c
}

func TestProfilePrecedence(t *testing.T) {
	tests := []struct {
		name    string
		flag    string
		env     map[string]string
		current string
		want    string
	}{
		{name: "flag wins over everything", flag: "work",
			env: map[string]string{EnvProfile: "plain"}, current: "plain", want: "work"},
		{name: "env wins over file", env: map[string]string{EnvProfile: "work"},
			current: "plain", want: "work"},
		{name: "file wins over default", current: "plain", want: "plain"},
		{name: "default is none", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := sample(tt.env)
			c.CurrentProfile = tt.current
			if got := c.ProfileName(Overrides{Profile: tt.flag}); got != tt.want {
				t.Errorf("profile = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestOutputPrecedence(t *testing.T) {
	tests := []struct {
		name     string
		flag     string
		env      map[string]string
		current  string
		fileWide string
		want     string
	}{
		{name: "flag wins", flag: "table", env: map[string]string{EnvOutput: "json"},
			current: "work", fileWide: "json", want: "table"},
		{name: "env beats the profile", env: map[string]string{EnvOutput: "table"},
			current: "work", want: "table"},
		{name: "profile beats the file default", current: "work", fileWide: "table",
			want: "json"},
		{name: "file default beats the built-in", current: "plain", fileWide: "json",
			want: "json"},
		{name: "built-in default", want: DefaultOutputFormat},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := sample(tt.env)
			c.CurrentProfile = tt.current
			c.DefaultOutput = tt.fileWide
			if got := c.Output(Overrides{Output: tt.flag}); got != tt.want {
				t.Errorf("output = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBooleanPrecedence(t *testing.T) {
	on, off := true, false
	tests := []struct {
		name string
		flag *bool
		env  map[string]string
		want bool
	}{
		{name: "flag off wins over env on", flag: &off,
			env: map[string]string{EnvNoInput: "1"}, want: false},
		{name: "flag on", flag: &on, want: true},
		{name: "env on", env: map[string]string{EnvNoInput: "true"}, want: true},
		{name: "env off", env: map[string]string{EnvNoInput: "false"}, want: false},
		{name: "default off", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := sample(tt.env)
			if got := c.NoInput(Overrides{NoInput: tt.flag}); got != tt.want {
				t.Errorf("no-input = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFallbackPrecedence(t *testing.T) {
	on := true
	tests := []struct {
		name    string
		flag    *bool
		env     map[string]string
		file    Fallback
		want    Fallback
		wantErr bool
	}{
		{name: "default refuses plaintext", want: Never},
		{name: "flag opts in", flag: &on, want: Plaintext},
		{name: "env opts in", env: map[string]string{EnvFallback: "plaintext"},
			want: Plaintext},
		{name: "file opts in", file: Plaintext, want: Plaintext},
		{name: "env beats file", env: map[string]string{EnvFallback: "never"},
			file: Plaintext, want: Never},
		{name: "unknown value is refused",
			env: map[string]string{EnvFallback: "maybe"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := sample(tt.env)
			c.Secrets.Fallback = tt.file
			got, err := c.Fallback(Overrides{Plain: tt.flag})
			if tt.wantErr {
				var invalid *InvalidError
				if !errors.As(err, &invalid) {
					t.Fatalf("error = %v, want InvalidError", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Fallback: %v", err)
			}
			if got != tt.want {
				t.Errorf("fallback = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestActiveReportsNothingConfigured(t *testing.T) {
	c := sample(nil)
	_, _, err := c.Active(Overrides{})
	var missing *NotFoundError
	if !errors.As(err, &missing) || missing.Kind != KindProfile {
		t.Fatalf("error = %v, want a profile NotFoundError", err)
	}
}
