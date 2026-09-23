// Package plan turns a fresh manifest plus the previously written index into
// the two work sets a build runs: Fetch, decided by modify signal, and
// Describe, decided by content fingerprint. Both are pure; the fetch stage runs
// between them.
package plan

import (
	"sort"

	"github.com/branow/dbmap/internal/catalog"
	"github.com/branow/dbmap/internal/fingerprint"
)

// Reason records why an object is being refetched.
type Reason string

const (
	// New is an object the previous build never saw.
	New Reason = "new"
	// Modified covers a moved modify signal and an absent one: an object with
	// no signal can never be proven untouched, so it always refetches.
	Modified Reason = "modified"
	// Sample is a table whose row count crossed the depth the describer sees.
	Sample Reason = "sample"
)

// Rule is one row of the fetch table.
type Rule struct {
	Reason Reason
	// Kinds scopes a rule to particular kinds; nil means every kind.
	Kinds []catalog.Kind
	// When is asked only after every earlier rule declined, so every rule but
	// the first may assume prior is non-nil.
	When func(object catalog.Object, prior *catalog.State) bool
}

// FetchReasons is the fetch decision as data, in the order it is asked; the
// first rule that applies and fires names the reason.
var FetchReasons = []Rule{
	{
		Reason: New,
		When:   func(_ catalog.Object, prior *catalog.State) bool { return prior == nil },
	},
	{
		Reason: Modified,
		When: func(object catalog.Object, prior *catalog.State) bool {
			return !object.Modified.Same(prior.Modified)
		},
	},
	{
		Reason: Sample,
		Kinds:  []catalog.Kind{catalog.Table},
		When: func(object catalog.Object, prior *catalog.State) bool {
			return fingerprint.SampleDepth(prior.Rows) != fingerprint.SampleDepth(object.Rows)
		},
	},
}

func (r Rule) applies(object catalog.Object) bool {
	if r.Kinds == nil {
		return true
	}
	for _, kind := range r.Kinds {
		if kind == object.Kind {
			return true
		}
	}
	return false
}

// FetchPlan is what the fetch stage is asked to do, and what it may skip.
type FetchPlan struct {
	Fetch []catalog.Object
	Reuse []catalog.Object
	// Dropped are keys the database no longer has, sorted, so the writer can
	// remove their rows.
	Dropped []string
	// Reasons maps an object key to why it is being fetched.
	Reasons map[string]Reason
}

// Fetch decides what to pull from the database, given the state the previous
// build left in the index it wrote.
func Fetch(objects []catalog.Object, state map[string]catalog.State) FetchPlan {
	plan := FetchPlan{Reasons: make(map[string]Reason)}
	seen := make(map[string]struct{}, len(objects))

	for _, object := range objects {
		key := object.Key()
		seen[key] = struct{}{}
		var prior *catalog.State
		if recorded, ok := state[key]; ok {
			prior = &recorded
		}
		if reason, hit := reasonFor(object, prior); hit {
			plan.Reasons[key] = reason
			plan.Fetch = append(plan.Fetch, object)
			continue
		}
		plan.Reuse = append(plan.Reuse, object)
	}

	for key := range state {
		if _, ok := seen[key]; !ok {
			plan.Dropped = append(plan.Dropped, key)
		}
	}
	sort.Strings(plan.Dropped)
	return plan
}

func reasonFor(object catalog.Object, prior *catalog.State) (Reason, bool) {
	for _, rule := range FetchReasons {
		if rule.applies(object) && rule.When(object, prior) {
			return rule.Reason, true
		}
	}
	return "", false
}

// DescribePlan is what the model is asked to look at, and what it is not.
type DescribePlan struct {
	Describe []catalog.Entry
	// Unchanged keep the description already written for them.
	Unchanged []catalog.Entry
}

// Describe splits freshly fetched entries by content fingerprint, each returned
// carrying the hash just computed. An entry whose hash matches but has no prior
// description to reuse is still described.
func Describe(fetched []catalog.Entry, state map[string]catalog.State) (DescribePlan, error) {
	var plan DescribePlan

	for _, entry := range fetched {
		current, err := fingerprint.Of(entry.Object, entry.Structure)
		if err != nil {
			return DescribePlan{}, err
		}
		entry.Fingerprint = current

		prior, ok := state[entry.Key()]
		if ok && prior.Fingerprint == current && prior.Description != "" {
			entry.Description = prior.Description
			plan.Unchanged = append(plan.Unchanged, entry)
			continue
		}
		plan.Describe = append(plan.Describe, entry)
	}

	return plan, nil
}

// Describable keeps only the entries whose kind earns a generated sentence.
func Describable(entries []catalog.Entry) []catalog.Entry {
	var kept []catalog.Entry
	for _, entry := range entries {
		if entry.Object.Describable() {
			kept = append(kept, entry)
		}
	}
	return kept
}

// Summary is the one-line shape of a plan, for a build's progress output.
type Summary struct {
	Fetch     int
	Reuse     int
	Dropped   int
	Describe  int
	Unchanged int
}

// Summarize counts both plans together.
func Summarize(fetch FetchPlan, describe DescribePlan) Summary {
	return Summary{
		Fetch:     len(fetch.Fetch),
		Reuse:     len(fetch.Reuse),
		Dropped:   len(fetch.Dropped),
		Describe:  len(describe.Describe),
		Unchanged: len(describe.Unchanged),
	}
}
