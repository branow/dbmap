package index

import (
	"github.com/branow/dbmap/internal/cache"
	"github.com/branow/dbmap/internal/catalog"
	"github.com/branow/dbmap/internal/redact"
)

// store is the fetch cache as the build sees it: every reason an entry might
// not be usable collapses to a miss, and a cache that cannot be written is a
// warning rather than a failed build. A build given no cache root gets a store
// that misses everything and writes nothing, so no stage below has to ask
// whether caching is on.
type store struct {
	scope  *cache.Scope
	logger Logger
	// writable is false for a dry run, which must leave nothing behind
	// anywhere.
	writable bool
}

// openCache scopes the cache to one database. An empty root disables it
// entirely, which is what a build with nowhere to cache asks for.
func openCache(root, environment, database string, writable bool, logger Logger) store {
	s := store{writable: writable, logger: logger}
	if root != "" {
		s.scope = cache.New(root).Scope(environment, database)
	}
	return s
}

// structure reads one object's cached structure, valid only while the signal
// still proves the object untouched.
func (s store) structure(key string, signal catalog.Signal) (catalog.Structure, bool) {
	if s.scope == nil {
		return catalog.Structure{}, false
	}
	value, ok, err := s.scope.Structure(key, signal)
	if err != nil {
		warn(s.logger, "cache: "+err.Error())
		return catalog.Structure{}, false
	}
	return value, ok
}

// module reads one object's cached body and the tally of what was stripped out
// of it. The tally is replayed so a resumed build still reports a secret it
// found on an earlier run.
func (s store) module(key string, signal catalog.Signal) (cache.Module, bool) {
	if s.scope == nil {
		return cache.Module{}, false
	}
	value, ok, err := s.scope.Module(key, signal)
	if err != nil {
		warn(s.logger, "cache: "+err.Error())
		return cache.Module{}, false
	}
	return value, ok
}

// put stores what the database just returned. A cache is an optimisation, so a
// filesystem that refuses the write is reported and survived.
func (s store) putStructure(key string, signal catalog.Signal, value catalog.Structure) {
	if s.scope == nil || !s.writable {
		return
	}
	if err := s.scope.PutStructure(key, signal, value); err != nil {
		warn(s.logger, "cache: "+err.Error())
	}
}

func (s store) putModule(key string, signal catalog.Signal, body redact.Body) {
	if s.scope == nil || !s.writable {
		return
	}
	if err := s.scope.PutModule(key, signal, body); err != nil {
		warn(s.logger, "cache: "+err.Error())
	}
}

func (s store) putManifest(manifest cache.Manifest) {
	if s.scope == nil || !s.writable {
		return
	}
	if err := s.scope.PutManifest(manifest); err != nil {
		warn(s.logger, "cache: "+err.Error())
	}
}
