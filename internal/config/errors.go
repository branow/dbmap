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

// NotFoundError reports a name that is not defined.
type NotFoundError struct {
	Kind Kind
	Name string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("%s %q is not defined", e.Kind, e.Name)
}

// InvalidError reports a value this tool does not accept.
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

// InUseError reports a name a profile still binds. Removal refuses rather than
// leaving that profile bound to something gone.
type InUseError struct {
	Kind Kind
	Name string
	By   []string
}

func (e *InUseError) Error() string {
	return fmt.Sprintf("%s %q is used by profile %s",
		e.Kind, e.Name, strings.Join(e.By, ", "))
}
