package catalog

// Entry is one object as the build carries it from fetch to writer.
type Entry struct {
	Object      Object
	Structure   Structure
	Fingerprint string
	Description string
}

// Key is the name this entry is indexed by.
func (e Entry) Key() string { return e.Object.Key() }

// State is what the previous build recorded about one object, read back out of
// the catalogs that build wrote. There is deliberately no sidecar state file: a
// parallel file drifts out of step with the index beside it.
type State struct {
	Modified    Signal
	Fingerprint string
	Description string
	// Rows lets a table that grew past the depth the describer sees be told from
	// one that did not. Kinds whose catalog stores no row count read back as
	// zero, which keeps the sample rule scoped to tables by rule, not accident.
	Rows int64
}
