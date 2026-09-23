package connect

import (
	"errors"
	"strings"
	"testing"
)

// A server that rejects a login or names a missing database has decided. Left
// unclassified these read as "unavailable", and setup then stores a credential
// the server has already refused, or a database name that does not exist.
func TestADefinitiveRefusalIsNotAnOutage(t *testing.T) {
	cases := []struct {
		name    string
		err     string
		subject string
	}{
		{"postgres wrong password",
			`failed SASL auth: FATAL: password authentication failed for user "postgres" (SQLSTATE 28P01)`,
			"login"},
		{"sqlserver wrong login",
			"mssql: Login failed for user 'app'.",
			"login"},
		{"sqlserver missing database",
			`mssql: Cannot open database "NoSuchDatabase" that was requested by the login. (4063)`,
			"database"},
		{"postgres missing database",
			`FATAL: database "nosuch" does not exist (SQLSTATE 3D000)`,
			"database"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := diagnose(errors.New(c.err), "db.example.internal")

			var rejected *RejectedError
			if !errors.As(err, &rejected) {
				t.Fatalf("error is %T, want *RejectedError", err)
			}
			if !strings.Contains(rejected.Subject, c.subject) {
				t.Errorf("Subject = %q, want it to name the %s", rejected.Subject, c.subject)
			}
		})
	}
}

// A refused connection is still ambiguous: the server may be restarting. It
// must stay unclassified so setup stores with a warning rather than refusing.
func TestATransientFailureIsLeftUnclassified(t *testing.T) {
	for _, message := range []string{
		"dial error: dial tcp 127.0.0.1:5432: connect: connection refused",
		"i/o timeout",
	} {
		err := errors.New(message)
		if got := diagnose(err, "db.example.internal"); got != err {
			t.Errorf("diagnose classified a transient failure as %T: %v", got, got)
		}
	}
}
