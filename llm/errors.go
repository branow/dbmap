package llm

import (
	"errors"
	"net/http"
	"time"
)

// Class is the failure classification every provider maps its native errors
// onto, so retry and exit-code decisions never string-match prose.
type Class int

// The six classes. Their retry semantics live in the table below.
const (
	// ClassAuth means credentials were rejected or absent.
	ClassAuth Class = iota + 1
	// ClassRateLimited means a quota or rate limit was hit.
	ClassRateLimited
	// ClassUnavailable means the provider failed transiently.
	ClassUnavailable
	// ClassRefused means the model declined to answer.
	ClassRefused
	// ClassSchema means the answer was not JSON, or not schema-conforming.
	ClassSchema
	// ClassBadRequest means the request itself is wrong.
	ClassBadRequest
)

// The six sentinels, so a caller can write errors.Is(err, llm.ErrRateLimited).
var (
	ErrAuth        = errors.New("llm: credentials rejected or absent")
	ErrRateLimited = errors.New("llm: rate limited")
	ErrUnavailable = errors.New("llm: provider unavailable")
	ErrRefused     = errors.New("llm: model declined to answer")
	ErrSchema      = errors.New("llm: response did not satisfy the schema")
	ErrBadRequest  = errors.New("llm: request rejected")
)

// sentinels ties the class and sentinel vocabularies together.
var sentinels = map[Class]error{
	ClassAuth:        ErrAuth,
	ClassRateLimited: ErrRateLimited,
	ClassUnavailable: ErrUnavailable,
	ClassRefused:     ErrRefused,
	ClassSchema:      ErrSchema,
	ClassBadRequest:  ErrBadRequest,
}

// retryable is what the retry middleware walks. ClassSchema is retryable but
// tightly capped: a second attempt sometimes lands, a third never does.
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

// Error renders the class name.
func (c Class) String() string {
	if s, ok := sentinels[c]; ok {
		return s.Error()
	}
	return "llm: unknown error class"
}

// Error is the structured failure every provider returns. Context lives on
// fields, never baked into the message, so the caller decides what to show.
type Error struct {
	Class    Class
	Provider string
	Model    string
	// Status is the HTTP status, 0 when the failure was not an HTTP one.
	Status int
	// RetryAfter is the delay the provider asked for, 0 when it asked for none.
	RetryAfter time.Duration
	// Detail is the provider's own message, verbatim.
	Detail string
	Err    error
}

// Error renders the class and the provider's own message when there is one.
func (e *Error) Error() string {
	if e.Detail == "" {
		return e.Class.String()
	}
	return e.Class.String() + ": " + e.Detail
}

// Unwrap exposes the provider's error, so a context cancellation stays visible.
func (e *Error) Unwrap() error { return e.Err }

// Is matches this error against its class sentinel.
func (e *Error) Is(target error) bool { return sentinels[e.Class] == target }

// Retryable reports whether this failure is worth another attempt.
func (e *Error) Retryable() bool { return e.Class.Retryable() }

// Classify returns an error's class, and false when it is not from this module.
func Classify(err error) (Class, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e.Class, true
	}
	return 0, false
}

// Retryable reports whether an error is worth another attempt. One from outside
// this module never is.
func Retryable(err error) bool {
	class, ok := Classify(err)
	return ok && class.Retryable()
}

// statuses is shared by both HTTP providers.
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

// ParseRetryAfter reads a Retry-After header value, delay-seconds form only.
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
