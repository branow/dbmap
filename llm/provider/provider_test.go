package provider

import (
	"errors"
	"reflect"
	"testing"

	"github.com/branow/dbmap/llm"
)

func TestNewBuildsTheNamedProvider(t *testing.T) {
	cases := []struct {
		name string
		cfg  llm.Config
	}{
		{"anthropic", llm.Config{Provider: "anthropic", APIKey: "k"}},
		{"openai", llm.Config{Provider: "openai", APIKey: "k", Model: "m"}},
		{"claudecode", llm.Config{Provider: "claudecode"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			client, err := New(c.cfg)
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if client.Name() != c.name {
				t.Fatalf("Name = %q, want %q", client.Name(), c.name)
			}
		})
	}
}

func TestNewRejectsAnUnknownProvider(t *testing.T) {
	_, err := New(llm.Config{Provider: "telepathy"})
	if !errors.Is(err, llm.ErrBadRequest) {
		t.Fatalf("err = %v", err)
	}
}

func TestNewPassesTheProviderFailureThrough(t *testing.T) {
	if _, err := New(llm.Config{Provider: "anthropic"}); !errors.Is(err, llm.ErrAuth) {
		t.Fatalf("err = %v", err)
	}
}

func TestNames(t *testing.T) {
	want := []string{"anthropic", "claudecode", "openai"}
	if got := Names(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Names = %v, want %v", got, want)
	}
}
