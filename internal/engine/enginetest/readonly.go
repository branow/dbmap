package enginetest

import (
	"fmt"
	"regexp"
	"strings"
)

// writes is deliberately broad: a false positive costs one test edit, a false
// negative ships a query nobody noticed writing.
var writes = regexp.MustCompile(`(?i)\b(insert|update|delete|merge|into|create|alter|drop|` +
	`truncate|grant|revoke|deny|backup|restore|dbcc|exec|execute|shutdown|kill|waitfor|` +
	`openrowset|opendatasource|openquery|xp_cmdshell|sp_configure|vacuum|copy|call)\b`)

var opensRead = regexp.MustCompile(`(?i)^\s*(select|with)\b`)

// ReadOnly reports whether a statement an engine builds is a read. Test-time
// only: at run time the account's permissions stop a write.
func ReadOnly(statement string) error {
	if !opensRead.MatchString(statement) {
		return fmt.Errorf("does not open with SELECT or WITH: %s", first(statement))
	}
	if hit := writes.FindString(statement); hit != "" {
		return fmt.Errorf("contains %q: %s", strings.ToLower(hit), first(statement))
	}
	return nil
}

func first(statement string) string {
	line := strings.TrimSpace(statement)
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	return line
}
