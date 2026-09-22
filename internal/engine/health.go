package engine

import (
	"fmt"
	"regexp"
)

// The floors. MinAvailableGB suits the measured server — 125 GB physical with
// max server memory at 119 GB, leaving the operating system roughly 6 GB for
// its network stack, its availability-group threads and its cluster service. A
// laptop wants a different number, which is why this is a variable a future
// config layer can set rather than a constant compiled into a decision.
const (
	MinAvailableGB = 2.0
	MaxWaiting     = 5
)

// Reading is one health sample, in engine-neutral terms. Not every engine can
// see every field: SQL Server exposes operating-system memory through its own
// DMVs, while Postgres exposes none of it to an ordinary role. MemoryVisible
// says which, so a condition about memory is simply not asked where memory
// cannot be seen — rather than reading zero free bytes and halting a build that
// was never in danger.
type Reading struct {
	MemoryVisible bool
	AvailableGB   float64
	// State is the server's own word for its memory situation, matched for
	// "low" rather than compared, because the wording is the server's.
	State     string
	MemoryLow bool
	// Waiting is how many queries are queued for a resource they cannot get:
	// a memory grant on SQL Server, a lock on Postgres.
	Waiting int
}

// Stop is one condition that halts a build, with the sentence it halts under.
// Conditions are data, walked in order, so a new signal is a new row rather
// than another branch in a chain of ifs.
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
	// OK is whether the next batch may be sent. It is false whenever the answer
	// is not a clear yes.
	OK bool
	// Known is whether the server answered at all. An account that cannot see
	// the health views produces Known false, which is reported as unknown and
	// never as healthy: not being able to see the floor is not the same as
	// being above it.
	Known   bool
	Reason  string
	Reading Reading
}

// Unreadable is the reason attached to a reading that never arrived.
const Unreadable = "server memory state could not be read"

// Classify turns a reading into a verdict. A nil reading — the account cannot
// see the views, or the query failed — is unknown, never healthy. That rule is
// the whole point of separating Known from OK.
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

// UnhealthyError halts a stage before it sends anything. The stage and the
// reason are fields, so a caller can report them without parsing a sentence.
type UnhealthyError struct {
	Stage  string
	Reason string
	// Known distinguishes a server that said it has no room from one that would
	// not say. Both stop the build; only the first is the server's decision.
	Known bool
}

func (e *UnhealthyError) Error() string {
	return fmt.Sprintf("halting before %s: %s", e.Stage, e.Reason)
}

// Assert stops a stage unless the server has room. It is how every caller
// consumes Health, so "unknown is not healthy" is enforced in one place.
func Assert(health Health, stage string) error {
	if health.OK {
		return nil
	}
	return &UnhealthyError{Stage: stage, Reason: health.Reason, Known: health.Known}
}
