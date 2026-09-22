// Package cache is the resumable on-disk fetch cache: the three artifacts the
// fetch stage produces — manifest, structure and module bodies — stored per
// environment and database so a killed run restarts and refetches nothing.
//
// Two properties make that true rather than hopeful:
//
// Entries are keyed by object AND by the engine's modify signal. A hit is only
// a hit when the signal proves the object untouched, so a changed object is a
// miss by construction and an engine that supplies no signal (Postgres has no
// modify_date) always refetches. No stage anywhere names an engine to get that.
//
// Every write is atomic and every read is checksummed. A killed write leaves a
// temporary file no read path opens; a truncated or tampered entry fails its
// checksum and reads as a miss. Nothing here ever deserialises into garbage.
//
// Module bodies enter only through PutModule, which takes a redact.Body — a
// type only the redactor can produce. The cache therefore cannot hold a raw
// body even if a caller tries, which is the point: bodies are redacted on
// arrival, before this package sees them.
package cache

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/branow/dbmap/internal/catalog"
)

// Store is a cache rooted at one directory. It owns no state beyond that path:
// every operation reads or writes exactly one file, so two runs over different
// databases never contend.
type Store struct {
	root string
}

// New opens the cache under root. The directory is created lazily, on the first
// write, so listing a cache that has never been built is not a side effect.
func New(root string) *Store { return &Store{root: root} }

// Root is the directory this cache lives in.
func (s *Store) Root() string { return s.root }

// Scope narrows the cache to one database in one environment, which is the unit
// everything else is keyed inside. Both names are encoded into single path
// segments, so a name carrying a separator or a parent reference cannot reach
// outside the cache root.
func (s *Store) Scope(environment, database string) *Scope {
	return &Scope{
		dir:         filepath.Join(s.root, segment(environment), segment(database)),
		environment: environment,
		database:    database,
	}
}

// Scope is one database's cache: the manifest for it, and one entry per object
// for structure and for module bodies.
type Scope struct {
	dir         string
	environment string
	database    string
}

// Dir is where this scope's entries live.
func (s *Scope) Dir() string { return s.dir }

// artifact is one row of the artifact table: what the fetch stage stores, where
// it lands, and whether an entry needs a modify signal to count as a hit.
type artifact struct {
	name string
	// dir is the subdirectory holding one file per object, empty for an
	// artifact that is a single file for the whole database.
	dir string
	// proven marks an artifact whose entries are per-object and therefore only
	// valid while the object's modify signal says it has not changed.
	proven bool
}

// The three artifacts the fetch stage produces. The manifest is the checkpoint
// everything else resumes from, so it is one file and carries no signal: it is
// the thing that tells a resumed run what the signals even are.
var (
	manifestOf  = artifact{name: "manifest"}
	structureOf = artifact{name: "structure", dir: "structure", proven: true}
	moduleOf    = artifact{name: "module", dir: "modules", proven: true}
)

// path is where one entry lives.
func (s *Scope) path(a artifact, key string) string {
	if a.dir == "" {
		return filepath.Join(s.dir, a.name+".json")
	}
	return filepath.Join(s.dir, a.dir, entry(key))
}

// load reads one entry and reports whether it is usable. Every reason it might
// not be — absent, corrupt, written for another object, superseded by a newer
// modify signal — collapses to the same miss, because the caller's answer to
// all of them is to fetch.
func load[T any](s *Scope, a artifact, key string, signal catalog.Signal) (T, bool, error) {
	var value T
	found, ok, err := read(s.path(a, key))
	if err != nil || !ok {
		return value, false, err
	}
	if found.Artifact != a.name || found.Key != key {
		return value, false, nil
	}
	if a.proven && !signal.Same(found.Signal) {
		return value, false, nil
	}
	if json.Unmarshal(found.Payload, &value) != nil {
		var zero T
		return zero, false, nil
	}
	return value, true, nil
}

// save stores one entry, stamped with the object and signal it was fetched at.
func save[T any](s *Scope, a artifact, key string, signal catalog.Signal, value T) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return &StoreError{Op: OpWrite, Artifact: a.name, Key: key, Path: s.path(a, key), Err: err}
	}
	return write(s.path(a, key), envelope{
		Artifact: a.name,
		Key:      key,
		Signal:   signal,
		Checksum: payloadSum(payload),
		Payload:  payload,
	})
}

// entry is the file name for one object's entry. The escaped key keeps the file
// readable for a human looking at the cache; the digest suffix keeps two keys
// apart that a case-insensitive filesystem would otherwise fold together.
func entry(key string) string {
	return fmt.Sprintf("%s-%s.json", segment(key), checksum([]byte(key))[:8])
}

// segment encodes an arbitrary catalog name as exactly one path segment.
// Anything outside a conservative alphabet is percent-escaped, and a leading
// dot is escaped too, so neither a separator nor a parent reference survives
// and no name can address a file outside the cache.
func segment(name string) string {
	var out strings.Builder
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.' && i > 0:
			out.WriteByte(c)
		default:
			fmt.Fprintf(&out, "%%%02X", c)
		}
	}
	if out.Len() == 0 {
		return "%00"
	}
	return out.String()
}
