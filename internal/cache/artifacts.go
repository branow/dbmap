package cache

import (
	"time"

	"github.com/branow/dbmap/internal/catalog"
	"github.com/branow/dbmap/internal/redact"
)

// BodyChars caps a stored module body, above the prompt cap so the stored copy
// is always the fuller one.
const BodyChars = 50000

// Manifest is the fetch stage's checkpoint, and what a resumed run reads first.
type Manifest struct {
	Environment string           `json:"environment"`
	Database    string           `json:"database"`
	FetchedAt   time.Time        `json:"fetchedAt"`
	Objects     []catalog.Object `json:"objects"`
}

// Module is a cached body plus the tally of what was stripped; the tally is
// stored so a run that refetches nothing still reports a secret was found.
type Module struct {
	Body       string        `json:"body"`
	Redactions redact.Counts `json:"redactions,omitempty"`
}

// Manifest reads the cached manifest for this database.
func (s *Scope) Manifest() (Manifest, bool, error) {
	return load[Manifest](s, manifestOf, "", "")
}

// PutManifest replaces any previous manifest; it is only written complete.
func (s *Scope) PutManifest(manifest Manifest) error {
	return save(s, manifestOf, "", "", manifest)
}

// Structure reads what was fetched about one object, valid only while signal
// still proves the object untouched.
func (s *Scope) Structure(key string, signal catalog.Signal) (catalog.Structure, bool, error) {
	return load[catalog.Structure](s, structureOf, key, signal)
}

// PutStructure stores one object's structure, dropping Definition: a structure
// arrives from the engine unredacted, so a body may only enter via PutModule.
func (s *Scope) PutStructure(key string, signal catalog.Signal, value catalog.Structure) error {
	value.Definition = ""
	return save(s, structureOf, key, signal, value)
}

// Module reads one object's cached body.
func (s *Scope) Module(key string, signal catalog.Signal) (Module, bool, error) {
	return load[Module](s, moduleOf, key, signal)
}

// PutModule stores one object's body, capped at BodyChars. It takes a
// redact.Body so unredacted text cannot be offered.
func (s *Scope) PutModule(key string, signal catalog.Signal, body redact.Body) error {
	return save(s, moduleOf, key, signal, Module{
		Body:       trim(body.String()),
		Redactions: body.Counts(),
	})
}

// trim counts runes, so a cut never lands inside one.
func trim(body string) string {
	runes := []rune(body)
	if len(runes) <= BodyChars {
		return body
	}
	return string(runes[:BodyChars])
}
