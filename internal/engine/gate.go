package engine

import (
	"fmt"
	"regexp"
	"strings"
)

// Denied are the words a statement this tool sends may never contain. The list
// is deliberately blunt and deliberately stricter than a shell-level gate: this
// process builds its own SQL and sends it on its own connection, so nothing
// outside the process ever sees the statement and there is no second chance to
// catch a write.
//
// "into" is on the list because SELECT ... INTO is a write wearing a read's
// opening word, and "exec" because a read that ends in a procedure call is a
// read of whatever that procedure decides to do.
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

// opensRead is the whole of the positive half of the gate. A statement that
// does not begin with one of these two words is refused without being read any
// further, so a leading comment, a DECLARE, an empty string or anything else
// this gate cannot classify fails closed.
var opensRead = regexp.MustCompile(`(?i)^\s*(select|with)\b`)

// denied matches any denied word on a word boundary. A word boundary is what
// keeps a column named create_date from reading as a CREATE: the underscore is
// a word character, so there is no boundary inside the name.
var denied = regexp.MustCompile(`(?i)\b(` + strings.Join(Denied, "|") + `)\b`)

// Refusal is why the gate refused a statement.
type Refusal string

const (
	// NotARead is a statement that does not open with SELECT or WITH, which
	// includes every statement the gate cannot classify at all.
	NotARead Refusal = "does not open with SELECT or WITH"
	// DeniedWord is a statement carrying a word from Denied — a write smuggled
	// behind a leading read, most often.
	DeniedWord Refusal = "contains a forbidden word"
)

// RefusedError reports a statement the read-only gate would not send. It is
// returned before a connection is used, which is the point: a query that cannot
// be proven read-only never reaches a server to be judged there.
type RefusedError struct {
	Refusal Refusal
	// Word is the denied word that matched, when one did.
	Word string
	// Statement is the refused SQL. It is this tool's own generated SQL, never
	// user input and never a secret.
	Statement string
}

func (e *RefusedError) Error() string {
	if e.Word != "" {
		return fmt.Sprintf("refusing to send a query that contains %q", e.Word)
	}
	return "refusing to send a query that " + string(e.Refusal)
}

// AssertReadOnly proves a statement is a read, or refuses it. It is called by
// Query for every statement, so no engine has to remember to call it.
func AssertReadOnly(statement string) error {
	if !opensRead.MatchString(statement) {
		return &RefusedError{Refusal: NotARead, Statement: statement}
	}
	if hit := denied.FindString(statement); hit != "" {
		return &RefusedError{Refusal: DeniedWord, Word: strings.ToLower(hit), Statement: statement}
	}
	return nil
}
