package catalog

// Withholding says why a column was kept out of a sample.
type Withholding string

const (
	// WithheldPII marks a column whose name says it holds a person's data.
	// Matched on the name: a type says nothing about who a value belongs to.
	WithheldPII Withholding = "pii"
	// WithheldUnsampleable marks a blob, spatial or (max) column: unbounded by
	// definition, and worth nothing to a describer.
	WithheldUnsampleable Withholding = "unsampleable"
)

// Withheld is a column the sample did not read, and why. Recorded rather than
// dropped silently, so the describer knows the column exists without ever
// seeing a value from it.
type Withheld struct {
	Column string
	Reason Withholding
}

// Sample is the first rows of one table, for the describer only. Sample values
// never reach the index and never enter a fingerprint: they change on every run
// against a live database and would leave every table permanently dirty.
type Sample struct {
	Key      string
	Columns  []string
	Withheld []Withheld
	Rows     [][]string
	// Complete marks a table small enough that the sample is the whole table:
	// how a lookup table's value domain is captured with no DISTINCT scan.
	Complete bool
}
