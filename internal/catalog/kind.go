// Package catalog is the engine-agnostic vocabulary every other domain package
// speaks: what an indexed object is, what was fetched about it, and what the
// previous build recorded. It imports nothing and knows no SQL — an engine maps
// its own catalog type codes onto these types at the boundary, and nothing
// above that boundary ever sees a type code.
package catalog

// Kind is what an indexed object is.
type Kind string

// The kinds the index covers. Triggers are deliberately absent: one database
// holds 258 of them at close to one per table, generated changelog writers, so
// the parent table records a trigger count instead of describing each one.
const (
	Table     Kind = "table"
	View      Kind = "view"
	Procedure Kind = "procedure"
	Function  Kind = "function"
	Synonym   Kind = "synonym"
)

// Spec is one row of the kind table: everything the pipeline needs to know
// about a kind, so no stage branches on a kind itself.
type Spec struct {
	Kind Kind
	// Describe marks a kind worth a generated sentence. A synonym is indexed
	// for the target it points at, not for a sentence about it.
	Describe bool
	// Module marks a kind that carries a body, and therefore fingerprints over
	// its definition rather than over its structure.
	Module bool
}

// Kinds is the vocabulary as data, walked rather than switched on.
var Kinds = []Spec{
	{Kind: Table, Describe: true, Module: false},
	{Kind: View, Describe: true, Module: true},
	{Kind: Procedure, Describe: true, Module: true},
	{Kind: Function, Describe: true, Module: true},
	{Kind: Synonym, Describe: false, Module: false},
}

var specs = func() map[Kind]Spec {
	byKind := make(map[Kind]Spec, len(Kinds))
	for _, spec := range Kinds {
		byKind[spec.Kind] = spec
	}
	return byKind
}()

// Lookup returns the kind table row for k, and whether the index covers k at
// all. An unrecognised kind is reported rather than guessed at, so a type an
// engine starts returning cannot slip into the index unclassified.
func Lookup(k Kind) (Spec, bool) {
	spec, ok := specs[k]
	return spec, ok
}
