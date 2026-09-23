// Package redact keeps sensitive values out of the index: one rule table strips
// secrets out of a module body, the other names the columns a sample must never
// read. Both are pure, so a body is redacted the moment it arrives.
package redact

import "sort"

// Counts is how many values of each class a pass stripped; a class that never
// fired is absent.
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

// Add returns the sum of two tallies, leaving both untouched.
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

// Body is redacted text plus the tally of what came out. Text is its only
// constructor, so demanding a Body is how the cache cannot store a raw one.
type Body struct {
	text   string
	counts Counts
}

// String is the redacted text.
func (b Body) String() string { return b.text }

// Counts is the tally of values stripped out of this body, per class.
func (b Body) Counts() Counts { return b.counts }

// Text runs the secret table over one body.
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
