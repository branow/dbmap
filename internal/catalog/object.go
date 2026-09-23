package catalog

// Signal gates the fetch stage. Empty means the engine supplies none, and such
// an object can never be proven untouched.
type Signal string

func (s Signal) Present() bool { return s != "" }

// Same reports whether two signals prove an object untouched. An absent signal
// proves nothing, so it is never the same as anything — including another
// absent signal.
func (s Signal) Same(other Signal) bool { return s.Present() && s == other }

func (s Signal) String() string { return string(s) }

// Object is one indexed database object as the manifest reports it.
type Object struct {
	Schema   string
	Name     string
	Kind     Kind
	Rows     int64
	KB       int64
	Triggers int
	Modified Signal
}

// Key is the name the whole pipeline indexes an object by.
func (o Object) Key() string { return o.Schema + "." + o.Name }

// Describable reports whether this object earns a generated sentence.
func (o Object) Describable() bool {
	spec, ok := Lookup(o.Kind)
	return ok && spec.Describe
}
