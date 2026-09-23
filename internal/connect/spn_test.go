package connect

import (
	"strings"
	"testing"

	"github.com/branow/dbmap/internal/config"
)

// The remedy has to name the service principal the engine actually registers.
// A Postgres user told to run kgetcred "MSSQLSvc/..." gets a command that
// cannot succeed, which is worse than no remedy at all.
func TestTheRemedyNamesTheEnginesOwnServicePrincipal(t *testing.T) {
	cases := []struct {
		engine config.Engine
		want   string
		absent string
	}{
		{config.SQLServer, "MSSQLSvc/db.example.internal:1433", "postgres/"},
		{config.Postgres, "postgres/db.example.internal", "MSSQLSvc"},
	}

	for _, c := range cases {
		t.Run(string(c.engine), func(t *testing.T) {
			err := &CrossRealmError{
				Host:  "db.example.internal",
				Realm: "EXAMPLE.LOCAL",
				SPN:   SPN(c.engine, "db.example.internal"),
			}

			remedy := err.Remedy()
			if !strings.Contains(remedy, c.want) {
				t.Errorf("remedy does not name %q:\n%s", c.want, remedy)
			}
			if strings.Contains(remedy, c.absent) {
				t.Errorf("remedy names the other engine's principal (%q):\n%s", c.absent, remedy)
			}
			if !strings.Contains(remedy, "@EXAMPLE.LOCAL") {
				t.Errorf("remedy carries no realm:\n%s", remedy)
			}
		})
	}
}

// Without an engine the SQL Server shape is the safe default, and the realm
// placeholder must still be visible.
func TestARemedyWithoutAnEngineStillReadsSensibly(t *testing.T) {
	remedy := (&CrossRealmError{Host: "db.example.internal"}).Remedy()

	for _, want := range []string{"kgetcred", "MSSQLSvc", "<HOST_REALM>", "kcc copy_cred_cache"} {
		if !strings.Contains(remedy, want) {
			t.Errorf("remedy lost %q:\n%s", want, remedy)
		}
	}
}
