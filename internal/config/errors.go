package config

import (
	"fmt"
	"strings"
)

// Kind names one of the namespaces a lookup can miss.
type Kind string

// The namespaces config.yml holds.
const (
	KindConnection Kind = "connection"
	KindBackend    Kind = "backend"
	KindProfile    Kind = "profile"
	KindSetting    Kind = "setting"
)

// NotFoundError reports a name that is not defined. The kind and the name are
// fields, not text baked into a message, so a caller can act on them.
type NotFoundError struct {
	Kind Kind
	Name string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("%s %q is not defined", e.Kind, e.Name)
}

// InvalidError reports a value that is not one this tool accepts. It carries
// the offending field so a command can point at it without parsing a string.
type InvalidError struct {
	Field   string
	Value   string
	Allowed []string
	Reason  string
}

func (e *InvalidError) Error() string {
	switch {
	case e.Reason != "":
		return fmt.Sprintf("invalid %s %q: %s", e.Field, e.Value, e.Reason)
	case len(e.Allowed) > 0:
		return fmt.Sprintf("invalid %s %q, want one of %s",
			e.Field, e.Value, strings.Join(e.Allowed, ", "))
	default:
		return fmt.Sprintf("invalid %s %q", e.Field, e.Value)
	}
}

// InUseError reports a name another entry still points at. Removal refuses
// rather than leaving a profile bound to something that no longer exists.
type InUseError struct {
	Kind Kind
	Name string
	By   []string
}

func (e *InUseError) Error() string {
	return fmt.Sprintf("%s %q is used by profile %s",
		e.Kind, e.Name, strings.Join(e.By, ", "))
}
