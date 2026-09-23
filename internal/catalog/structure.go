package catalog

// Returns is the parameter name carrying a function's return type; catalogs
// emit that row with an empty name.
const Returns = "(returns)"

// Column is one column of a table or view.
type Column struct {
	Name string
	Type string
	// Length is already rendered: "(20)", "(18,2)", "(max)", or empty.
	Length   string
	Nullable bool
	Identity bool
	Computed bool
}

// Index is one non-primary index.
type Index struct {
	Name    string
	Unique  bool
	Columns []string
}

// ForeignKey is one declared foreign key column.
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
// Which fields are populated follows from the kind.
type Structure struct {
	Columns     []Column
	PrimaryKey  []string
	ForeignKeys []ForeignKey
	Indexes     []Index
	Parameters  []Param
	Definition  string
	Target      string
}

// MaxDefinition caps one module body so a pathological body cannot decide the
// size of a fetch batch. It lives here because the engine that applies it and
// the writer that reports a cut body must agree on it.
const MaxDefinition = 50000
