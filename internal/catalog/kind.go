// Package catalog is the engine-agnostic vocabulary every other domain package
// speaks. It imports nothing and knows no SQL.
package catalog

// Kind is what an indexed object is.
type Kind string

// The kinds the index covers. Triggers are deliberately absent; a table records
// a trigger count instead.
const (
	Table     Kind = "table"
	View      Kind = "view"
	Procedure Kind = "procedure"
	Function  Kind = "function"
	Synonym   Kind = "synonym"
)

// Spec is one row of the kind table, so no stage branches on a kind itself.
type Spec struct {
	Kind     Kind
	Describe bool
	// Module marks a kind that carries a body, and so fingerprints over its
	// definition rather than over its structure.
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
// all, so an unrecognised kind is reported rather than guessed at.
func Lookup(k Kind) (Spec, bool) {
	spec, ok := specs[k]
	return spec, ok
}
