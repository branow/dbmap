package catalog

// Entry is one object as the build carries it from fetch to writer.
type Entry struct {
	Object      Object
	Structure   Structure
	Fingerprint string
	Description string
}

func (e Entry) Key() string { return e.Object.Key() }

// State is what the previous build recorded about one object, read back out of
// the catalogs that build wrote rather than from a sidecar file.
type State struct {
	Modified    Signal
	Fingerprint string
	Description string
	// Rows reads back zero for kinds whose catalog stores no row count.
	Rows int64
}
