package cmdutil

import (
	"context"
	"errors"

	"github.com/branow/dbmap/internal/config"
	"github.com/branow/dbmap/internal/connect"
	"github.com/branow/dbmap/internal/credentials"
	"github.com/branow/dbmap/internal/engine"
	"github.com/branow/dbmap/internal/iostreams"
	"github.com/branow/dbmap/internal/output"
)

// The exit table: this tool's contract with a script that calls it.
const (
	ExitOK          = 0
	ExitError       = 1
	ExitCancelled   = 2
	ExitValidation  = 3
	ExitAuth        = 4
	ExitNotFound    = 5
	ExitUnavailable = 6
)

// Code documents one exit status, so the docs, the mapping below and the
// reachability test read one list.
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

func Codes() []Code {
	out := make([]Code, len(table))
	copy(out, table)
	return out
}

// rules map an error to an exit code, matched by errors.Is/As and never by
// message. The order is load-bearing: a wrapped cause is matched before its
// wrapper, so a Kerberos failure inside a ConnectError reports auth, not
// unavailable.
var rules = []struct {
	code  int
	match func(error) bool
}{
	{ExitCancelled, is(ErrCancelled)},
	{ExitCancelled, is(iostreams.ErrCancelled)},
	{ExitCancelled, is(context.Canceled)},

	{ExitValidation, is(iostreams.ErrNoInput)},
	{ExitValidation, as[*ValidationError]},
	{ExitValidation, as[*config.InvalidError]},
	{ExitValidation, as[*config.InUseError]},
	{ExitValidation, as[*output.UnknownFormatError]},
	{ExitValidation, as[*connect.UnsupportedError]},
	// A server this tool will not open on the settings it was given, which is
	// the user's decision to make, not a credential that was rejected.
	{ExitValidation, as[*connect.CertificateError]},

	{ExitAuth, as[*AuthError]},
	{ExitAuth, as[*connect.RejectedError]},
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

// ExitCode is the single translation from an error to a process exit status.
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
