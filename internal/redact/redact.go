// Package redact keeps sensitive values out of the index. It holds two rule
// tables and the engines that walk them: one strips secrets out of a module
// body, the other names the columns a sample must never read.
//
// Both are pure and stand alone — no filesystem, no database, no clock — so
// redaction can happen the moment text arrives, which is the point. A module
// body is redacted before it is written to the cache and long before it reaches
// a prompt, so a secret in the schema never becomes a secret on disk.
//
// Nothing here throws away what it found. Text reports a count per class, so a
// build summary can surface a real credential rather than silently swallowing
// it.
package redact

import "sort"

// Counts is how many values of each class a pass stripped. It is a map so a
// class that never fired says nothing at all, and a summary lists only what was
// actually found.
type Counts map[Class]int

// Total is every hit across every class.
func (c Counts) Total() int {
	total := 0
	for _, n := range c {
		total += n
	}
	return total
}

// Classes are the classes that fired, in a stable order, so a summary reads the
// same way twice.
func (c Counts) Classes() []Class {
	classes := make([]Class, 0, len(c))
	for class := range c {
		classes = append(classes, class)
	}
	sort.Slice(classes, func(i, j int) bool { return classes[i] < classes[j] })
	return classes
}

// Add returns the sum of two tallies, so a build can fold every body's counts
// into one summary without either operand changing underfoot.
func (c Counts) Add(other Counts) Counts {
	if len(c) == 0 && len(other) == 0 {
		return nil
	}
	sum := make(Counts, len(c)+len(other))
	for _, from := range []Counts{c, other} {
		for class, n := range from {
			sum[class] += n
		}
	}
	return sum
}

// Body is text that has been through the redactor, and the tally of what came
// out of it. Its text is unexported and Text is its only constructor, so a
// value of this type cannot hold anything the rules did not see. That is what
// lets the cache demand a Body and thereby be structurally unable to store a
// raw body — the zero Body is empty text, which is equally safe.
type Body struct {
	text   string
	counts Counts
}

// String is the redacted text.
func (b Body) String() string { return b.text }

// Counts is the tally of values stripped out of this body, per class.
func (b Body) Counts() Counts { return b.counts }

// Text runs the secret table over one body and returns the cleaned text with
// its tally. It is pure: the same input always gives the same output, and
// nothing outside this function is touched.
func Text(body string) Body {
	out := Body{text: body}
	for _, rule := range Rules {
		hits := len(rule.Pattern.FindAllStringIndex(out.text, -1))
		if hits == 0 {
			continue
		}
		if out.counts == nil {
			out.counts = make(Counts, len(Rules))
		}
		out.counts[rule.Class] += hits
		out.text = rule.Pattern.ReplaceAllString(out.text, rule.Replacement)
	}
	return out
}
