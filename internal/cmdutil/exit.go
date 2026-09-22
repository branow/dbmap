package cmdutil

import (
	"errors"

	"github.com/branow/dbmap/internal/config"
	"github.com/branow/dbmap/internal/credentials"
	"github.com/branow/dbmap/internal/iostreams"
	"github.com/branow/dbmap/internal/output"
)

// The exit table. It is the tool's contract with a script that calls it, so it
// is documented here and nowhere else.
//
//	0  ok           the command did what it was asked
//	1  generic      anything unclassified
//	2  cancelled    the user aborted, or input ended at a prompt
//	3  validation   the input was refused: bad value, missing required value
//	4  auth         a credential was presented and rejected
//	5  not found    a named entry, setting or stored secret does not exist
//	6  unavailable  a dependency could not be reached: keychain, database, model
const (
	ExitOK          = 0
	ExitError       = 1
	ExitCancelled   = 2
	ExitValidation  = 3
	ExitAuth        = 4
	ExitNotFound    = 5
	ExitUnavailable = 6
)

// rules map an error to an exit code. They are a table walked in order, and
// every match is errors.Is or errors.As: a message is never inspected, so
// rewording an error can never change a script's behaviour.
var rules = []struct {
	code  int
	match func(error) bool
}{
	{ExitCancelled, is(ErrCancelled)},
	{ExitCancelled, is(iostreams.ErrCancelled)},
	{ExitValidation, is(iostreams.ErrNoInput)},
	{ExitValidation, as[*ValidationError]},
	{ExitValidation, as[*config.InvalidError]},
	{ExitValidation, as[*config.InUseError]},
	{ExitValidation, as[*output.UnknownFormatError]},
	{ExitAuth, as[*AuthError]},
	{ExitNotFound, as[*NotConfiguredError]},
	{ExitNotFound, as[*config.NotFoundError]},
	{ExitNotFound, as[*credentials.NotFoundError]},
	{ExitUnavailable, as[*UnavailableError]},
	{ExitUnavailable, as[*credentials.KeychainError]},
	{ExitUnavailable, as[*credentials.ReadOnlyError]},
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
