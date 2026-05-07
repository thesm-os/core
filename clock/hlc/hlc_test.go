// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package hlc_test

import (
	"testing"
	"time"

	"go.thesmos.sh/testkit/bench"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/clock/hlc"
	"go.thesmos.sh/core/coretest/clocktest"
)

// newClock is the SUT factory shared by every testkit-driven
// entry point: contract suite, model, fuzz target, bench.
func newClock() clock.Clock { return hlc.New(0) }

// --- testkit-driven contract layer ---

func TestHLCClockContract(t *testing.T) {
	t.Parallel()
	clocktest.AssertClockContract(t, newClock, clocktest.ClockContractAssertions()...)
}

// TestHLCClockModel runs the property-based state-machine model
// against [hlc.Clock]. The //testkit:nondeterministic directives
// on Clock.Now/Time/Update auto-emit action.Stress; the model
// framework runs random sequences and catches panics + races
// (with -race).
func TestHLCClockModel(t *testing.T) {
	t.Parallel()
	clocktest.ClockModelTest(t, newClock)
}

// FuzzHLCClockModel drives the same property as
// [TestHLCClockModel] via coverage-guided fuzzing.
func FuzzHLCClockModel(f *testing.F) {
	clocktest.ClockModelFuzz(f, newClock)
}

// BenchmarkHLCClock runs the standard Clock bench contract:
// auto hot-path measurement for Now/Time/Update/NewTimer; per-
// method PureAllocsWithin(0) gates for the documented zero-alloc
// methods; PureConcurrentThroughput on Now (HLC's hottest path)
// to surface contention regressions.
func BenchmarkHLCClock(b *testing.B) {
	clocktest.BenchmarkClockContract(b,
		newClock,
		clocktest.ClockBenchOnNow(
			bench.PureAllocsWithin[clock.Clock, clock.Instant](0),
			bench.PureConcurrentThroughput[clock.Clock, clock.Instant](32),
		),
		clocktest.ClockBenchOnTime(bench.PureAllocsWithin[clock.Clock, time.Time](0)),
		clocktest.ClockBenchOnUpdate(bench.PureAllocsWithin[clock.Clock, clock.Instant](0)),
	)
}

// --- HLC-specific tests ---

func TestNow(t *testing.T) {
	t.Parallel()

	t.Run("tags every instant with the configured node", func(t *testing.T) {
		t.Parallel()
		const node = clock.NodeID(42)
		c := hlc.New(node)
		for range 100 {
			if got := c.Now().Node; got != node {
				t.Fatalf("Node: got %d, want %d", got, node)
			}
		}
	})

	t.Run("logical resets to 1 on wall advance", func(t *testing.T) {
		t.Parallel()
		now := int64(100)
		c := hlc.NewWithSource(clock.NodeID(1), fixedSource(&now))
		// Burn the logical counter at wall=100.
		for range 5 {
			c.Now()
		}
		now = 200
		got := c.Now()
		if got.Wall != 200 {
			t.Fatalf("Wall: got %d, want 200", got.Wall)
		}
		if got.Logical != 1 {
			t.Fatalf("Logical: got %d, want 1 (reset on wall advance)", got.Logical)
		}
	})

	t.Run("logical increments when wall does not advance", func(t *testing.T) {
		t.Parallel()
		now := int64(100)
		c := hlc.NewWithSource(clock.NodeID(1), fixedSource(&now))
		first := c.Now()
		second := c.Now()
		if first.Wall != second.Wall {
			t.Fatalf("Wall changed unexpectedly: %d → %d", first.Wall, second.Wall)
		}
		if second.Logical != first.Logical+1 {
			t.Fatalf("Logical: got %d, want %d", second.Logical, first.Logical+1)
		}
	})
}

func TestUpdate(t *testing.T) {
	t.Parallel()

	type scenario struct {
		name         string
		startWall    int64
		physicalWall int64 // wall-time source value at Update
		observedWall int64
		wantWall     int64
		startNows    int // Now() calls before Update, to set local state
		observedLog  uint32
		wantLogical  uint32
	}
	cases := []scenario{
		{
			name:         "observed peer ahead adopts observed.Logical+1",
			startWall:    100,
			startNows:    1, // local: wall=100, logical=1
			physicalWall: 100,
			observedWall: 200,
			observedLog:  7,
			wantWall:     200,
			wantLogical:  8,
		},
		{
			name:         "local ahead increments local logical",
			startWall:    200,
			startNows:    2, // local: wall=200, logical=2
			physicalWall: 200,
			observedWall: 100,
			observedLog:  5,
			wantWall:     200,
			wantLogical:  3,
		},
		{
			name:         "tied wall takes max(local, observed)+1",
			startWall:    100,
			startNows:    2, // local: wall=100, logical=2
			physicalWall: 100,
			observedWall: 100,
			observedLog:  5,
			wantWall:     100,
			wantLogical:  6,
		},
		{
			name:         "physical ahead of both resets logical to 1",
			startWall:    50,
			startNows:    1, // local: wall=50, logical=1
			physicalWall: 300,
			observedWall: 100,
			observedLog:  5,
			wantWall:     300,
			wantLogical:  1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			now := tc.startWall
			c := hlc.NewWithSource(clock.NodeID(1), fixedSource(&now))
			for range tc.startNows {
				c.Now()
			}
			now = tc.physicalWall
			got := c.Update(clock.Instant{Wall: tc.observedWall, Logical: tc.observedLog, Node: 99})
			if got.Wall != tc.wantWall {
				t.Fatalf("Wall: got %d, want %d", got.Wall, tc.wantWall)
			}
			if got.Logical != tc.wantLogical {
				t.Fatalf("Logical: got %d, want %d", got.Logical, tc.wantLogical)
			}
			if got.Node != clock.NodeID(1) {
				t.Fatalf("Node: got %d, want 1", got.Node)
			}
		})
	}
}

// --- helpers ---

// fixedSource returns a wall-time source that returns *now until
// the test mutates it. Used to drive HLC branch tests
// deterministically without depending on real-clock cadence.
func fixedSource(now *int64) func() int64 {
	return func() int64 { return *now }
}
