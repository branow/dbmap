package cache

import "fmt"

// Op names the direction a cache operation was going when it failed.
type Op string

// The two directions.
const (
	OpRead  Op = "read"
	OpWrite Op = "write"
)

// StoreError reports a cache operation the filesystem refused. Every part of
// the context is a field, so a caller decides what to show.
//
// A corrupt or half-written entry is deliberately NOT this error but a miss: a
// run that stopped because a stale file went bad would defeat the cache.
type StoreError struct {
	Op       Op
	Artifact string
	// Key is the object the entry belongs to, empty for a whole-database
	// artifact such as the manifest.
	Key  string
	Path string
	Err  error
}

func (e *StoreError) Error() string {
	subject := e.Artifact
	if e.Key != "" {
		subject = fmt.Sprintf("%s %q", e.Artifact, e.Key)
	}
	return fmt.Sprintf("cache %s of %s at %s: %v", e.Op, subject, e.Path, e.Err)
}

func (e *StoreError) Unwrap() error { return e.Err }
