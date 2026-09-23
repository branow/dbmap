package catalog

// Withholding says why a column was kept out of a sample.
type Withholding string

const (
	// WithheldPII is matched on the column name: a type says nothing about who
	// a value belongs to.
	WithheldPII          Withholding = "pii"
	WithheldUnsampleable Withholding = "unsampleable"
)

// Withheld lets the describer know a column exists without seeing a value.
type Withheld struct {
	Column string
	Reason Withholding
}

// Sample is the first rows of one table, for the describer only. Its values
// never reach the index and never enter a fingerprint.
type Sample struct {
	Key      string
	Columns  []string
	Withheld []Withheld
	Rows     [][]string
	// Complete marks a table small enough that the sample is the whole table.
	Complete bool
}
