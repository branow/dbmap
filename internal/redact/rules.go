package redact

import "regexp"

// Class names a kind of sensitive value. It is shared by both tables in this
// package: the same class that strips an address out of a module body is the
// one that keeps an address column out of a sample, so a caller reasons about
// one vocabulary rather than two.
type Class string

// The classes the secret table strips out of text.
const (
	ClassPassword   Class = "password"
	ClassConnection Class = "connection-string"
	ClassAPIKey     Class = "api-key"
	ClassBearer     Class = "bearer-token"
	ClassEmail      Class = "email"
)

// Redacted is what replaces a dropped value. The key survives it: that a
// procedure authenticates somewhere is worth indexing, the credential never is.
const Redacted = "<redacted>"

// Rule is one row of the secret table. The engine in Text walks these in order
// and never branches on a class, so adding a class is adding a row.
type Rule struct {
	Class   Class
	Pattern *regexp.Regexp
	// Replacement is a regexp expansion template, so a rule can keep the part
	// of its own match that is safe to index.
	Replacement string
}

// Rules is the secret table, in application order. Order is load-bearing: a
// keyed rule runs before the bare email rule so that a credential written as an
// address (`password = admin@example.internal`) is counted as a password rather
// than as mail.
//
// Every pattern is anchored on a key it keeps. A rule that fired on ordinary
// SQL would be worse than useless — it would teach a reader to ignore the
// count. Measured baseline over 622 procedure bodies: 7 addresses in mail-send
// calls, zero credentials.
var Rules = []Rule{
	{
		Class: ClassPassword,
		// The leading \w* is a deliberate widening of the rule this was ported
		// from, which anchored on a word boundary and therefore missed
		// @rmtpassword — the parameter that carries a linked server's password,
		// and the one place a credential most reliably appears.
		Pattern:     keyed(`\w*(?:password|pwd)`),
		Replacement: "${1}${2}" + Redacted,
	},
	{
		Class: ClassConnection,
		// A connection string is a chain of keyed values, so the parts that
		// name a deployment go the same way a password does.
		Pattern: regexp.MustCompile(
			`(?i)\b(data source|initial catalog|integrated security)(\s*=\s*)[^;'"\n]+`),
		Replacement: "${1}${2}" + Redacted,
	},
	{
		Class:       ClassAPIKey,
		Pattern:     keyed(`api[_-]?key|access[_-]?token|client[_-]?secret|secret[_-]?key`),
		Replacement: "${1}${2}" + Redacted,
	},
	{
		Class:       ClassBearer,
		Pattern:     regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._\-]{12,}`),
		Replacement: "Bearer " + Redacted,
	},
	{
		Class: ClassEmail,
		// The only class that fires in practice: staff distribution lists in
		// mail-send calls. An address is a person, so it goes even though it is
		// not a credential.
		Pattern:     regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`),
		Replacement: "<email>",
	},
}

// keyed builds the `key = value` shape the credential rules share: the key and
// its separator are captured so they survive, and the value — quoted or bare —
// is dropped whole. A quoted value may contain spaces, which a bare one may
// not, because a bare value ends where the statement does.
func keyed(keys string) *regexp.Regexp {
	return regexp.MustCompile(`(?i)\b(` + keys + `)(\s*=\s*)(?:'[^'\n]*'|"[^"\n]*"|[^\s,;'")]+)`)
}
