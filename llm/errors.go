package llm

import (
	"errors"
	"net/http"
	"time"
)

// Class is the failure classification every provider maps its native errors
// onto. Retry and exit-code decisions read the class; string-matching a
// provider's prose is not a decision procedure.
type Class int

// The six classes. Their retry semantics live in the table below, not in the
// call sites.
const (
	// ClassAuth means credentials were rejected or absent.
	ClassAuth Class = iota + 1
	// ClassRateLimited means a quota or rate limit was hit.
	ClassRateLimited
	// ClassUnavailable means the provider failed transiently: 5xx, a dropped
	// connection, a timeout.
	ClassUnavailable
	// ClassRefused means the model declined to answer. The caller reports the
	// object rather than retrying it.
	ClassRefused
	// ClassSchema means the answer did not satisfy the schema, or was not JSON
	// at all.
	ClassSchema
	// ClassBadRequest means the request itself is wrong: prompt too long, bad
	// model id, malformed schema.
	ClassBadRequest
)

// The six sentinels, so callers can write errors.Is(err, llm.ErrRateLimited)
// without reaching for the class value.
var (
	ErrAuth        = errors.New("llm: credentials rejected or absent")
	ErrRateLimited = errors.New("llm: rate limited")
	ErrUnavailable = errors.New("llm: provider unavailable")
	ErrRefused     = errors.New("llm: model declined to answer")
	ErrSchema      = errors.New("llm: response did not satisfy the schema")
	ErrBadRequest  = errors.New("llm: request rejected")
)

// sentinels maps a class to its sentinel. It is the only place the two
// vocabularies are tied together.
var sentinels = map[Class]error{
	ClassAuth:        ErrAuth,
	ClassRateLimited: ErrRateLimited,
	ClassUnavailable: ErrUnavailable,
	ClassRefused:     ErrRefused,
	ClassSchema:      ErrSchema,
	ClassBadRequest:  ErrBadRequest,
}

// retryable is the classification the retry middleware walks. ClassSchema is
// retryable but tightly capped: a second attempt sometimes lands, a third never
// does.
var retryable = map[Class]bool{
	ClassAuth:        false,
	ClassRateLimited: true,
	ClassUnavailable: true,
	ClassRefused:     false,
	ClassSchema:      true,
	ClassBadRequest:  false,
}

// Retryable reports whether a failure of this class is worth another attempt.
func (c Class) Retryable() bool { return retryable[c] }

// Error renders the class name, which is also the sentinel's message.
func (c Class) String() string {
	if s, ok := sentinels[c]; ok {
		return s.Error()
	}
	return "llm: unknown error class"
}

// Error is the structured failure every provider returns. Context lives on
// fields, never baked into the message, so the caller decides what to show.
type Error struct {
	// Class drives every retry and exit-code decision.
	Class Class
	// Provider is the client's Name at the point of failure.
	Provider string
	// Model is the model that was asked, when one was chosen.
	Model string
	// Status is the HTTP status when the failure came from an HTTP call, 0
	// otherwise.
	Status int
	// RetryAfter is the delay the provider asked for, 0 when it asked for none.
	RetryAfter time.Duration
	// Detail is the provider's own message, carried verbatim for reporting.
	Detail string
	// Err is the underlying failure, when there was one to keep.
	Err error
}

// Error renders the class and, when the provider said something useful, its
// message. Provider, model and status stay on their fields.
func (e *Error) Error() string {
	if e.Detail == "" {
		return e.Class.String()
	}
	return e.Class.String() + ": " + e.Detail
}

// Unwrap exposes the provider's own error to errors.Is and errors.As, so a
// context cancellation stays visible through the classification.
func (e *Error) Unwrap() error { return e.Err }

// Is matches this error against its class sentinel.
func (e *Error) Is(target error) bool { return sentinels[e.Class] == target }

// Retryable reports whether this failure is worth another attempt.
func (e *Error) Retryable() bool { return e.Class.Retryable() }

// Classify returns the class of any error, and false when the error did not
// come from this module.
func Classify(err error) (Class, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e.Class, true
	}
	return 0, false
}

// Retryable reports whether an arbitrary error is worth another attempt. An
// error from outside this module never is.
func Retryable(err error) bool {
	class, ok := Classify(err)
	return ok && class.Retryable()
}

// statuses maps an HTTP status onto a class. Providers share it, because the
// status vocabulary is the same on both API transports.
var statuses = map[int]Class{
	http.StatusBadRequest:            ClassBadRequest,
	http.StatusUnauthorized:          ClassAuth,
	http.StatusForbidden:             ClassAuth,
	http.StatusNotFound:              ClassBadRequest,
	http.StatusRequestTimeout:        ClassUnavailable,
	http.StatusRequestEntityTooLarge: ClassBadRequest,
	http.StatusUnprocessableEntity:   ClassBadRequest,
	http.StatusTooManyRequests:       ClassRateLimited,
}

// ClassifyStatus maps an HTTP status onto a class. Anything unlisted at or
// above 500 is transient; anything else unlisted is the caller's fault.
func ClassifyStatus(status int) Class {
	if class, ok := statuses[status]; ok {
		return class
	}
	if status >= 500 {
		return ClassUnavailable
	}
	return ClassBadRequest
}

// ParseRetryAfter reads a Retry-After header value. It understands the
// delay-seconds form only; the HTTP-date form is rare on these APIs and a wrong
// parse would be worse than no hint at all.
func ParseRetryAfter(value string) time.Duration {
	if value == "" {
		return 0
	}
	seconds, err := time.ParseDuration(value + "s")
	if err != nil || seconds < 0 {
		return 0
	}
	return seconds
}
