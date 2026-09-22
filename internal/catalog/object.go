package catalog

// Signal is an engine's cheap "has this object changed" indicator — SQL
// Server's modify_date, already free in the manifest. It decides whether to
// FETCH: it never misses a real change but over-reports badly, because a
// release that ALTERs 58 procedures bumps all 58 dates even when most are
// byte-identical. The exact content fingerprint then decides whether to
// DESCRIBE, so the loose check guards the cheap stage and the exact one guards
// the LLM call.
//
// A signal is OPTIONAL. Postgres has no modify_date, so an object from an
// engine that supplies none carries the empty Signal and can never be proven
// untouched — it always fetches, and the content fingerprint does all the real
// gating. That is a property of the object, not of an engine's name, so no
// stage anywhere special-cases an engine.
type Signal string

// Present reports whether an engine supplied a modify signal at all.
func (s Signal) Present() bool { return s != "" }

// Same reports whether two signals prove an object untouched. An absent signal
// proves nothing, so it is never the same as anything — including another
// absent signal.
func (s Signal) Same(other Signal) bool { return s.Present() && s == other }

// String renders the signal for the index, where it earns its place for a
// reader too: a procedure untouched since 2018 says something a description
// cannot.
func (s Signal) String() string { return string(s) }

// Object is one indexed database object as the manifest reports it. Rows and KB
// come from stored page totals, never from a scan.
type Object struct {
	Schema   string
	Name     string
	Kind     Kind
	Rows     int64
	KB       int64
	Triggers int
	Modified Signal
}

// Key is the name the whole pipeline indexes an object by, and the first cell
// of every catalog row.
func (o Object) Key() string { return o.Schema + "." + o.Name }

// Describable reports whether this object earns a generated sentence, read off
// the kind table rather than decided here. An object of a kind the index does
// not cover is not describable.
func (o Object) Describable() bool {
	spec, ok := Lookup(o.Kind)
	return ok && spec.Describe
}
