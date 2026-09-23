package cmd

import (
	"strings"
	"testing"
)

// Nothing but a secret is prompted for. A prompt duplicated every flag's
// validation in a second path that ran only on a terminal, which is how the
// claudecode provider — which has no endpoint — came to be asked for a base url
// and given "sdaf".
func TestANonSecretValueIsNeverPrompted(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"connection host", []string{"connection", "add", "c"}, "--host"},
		{"connection username", []string{"connection", "add", "c",
			"--engine", "postgres", "--auth", "scram", "--host", "h"}, "--username"},
		{"profile connection", []string{"profile", "create", "p"}, "--connection"},
		{"profile backend", []string{"profile", "create", "p", "--connection", "c"}, "--backend"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// A terminal is available and answers are queued; the command must
			// still not ask for any of them.
			h := newHarness(t)
			h.interactive("someanswer\nsomeanswer\nsomeanswer\n")

			err := h.run(c.args...)
			if err == nil {
				t.Fatalf("a missing %s was accepted", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not name %s", err, c.want)
			}
			if h.out.Len() != 0 {
				t.Errorf("something was written where nothing should be asked:\n%s", h.out)
			}
		})
	}
}

// A provider with no endpoint must reject --base-url rather than store junk.
func TestABaseURLIsRejectedWhereThereIsNoEndpoint(t *testing.T) {
	h := newHarness(t)

	err := h.run("backend", "add", "b", "--provider", "claudecode",
		"--model", "m", "--base-url", "sdaf")

	if err == nil || !strings.Contains(err.Error(), "--base-url") {
		t.Fatalf("err = %v, want it to reject --base-url for claudecode", err)
	}
}

// And where there is an endpoint, it must still be a url.
func TestABaseURLMustBeAURL(t *testing.T) {
	h := newHarness(t)

	err := h.run("backend", "add", "b", "--provider", "openai",
		"--model", "m", "--base-url", "sdaf")

	if err == nil || !strings.Contains(err.Error(), "url") {
		t.Fatalf("err = %v, want it to reject a non-url", err)
	}
}
