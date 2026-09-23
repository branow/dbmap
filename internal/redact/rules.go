package redact

import "regexp"

// Class names a kind of sensitive value, shared by both tables here: the class
// that strips an address out of a body is the one that keeps an address column
// out of a sample.
type Class string

const (
	ClassPassword   Class = "password"
	ClassConnection Class = "connection-string"
	ClassAPIKey     Class = "api-key"
	ClassBearer     Class = "bearer-token"
	ClassEmail      Class = "email"
)

// Redacted replaces a dropped value; the key survives it.
const Redacted = "<redacted>"

// Rule is one row of the secret table. Replacement is an expansion template, so
// a rule can keep the part of its own match that is safe to index.
type Rule struct {
	Class       Class
	Pattern     *regexp.Regexp
	Replacement string
}

// Rules is the secret table, in application order. Order is load-bearing: the
// keyed rules run before the bare address rule, so a credential written in the
// shape of an address is counted as a credential rather than as mail. Every
// pattern is anchored on a key it keeps, because a rule that fired on ordinary
// SQL would teach a reader to ignore the count.
var Rules = []Rule{
	{
		Class: ClassPassword,
		// The leading \w* catches vendor-prefixed parameter names that a word
		// boundary would miss.
		Pattern:     keyed(`\w*(?:password|pwd)`),
		Replacement: "${1}${2}" + Redacted,
	},
	{
		Class: ClassConnection,
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
		// An address is a person, so it goes even though it is not a credential.
		Pattern:     regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`),
		Replacement: "<email>",
	},
}

// keyed builds the `key = value` shape the credential rules share. A quoted
// value may contain spaces; a bare one ends where the statement does.
func keyed(keys string) *regexp.Regexp {
	return regexp.MustCompile(`(?i)\b(` + keys + `)(\s*=\s*)(?:'[^'\n]*'|"[^"\n]*"|[^\s,;'")]+)`)
}
