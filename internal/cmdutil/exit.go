package cmdutil

import (
	"errors"

	"github.com/branow/dbmap/internal/config"
	"github.com/branow/dbmap/internal/connect"
	"github.com/branow/dbmap/internal/credentials"
	"github.com/branow/dbmap/internal/engine"
	"github.com/branow/dbmap/internal/iostreams"
	"github.com/branow/dbmap/internal/output"
)

// The exit table. It is the tool's contract with a script that calls it.
const (
	ExitOK          = 0
	ExitError       = 1
	ExitCancelled   = 2
	ExitValidation  = 3
	ExitAuth        = 4
	ExitNotFound    = 5
	ExitUnavailable = 6
)

// Code documents one exit status. The table is data so that the documentation,
// the mapping below and the test that every code stays reachable all read the
// same list.
type Code struct {
	Value   int
	Name    string
	Meaning string
}

var table = []Code{
	{ExitOK, "ok", "the command did what it was asked"},
	{ExitError, "generic", "anything unclassified, including a defect in this tool"},
	{ExitCancelled, "cancelled", "the user aborted, or input ended at a prompt"},
	{ExitValidation, "validation", "the input was refused: bad value, missing value, " +
		"a target this tool will not open"},
	{ExitAuth, "auth", "a credential was presented and rejected, or could not be read"},
	{ExitNotFound, "not found", "a named entry, setting or stored secret does not exist"},
	{ExitUnavailable, "unavailable", "a dependency could not be reached: keychain, " +
		"database, model"},
}

// Codes lists the documented exit statuses.
func Codes() []Code {
	out := make([]Code, len(table))
	copy(out, table)
	return out
}

// rules map an error to an exit code. They are a table walked in order, and
// every match is errors.Is or errors.As: a message is never inspected, so
// rewording an error can never change a script's behaviour.
//
// Order is meaning, not convenience. The engine and connect packages wrap: a
// ConnectError carries the cause that produced it, and the cause is the more
// actionable of the two. So the specific causes are matched first and the
// wrapper last, which is why a Kerberos failure inside a ConnectError reports
// auth rather than unavailable.
var rules = []struct {
	code  int
	match func(error) bool
}{
	{ExitCancelled, is(ErrCancelled)},
	{ExitCancelled, is(iostreams.ErrCancelled)},

	// A refused statement is this tool refusing its own generated SQL, which
	// is a defect here and not a user error. It is matched before every
	// wrapper so that nothing can disguise it as an environment problem.
	{ExitError, as[*engine.RefusedError]},

	{ExitValidation, is(iostreams.ErrNoInput)},
	{ExitValidation, as[*ValidationError]},
	{ExitValidation, as[*config.InvalidError]},
	{ExitValidation, as[*config.InUseError]},
	{ExitValidation, as[*output.UnknownFormatError]},
	{ExitValidation, as[*connect.ProductionError]},
	{ExitValidation, as[*connect.UnsupportedError]},

	{ExitAuth, as[*AuthError]},
	{ExitAuth, as[*connect.CrossRealmError]},
	{ExitAuth, as[*connect.CredentialCacheError]},
	{ExitAuth, as[*connect.KerberosError]},

	{ExitNotFound, as[*NotConfiguredError]},
	{ExitNotFound, as[*config.NotFoundError]},
	{ExitNotFound, as[*credentials.NotFoundError]},

	{ExitUnavailable, as[*UnavailableError]},
	{ExitUnavailable, as[*credentials.KeychainError]},
	{ExitUnavailable, as[*credentials.ReadOnlyError]},
	{ExitUnavailable, as[*connect.ConnectError]},
	{ExitUnavailable, as[*engine.UnhealthyError]},
}

// ExitCode translates an error into the process's exit status. It is the single
// translation point, called only from main.
func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	for _, rule := range rules {
		if rule.match(err) {
			return rule.code
		}
	}
	return ExitError
}

func is(target error) func(error) bool {
	return func(err error) bool { return errors.Is(err, target) }
}

func as[T error](err error) bool {
	var target T
	return errors.As(err, &target)
}
