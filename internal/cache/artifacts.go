package cache

import (
	"time"

	"github.com/branow/dbmap/internal/catalog"
	"github.com/branow/dbmap/internal/redact"
)

// BodyChars caps a module body on the way into the cache, well above the prompt
// cap so the stored copy is always the fuller one.
const BodyChars = 50000

// Manifest is the fetch stage's checkpoint: every object in scope for one
// database, including the modify signal every later entry is keyed by. A
// resumed run reads this first.
type Manifest struct {
	Environment string           `json:"environment"`
	Database    string           `json:"database"`
	FetchedAt   time.Time        `json:"fetchedAt"`
	Objects     []catalog.Object `json:"objects"`
}

// Module is a cached module body: the redacted text, and the tally of what was
// stripped. The tally is stored so a resumed run that refetches nothing can
// still report that a secret was found.
type Module struct {
	Body       string        `json:"body"`
	Redactions redact.Counts `json:"redactions,omitempty"`
}

// Manifest reads the cached manifest for this database.
func (s *Scope) Manifest() (Manifest, bool, error) {
	return load[Manifest](s, manifestOf, "", "")
}

// PutManifest stores the manifest, replacing any previous one: it is a whole
// database's list and is only ever written complete.
func (s *Scope) PutManifest(manifest Manifest) error {
	return save(s, manifestOf, "", "", manifest)
}

// Structure reads what was fetched about one object, valid only while signal
// still proves the object untouched.
func (s *Scope) Structure(key string, signal catalog.Signal) (catalog.Structure, bool, error) {
	return load[catalog.Structure](s, structureOf, key, signal)
}

// PutStructure stores one object's structure. Definition is dropped here
// because a structure arrives from the engine unredacted; bodies enter only
// through PutModule, which takes a type only the redactor can build. Stripping
// the field makes "the cache never holds a raw secret" a property of the code
// rather than a convention callers are trusted to follow.
func (s *Scope) PutStructure(key string, signal catalog.Signal, value catalog.Structure) error {
	value.Definition = ""
	return save(s, structureOf, key, signal, value)
}

// Module reads one object's cached body.
func (s *Scope) Module(key string, signal catalog.Signal) (Module, bool, error) {
	return load[Module](s, moduleOf, key, signal)
}

// PutModule stores one object's body. It takes a redact.Body rather than a
// string so that text which has not been through the redactor cannot be
// offered, and caps the stored text at BodyChars.
func (s *Scope) PutModule(key string, signal catalog.Signal, body redact.Body) error {
	return save(s, moduleOf, key, signal, Module{
		Body:       trim(body.String()),
		Redactions: body.Counts(),
	})
}

// trim cuts a body to BodyChars, counting runes so a cut never lands inside one.
func trim(body string) string {
	runes := []rune(body)
	if len(runes) <= BodyChars {
		return body
	}
	return string(runes[:BodyChars])
}
