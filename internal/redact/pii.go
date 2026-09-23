package redact

import "regexp"

// The classes the column table recognises, alongside ClassPassword and
// ClassEmail, which mean the same thing on a column name as they do in a body.
const (
	ClassPhone      Class = "phone"
	ClassGovernment Class = "government-id"
	ClassBirth      Class = "birth"
	ClassName       Class = "name"
	ClassAddress    Class = "address"
	ClassFinancial  Class = "financial"
	ClassLocation   Class = "location"
	ClassBiometric  Class = "biometric"
)

// Column is one row of the PII table: a class of personal data and the
// column-name pattern that betrays it. The match is on the NAME, never the
// type, because a type cannot tell a product code from a surname.
type Column struct {
	Class   Class
	Pattern *regexp.Regexp
}

// Columns is the PII table. A column matching any row is never selected into a
// sample, so its values reach neither the cache, nor a prompt, nor the index.
// The patterns are deliberately loose: a withheld harmless column costs one
// column of context and is recorded, where a leak cannot be undone.
var Columns = []Column{
	{Class: ClassEmail, Pattern: regexp.MustCompile(`(?i)e?mail`)},
	{Class: ClassPhone, Pattern: regexp.MustCompile(`(?i)phone|mobile|fax`)},
	{
		Class:   ClassGovernment,
		Pattern: regexp.MustCompile(`(?i)ssn|socialsecurity|nationalid|taxid|vatid`),
	},
	{Class: ClassBirth, Pattern: regexp.MustCompile(`(?i)birth|dob\b`)},
	{Class: ClassPassword, Pattern: regexp.MustCompile(`(?i)password|secret|token`)},
	{
		Class:   ClassName,
		Pattern: regexp.MustCompile(`(?i)first_?name|last_?name|middle_?name|full_?name|maiden`),
	},
	{
		Class:   ClassAddress,
		Pattern: regexp.MustCompile(`(?i)address|street|city|zip|postal|county|province`),
	},
	{
		Class:   ClassFinancial,
		Pattern: regexp.MustCompile(`(?i)card|cvv|ccnum|iban|routing|accountnumber`),
	},
	{Class: ClassLocation, Pattern: regexp.MustCompile(`(?i)latitude|longitude|geoloc`)},
	{Class: ClassBiometric, Pattern: regexp.MustCompile(`(?i)signature|photo|avatar`)},
}

// Classify reports which class of personal data a column name says it holds,
// and whether any row matched. The class is returned rather than a bare verdict
// so a sample can record why it withheld a column.
func Classify(column string) (Class, bool) {
	for _, rule := range Columns {
		if rule.Pattern.MatchString(column) {
			return rule.Class, true
		}
	}
	return "", false
}

// IsPII reports whether a column may never be sampled.
func IsPII(column string) bool {
	_, ok := Classify(column)
	return ok
}
