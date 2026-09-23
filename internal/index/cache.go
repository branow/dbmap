package index

import (
	"github.com/branow/dbmap/internal/cache"
	"github.com/branow/dbmap/internal/catalog"
	"github.com/branow/dbmap/internal/redact"
)

// store is the fetch cache as the build sees it: every unusable entry is a
// miss, and an unwritable cache is a warning, not a failed build. With no cache
// root it misses everything, so no stage below asks whether caching is on.
type store struct {
	scope  *cache.Scope
	logger Logger
	// writable is false for a dry run, which must leave nothing behind
	// anywhere.
	writable bool
}

// openCache scopes the cache to one database; an empty root disables it.
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

// module reads one object's cached body and its redaction tally. The tally is
// replayed so a resumed build still reports a secret found on an earlier run.
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

// putStructure stores what the database just returned. The cache is an
// optimisation, so a refused write is reported and survived.
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
