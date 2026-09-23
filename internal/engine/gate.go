package engine

import (
	"fmt"
	"regexp"
	"strings"
)

// Denied are the words a statement this tool sends may never contain. There is
// no second chance to catch a write, so the list is deliberately blunt: "into"
// because SELECT ... INTO is a write opening with a read's word, "exec" because
// a procedure call does whatever the procedure decides.
var Denied = []string{
	"insert",
	"update",
	"delete",
	"merge",
	"into",
	"create",
	"alter",
	"drop",
	"truncate",
	"grant",
	"revoke",
	"deny",
	"backup",
	"restore",
	"dbcc",
	"exec",
	"execute",
	"shutdown",
	"kill",
	"waitfor",
	"openrowset",
	"opendatasource",
	"openquery",
	"xp_cmdshell",
	"sp_configure",
	"vacuum",
	"analyze",
	"cluster",
	"reindex",
	"copy",
	"call",
	"do",
	"lo_import",
	"lo_export",
	"pg_read_file",
	"pg_ls_dir",
	"pg_sleep",
}

// opensRead is the positive half of the gate. Anything it cannot classify — a
// leading comment, a DECLARE, an empty string — fails closed.
var opensRead = regexp.MustCompile(`(?i)^\s*(select|with)\b`)

// denied matches a denied word on a word boundary, which is what keeps a column
// named create_date from reading as a CREATE.
var denied = regexp.MustCompile(`(?i)\b(` + strings.Join(Denied, "|") + `)\b`)

// Refusal is why the gate refused a statement.
type Refusal string

const (
	// NotARead covers every statement the gate cannot classify, not just writes.
	NotARead Refusal = "does not open with SELECT or WITH"
	// DeniedWord is a statement carrying a word from Denied.
	DeniedWord Refusal = "contains a forbidden word"
)

// RefusedError reports a statement the read-only gate would not send. It is
// returned before a connection is used: a query that cannot be proven read-only
// never reaches a server to be judged there.
type RefusedError struct {
	Refusal Refusal
	// Word is the denied word that matched, when one did.
	Word string
	// Statement is the refused SQL — this tool's own, never user input.
	Statement string
}

func (e *RefusedError) Error() string {
	if e.Word != "" {
		return fmt.Sprintf("refusing to send a query that contains %q", e.Word)
	}
	return "refusing to send a query that " + string(e.Refusal)
}

// AssertReadOnly proves a statement is a read, or refuses it. Query calls it
// for every statement, so no engine has to remember to.
func AssertReadOnly(statement string) error {
	if !opensRead.MatchString(statement) {
		return &RefusedError{Refusal: NotARead, Statement: statement}
	}
	if hit := denied.FindString(statement); hit != "" {
		return &RefusedError{Refusal: DeniedWord, Word: strings.ToLower(hit), Statement: statement}
	}
	return nil
}
