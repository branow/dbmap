package sqlserver

import "testing"

// A SQL bit reaches a string scan differently depending on the driver:
// go-mssqldb returns a Go bool, which reads as "true", while the same catalog
// column renders as "1" elsewhere. Reading only "1" made every flag false, so
// a nullable column was written "not null" and a primary key was recorded as an
// ordinary index. Both spellings must work.
func TestFlagAcceptsEverySpellingADriverMightHandBack(t *testing.T) {
	for _, cell := range []string{"1", "true", "TRUE", "True", " true ", "t"} {
		if !flag(cell) {
			t.Errorf("flag(%q) = false, want true", cell)
		}
	}
	for _, cell := range []string{"0", "false", "FALSE", "", "  ", "null"} {
		if flag(cell) {
			t.Errorf("flag(%q) = true, want false", cell)
		}
	}
}
