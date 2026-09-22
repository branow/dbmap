package llm

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestClassSentinelAndRetryability(t *testing.T) {
	cases := []struct {
		name      string
		class     Class
		sentinel  error
		retryable bool
	}{
		{"auth", ClassAuth, ErrAuth, false},
		{"rate limited", ClassRateLimited, ErrRateLimited, true},
		{"unavailable", ClassUnavailable, ErrUnavailable, true},
		{"refused", ClassRefused, ErrRefused, false},
		{"schema", ClassSchema, ErrSchema, true},
		{"bad request", ClassBadRequest, ErrBadRequest, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := error(&Error{Class: c.class, Provider: "p"})
			if !errors.Is(err, c.sentinel) {
				t.Fatalf("errors.Is(%v, %v) = false", err, c.sentinel)
			}
			for _, other := range cases {
				if other.class == c.class {
					continue
				}
				if errors.Is(err, other.sentinel) {
					t.Fatalf("%v also matched %v", err, other.sentinel)
				}
			}
			if got := Retryable(err); got != c.retryable {
				t.Fatalf("Retryable = %v, want %v", got, c.retryable)
			}
			class, ok := Classify(err)
			if !ok || class != c.class {
				t.Fatalf("Classify = %v, %v", class, ok)
			}
		})
	}
}

func TestClassifyIgnoresForeignErrors(t *testing.T) {
	err := errors.New("something else")
	if _, ok := Classify(err); ok {
		t.Fatal("a foreign error was classified")
	}
	if Retryable(err) {
		t.Fatal("a foreign error was called retryable")
	}
}

func TestErrorUnwrapKeepsTheCause(t *testing.T) {
	err := error(&Error{Class: ClassUnavailable, Err: context.Canceled})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("the cause was lost")
	}
	if !errors.Is(err, ErrUnavailable) {
		t.Fatal("the class was lost")
	}
}

func TestErrorMessageCarriesOnlyClassAndDetail(t *testing.T) {
	bare := (&Error{Class: ClassRefused}).Error()
	if bare != ErrRefused.Error() {
		t.Fatalf("bare message = %q", bare)
	}
	detailed := (&Error{Class: ClassRefused, Detail: "policy", Model: "m", Status: 400}).Error()
	if detailed != ErrRefused.Error()+": policy" {
		t.Fatalf("detailed message = %q", detailed)
	}
}

func TestClassifyStatus(t *testing.T) {
	cases := []struct {
		status int
		class  Class
	}{
		{http.StatusBadRequest, ClassBadRequest},
		{http.StatusUnauthorized, ClassAuth},
		{http.StatusForbidden, ClassAuth},
		{http.StatusNotFound, ClassBadRequest},
		{http.StatusRequestTimeout, ClassUnavailable},
		{http.StatusRequestEntityTooLarge, ClassBadRequest},
		{http.StatusUnprocessableEntity, ClassBadRequest},
		{http.StatusTooManyRequests, ClassRateLimited},
		{http.StatusInternalServerError, ClassUnavailable},
		{http.StatusBadGateway, ClassUnavailable},
		{529, ClassUnavailable},
		{http.StatusTeapot, ClassBadRequest},
	}
	for _, c := range cases {
		if got := ClassifyStatus(c.status); got != c.class {
			t.Fatalf("ClassifyStatus(%d) = %v, want %v", c.status, got, c.class)
		}
	}
}

func TestParseRetryAfter(t *testing.T) {
	cases := []struct {
		value string
		want  time.Duration
	}{
		{"", 0},
		{"3", 3 * time.Second},
		{"0.5", 500 * time.Millisecond},
		{"-1", 0},
		{"Wed, 21 Oct 2015 07:28:00 GMT", 0},
	}
	for _, c := range cases {
		if got := ParseRetryAfter(c.value); got != c.want {
			t.Fatalf("ParseRetryAfter(%q) = %v, want %v", c.value, got, c.want)
		}
	}
}
