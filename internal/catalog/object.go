package catalog

// Signal is an engine's cheap "has this object changed" indicator, already free
// in the manifest. It gates the FETCH stage only: it never misses a real change
// but over-reports badly, since one release ALTERing many procedures bumps every
// date even where the bodies are byte-identical. The exact content fingerprint
// then gates DESCRIBE, so the loose check guards the cheap stage and the exact
// one guards the LLM call.
//
// A signal is OPTIONAL. An engine that supplies none leaves it empty, and an
// object with an empty signal can never be proven untouched — it always fetches.
// That is a property of the object, not of an engine's name, so no stage
// special-cases an engine.
type Signal string

// Present reports whether an engine supplied a modify signal at all.
func (s Signal) Present() bool { return s != "" }

// Same reports whether two signals prove an object untouched. An absent signal
// proves nothing, so it is never the same as anything — including another
// absent signal.
func (s Signal) Same(other Signal) bool { return s.Present() && s == other }

// String renders the signal for the index, where it tells a reader something a
// description cannot: how long an object has gone untouched.
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
// the kind table rather than decided here.
func (o Object) Describable() bool {
	spec, ok := Lookup(o.Kind)
	return ok && spec.Describe
}
