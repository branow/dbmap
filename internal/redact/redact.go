// Package redact keeps sensitive values out of the index. It holds two rule
// tables and the engines that walk them: one strips secrets out of a module
// body, the other names the columns a sample must never read.
//
// Both are pure and stand alone — no filesystem, no database, no clock — so a
// body can be redacted the moment it arrives, before it is written to the cache
// and long before it reaches a prompt.
//
// Nothing here throws away what it found: Text reports a count per class, so a
// build summary can surface a real credential rather than swallow it.
package redact

import "sort"

// Counts is how many values of each class a pass stripped. A map, so a class
// that never fired is absent and a summary lists only what was found.
type Counts map[Class]int

// Total is every hit across every class.
func (c Counts) Total() int {
	total := 0
	for _, n := range c {
		total += n
	}
	return total
}

// Classes are the classes that fired, in a stable order.
func (c Counts) Classes() []Class {
	classes := make([]Class, 0, len(c))
	for class := range c {
		classes = append(classes, class)
	}
	sort.Slice(classes, func(i, j int) bool { return classes[i] < classes[j] })
	return classes
}

// Add returns the sum of two tallies, leaving both operands untouched.
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

// Body is text that has been through the redactor, plus the tally of what came
// out. Its text is unexported and Text is its only constructor, so a Body
// cannot hold anything the rules did not see — which is what lets the cache
// demand one and be structurally unable to store a raw body.
type Body struct {
	text   string
	counts Counts
}

// String is the redacted text.
func (b Body) String() string { return b.text }

// Counts is the tally of values stripped out of this body, per class.
func (b Body) Counts() Counts { return b.counts }

// Text runs the secret table over one body and returns the cleaned text with
// its tally.
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
