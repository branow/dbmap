package catalog

// Entry is one object as the build carries it from the fetch stage to the
// writer: the manifest fact, the structure fetched for it, and what the two
// staleness stages decided about it.
type Entry struct {
	Object      Object
	Structure   Structure
	Fingerprint string
	Description string
}

// Key is the name this entry is indexed by.
func (e Entry) Key() string { return e.Object.Key() }

// State is what the previous build recorded about one object. It is read back
// out of the catalogs that build wrote: there is deliberately no sidecar state
// file, because a parallel file drifts out of step with the index beside it.
type State struct {
	Modified    Signal
	Fingerprint string
	Description string
	// Rows is the row count the previous build wrote, so a table that grew past
	// the depth the describer sees can be told from one that did not. Kinds
	// whose catalog stores no row count read back as zero, which is what makes
	// scoping the sample rule to tables a stated rule rather than an accident.
	Rows int64
}
