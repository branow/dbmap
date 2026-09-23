package cmdutil

import (
	"errors"
	"fmt"
	"strings"
)

// ErrCancelled ends a command the user aborted.
var ErrCancelled = errors.New("cancelled")

// ValidationError reports input the command refuses: a bad flag value, a
// missing required value, a contradiction between two flags.
type ValidationError struct {
	Field   string
	Value   string
	Allowed []string
	Reason  string
}

func (e *ValidationError) Error() string {
	subject := e.Field
	if e.Value != "" {
		subject = fmt.Sprintf("%s %q", e.Field, e.Value)
	}
	switch {
	case e.Reason != "":
		return fmt.Sprintf("invalid %s: %s", subject, e.Reason)
	case len(e.Allowed) > 0:
		return fmt.Sprintf("invalid %s, want one of %s", subject, strings.Join(e.Allowed, ", "))
	default:
		return fmt.Sprintf("invalid %s", subject)
	}
}

// NotConfiguredError reports a setting the user has not defined yet. Fix names
// the command that defines it.
type NotConfiguredError struct {
	What string
	Fix  string
}

func (e *NotConfiguredError) Error() string {
	if e.Fix == "" {
		return fmt.Sprintf("%s is not configured", e.What)
	}
	return fmt.Sprintf("%s is not configured: %s", e.What, e.Fix)
}

// AuthError reports a credential that was presented and rejected, as distinct
// from a missing one, which is a not-found condition.
type AuthError struct {
	Subject string
	Err     error
}

func (e *AuthError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("authentication failed for %s", e.Subject)
	}
	return fmt.Sprintf("authentication failed for %s: %v", e.Subject, e.Err)
}

func (e *AuthError) Unwrap() error { return e.Err }

// UnavailableError reports a dependency that could not be reached: a keychain,
// a database, a model endpoint. The condition is environmental, not a mistake.
type UnavailableError struct {
	Subject string
	Err     error
}

func (e *UnavailableError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("%s is unavailable", e.Subject)
	}
	return fmt.Sprintf("%s is unavailable: %v", e.Subject, e.Err)
}

func (e *UnavailableError) Unwrap() error { return e.Err }
