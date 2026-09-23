package engine

import (
	"fmt"
	"regexp"
)

// The floors below which a build stops.
const (
	MinAvailableGB = 2.0
	MaxWaiting     = 5
)

// Reading is one health sample, in engine-neutral terms. MemoryVisible false
// (Postgres, to an ordinary role) means memory conditions are skipped rather
// than read as zero free bytes.
type Reading struct {
	MemoryVisible bool
	AvailableGB   float64
	// State is the server's own wording, so it is matched for "low" rather than
	// compared.
	State     string
	MemoryLow bool
	// Waiting is how many queries are queued for a resource they cannot get.
	Waiting int
}

// Stop is one condition that halts a build, with the sentence it halts under.
type Stop struct {
	Name   string
	When   func(Reading) bool
	Reason func(Reading) string
}

var low = regexp.MustCompile(`(?i)low`)

// StopConditions is the whole halt policy, in the order it is reported.
var StopConditions = []Stop{
	{
		Name: "os-memory-floor",
		When: func(r Reading) bool { return r.MemoryVisible && r.AvailableGB < MinAvailableGB },
		Reason: func(r Reading) string {
			return fmt.Sprintf("only %g GB of OS memory free (floor is %g GB)",
				r.AvailableGB, MinAvailableGB)
		},
	},
	{
		Name: "server-reports-low",
		When: func(r Reading) bool { return r.MemoryVisible && low.MatchString(r.State) },
		Reason: func(r Reading) string {
			return fmt.Sprintf("the server reports %q", r.State)
		},
	},
	{
		Name:   "process-memory-low",
		When:   func(r Reading) bool { return r.MemoryLow },
		Reason: func(Reading) string { return "the server signalled physical memory low" },
	},
	{
		Name: "grants-queued",
		When: func(r Reading) bool { return r.Waiting > MaxWaiting },
		Reason: func(r Reading) string {
			return fmt.Sprintf("%d queries are queued waiting for a resource", r.Waiting)
		},
	},
}

// Health is what a build asks before every stage and every batch.
type Health struct {
	// OK is false whenever the answer is not a clear yes.
	OK bool
	// Known is whether the server answered at all: not seeing the floor is not
	// the same as being above it.
	Known   bool
	Reason  string
	Reading Reading
}

// Unreadable is the reason attached to a reading that never arrived.
const Unreadable = "server memory state could not be read"

// Classify turns a reading into a verdict. A nil reading is unknown, never
// healthy.
func Classify(reading *Reading) Health {
	if reading == nil {
		return Health{OK: false, Known: false, Reason: Unreadable}
	}
	for _, stop := range StopConditions {
		if stop.When(*reading) {
			return Health{OK: false, Known: true, Reason: stop.Reason(*reading), Reading: *reading}
		}
	}
	return Health{OK: true, Known: true, Reading: *reading}
}

// UnhealthyError halts a stage before it sends anything.
type UnhealthyError struct {
	Stage  string
	Reason string
	// Known distinguishes a server that said it has no room from one that would
	// not say.
	Known bool
}

func (e *UnhealthyError) Error() string {
	return fmt.Sprintf("halting before %s: %s", e.Stage, e.Reason)
}

// Assert stops a stage unless the server has room. Every caller consumes Health
// through it, so "unknown is not healthy" lives in one place.
func Assert(health Health, stage string) error {
	if health.OK {
		return nil
	}
	return &UnhealthyError{Stage: stage, Reason: health.Reason, Known: health.Known}
}
