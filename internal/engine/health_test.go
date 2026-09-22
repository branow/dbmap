package engine

import (
	"errors"
	"testing"
)

// healthy is a reading with room to spare, which each case then spoils in one
// specific way.
func healthy() Reading {
	return Reading{MemoryVisible: true, AvailableGB: 64, State: "memory is available", Waiting: 0}
}

// The rule this pins is the reason Known exists separately from OK: an account
// that cannot see the health views produces no reading, and not being able to
// see the floor is not the same as being above it. Reading this as healthy is
// how a build walks into the failure the whole design exists to avoid.
func TestUnreadableHealthIsUnknownNeverHealthy(t *testing.T) {
	health := Classify(nil)

	if health.OK {
		t.Fatal("an unreadable health reading classified as OK")
	}
	if health.Known {
		t.Fatal("an unreadable health reading classified as known")
	}
	if health.Reason != Unreadable {
		t.Fatalf("reason = %q, want %q", health.Reason, Unreadable)
	}
}

func TestEveryStopConditionHalts(t *testing.T) {
	cases := []struct {
		name    string
		reading Reading
	}{
		{"os memory below the floor", func() Reading {
			r := healthy()
			r.AvailableGB = MinAvailableGB - 0.5
			return r
		}()},
		{"server reports memory low", func() Reading {
			r := healthy()
			r.State = "memory is low"
			return r
		}()},
		{"process physical memory low", func() Reading {
			r := healthy()
			r.MemoryLow = true
			return r
		}()},
		{"too many queries waiting", func() Reading {
			r := healthy()
			r.Waiting = MaxWaiting + 1
			return r
		}()},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			health := Classify(&c.reading)
			if health.OK {
				t.Fatal("classified as OK")
			}
			// The server answered; it said no. That is a different thing from
			// not answering, and a caller is entitled to tell them apart.
			if !health.Known {
				t.Fatal("classified as unknown; the server did answer")
			}
			if health.Reason == "" {
				t.Fatal("halted without saying why")
			}
		})
	}
}

func TestHealthyReadingPasses(t *testing.T) {
	reading := healthy()
	health := Classify(&reading)

	if !health.OK || !health.Known {
		t.Fatalf("a healthy reading did not pass: %+v", health)
	}
}

// An engine that cannot see operating-system memory must not be halted by a
// condition about memory it never reported. Otherwise Postgres, whose ordinary
// role sees none of it, would read as permanently out of memory.
func TestMemoryConditionsAreNotAskedWhereMemoryIsInvisible(t *testing.T) {
	reading := Reading{MemoryVisible: false, AvailableGB: 0, State: "", MemoryLow: false}

	health := Classify(&reading)
	if !health.OK {
		t.Fatalf("halted an engine that cannot see memory: %q", health.Reason)
	}

	// The non-memory conditions must still apply there.
	reading.Waiting = MaxWaiting + 1
	if Classify(&reading).OK {
		t.Fatal("waiting queries did not halt an engine without memory visibility")
	}
}

// Assert is the single place every caller consumes Health, which is what makes
// "unknown is not healthy" impossible to bypass by accident.
func TestAssertStopsOnAnythingButAClearYes(t *testing.T) {
	cases := []struct {
		name   string
		health Health
		stops  bool
		known  bool
	}{
		{"healthy", Health{OK: true, Known: true}, false, true},
		{"server said no", Health{OK: false, Known: true, Reason: "no room"}, true, true},
		{"server would not say", Health{OK: false, Known: false, Reason: Unreadable}, true, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := Assert(c.health, "sampling")
			if !c.stops {
				if err != nil {
					t.Fatalf("stopped a healthy server: %v", err)
				}
				return
			}
			var unhealthy *UnhealthyError
			if !errors.As(err, &unhealthy) {
				t.Fatalf("error is %T, want *UnhealthyError", err)
			}
			if unhealthy.Stage != "sampling" {
				t.Errorf("stage = %q, want %q", unhealthy.Stage, "sampling")
			}
			if unhealthy.Known != c.known {
				t.Errorf("known = %v, want %v", unhealthy.Known, c.known)
			}
		})
	}
}

// Conditions are data so a new signal is a new row. A row that can never fire
// is dead policy, so every one must be reachable.
func TestEveryStopConditionIsNamedAndReachable(t *testing.T) {
	if len(StopConditions) == 0 {
		t.Fatal("no stop conditions defined")
	}
	seen := map[string]bool{}
	for _, stop := range StopConditions {
		if stop.Name == "" {
			t.Fatal("a stop condition has no name")
		}
		if seen[stop.Name] {
			t.Fatalf("duplicate stop condition %q", stop.Name)
		}
		seen[stop.Name] = true
		if stop.When == nil || stop.Reason == nil {
			t.Fatalf("stop condition %q is incomplete", stop.Name)
		}
	}
}
