package catalog

// Returns is the parameter name carrying a function's return type. Catalogs
// emit the return row with an empty name, which the engine fills in with this
// so the writer can lift it out of the call signature.
const Returns = "(returns)"

// Column is one column of a table or view.
type Column struct {
	Name string
	Type string
	// Length is the width a reader needs, already rendered: "(20)", "(18,2)",
	// "(max)", or empty for a type that carries none.
	Length   string
	Nullable bool
	Identity bool
	Computed bool
}

// Index is one non-primary index, folded from the one-row-per-column shape a
// catalog returns.
type Index struct {
	Name    string
	Unique  bool
	Columns []string
}

// ForeignKey is one declared foreign key column. The index writes no foreign
// key graph — two declared keys exist across three measured databases, so joins
// here are naming convention — but a table's own keys ride along in its column
// file and in its fingerprint.
type ForeignKey struct {
	Column     string
	References string
}

// Param is one parameter of a procedure or function.
type Param struct {
	Name   string
	Type   string
	Output bool
}

// Structure is everything fetched about one object beyond its manifest row.
// Which fields are populated follows from the kind: a table has columns, a
// primary key and indexes; a module kind has a Definition; a synonym has a
// Target. The zero Structure is a valid structure for an object nothing was
// fetched for.
type Structure struct {
	Columns     []Column
	PrimaryKey  []string
	ForeignKeys []ForeignKey
	Indexes     []Index
	Parameters  []Param
	// Definition is a module body — the text a view, procedure or function
	// fingerprints over.
	Definition string
	// Target is what a synonym points at.
	Target string
}
