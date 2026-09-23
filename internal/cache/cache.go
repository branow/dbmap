// Package cache is the resumable on-disk fetch cache, stored per environment
// and database. Entries are keyed by object and by the engine's modify signal,
// so a changed object is a miss by construction. Module bodies enter only
// through PutModule, which takes a type only the redactor can produce.
package cache

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/branow/dbmap/internal/catalog"
)

// Store is a cache rooted at one directory; every operation touches one file.
type Store struct {
	root string
}

// New opens the cache under root, creating the directory lazily on first write.
func New(root string) *Store { return &Store{root: root} }

// Root is the directory this cache lives in.
func (s *Store) Root() string { return s.root }

// Scope narrows the cache to one database in one environment. Both names are
// encoded into single path segments, so neither can escape the cache root.
func (s *Store) Scope(environment, database string) *Scope {
	return &Scope{
		dir:         filepath.Join(s.root, segment(environment), segment(database)),
		environment: environment,
		database:    database,
	}
}

// Scope is one database's cache.
type Scope struct {
	dir         string
	environment string
	database    string
}

// Dir is where this scope's entries live.
func (s *Scope) Dir() string { return s.dir }

// artifact says what the fetch stage stores and where it lands.
type artifact struct {
	name string
	// dir holds one file per object, empty for a whole-database artifact.
	dir string
	// proven marks entries valid only while the modify signal is unchanged.
	proven bool
}

// The manifest carries no signal: it is what tells a resumed run what the
// signals are.
var (
	manifestOf  = artifact{name: "manifest"}
	structureOf = artifact{name: "structure", dir: "structure", proven: true}
	moduleOf    = artifact{name: "module", dir: "modules", proven: true}
)

func (s *Scope) path(a artifact, key string) string {
	if a.dir == "" {
		return filepath.Join(s.dir, a.name+".json")
	}
	return filepath.Join(s.dir, a.dir, entry(key))
}

// load reads one entry. Absent, corrupt, mis-keyed and superseded all collapse
// to one miss, because the answer to every one of them is to fetch.
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

// entry names one object's file. The digest suffix keeps apart two keys a
// case-insensitive filesystem would fold together.
func entry(key string) string {
	return fmt.Sprintf("%s-%s.json", segment(key), checksum([]byte(key))[:8])
}

// segment encodes an arbitrary catalog name as exactly one path segment, a
// leading dot escaped too, so no name can address a file outside the cache.
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
