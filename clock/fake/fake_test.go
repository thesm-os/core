// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package fake_test

import (
	"sync"
	"testing"
	"time"

	"go.thesmos.sh/testkit"
	"go.thesmos.sh/testkit/bench"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/coretest/clocktest"
)

// origin is the wall-time anchor for fake.Clock instances in
// these tests. 2026-01-01 UTC matches the project's reference
// year so failure messages line up visually with the codebase.
var origin = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// newClock is the SUT factory shared by every testkit-driven
// entry point: contract suite, model, fuzz target, bench.
func newClock() clock.Clock { return fake.New(origin) }

// --- testkit-driven contract layer ---

func TestFakeClockContract(t *testing.T) {
	t.Parallel()
	clocktest.AssertClockContract(t, newClock, clocktest.ClockContractAssertions()...)
}

// TestFakeClockModel — see TestHLCClockModel (clock/hlc); auto-
// emitted action.Stress via //testkit:nondeterministic directives
// on Clock.Now/Time/Update.
func TestFakeClockModel(t *testing.T) {
	t.Parallel()
	clocktest.ClockModelTest(t, newClock)
}

// FuzzFakeClockModel — same property as [TestFakeClockModel]
// via coverage-guided fuzzing.
func FuzzFakeClockModel(f *testing.F) {
	clocktest.ClockModelFuzz(f, newClock)
}

// BenchmarkFakeClock runs the standard Clock bench contract:
// auto hot-path measurement for Now/Time/Update/NewTimer plus
// PureAllocsWithin(0) gates for the documented zero-alloc
// methods.
func BenchmarkFakeClock(b *testing.B) {
	clocktest.BenchmarkClockContract(b,
		newClock,
		clocktest.ClockBenchOnNow(bench.PureAllocsWithin[clock.Clock, clock.Instant](0)),
		clocktest.ClockBenchOnTime(bench.PureAllocsWithin[clock.Clock, time.Time](0)),
		clocktest.ClockBenchOnUpdate(bench.PureAllocsWithin[clock.Clock, clock.Instant](0)),
	)
}

// --- fake-specific tests ---

func TestNow(t *testing.T) {
	t.Parallel()

	t.Run("returns origin Wall on first call", func(t *testing.T) {
		t.Parallel()
		c := fake.New(origin)
		testkit.Equal(t, c.Now().Wall, origin.UnixNano(),
			"first Now().Wall must equal origin")
	})

	t.Run("logical increments per call between advances", func(t *testing.T) {
		t.Parallel()
		c := fake.New(origin)
		first := c.Now()
		second := c.Now()
		testkit.Equal(t, second.Logical, first.Logical+1,
			"Logical must increment by exactly 1 per Now call")
	})

	t.Run("NewWithNode tags every instant with the configured node", func(t *testing.T) {
		t.Parallel()
		const node = clock.NodeID(7)
		c := fake.NewWithNode(origin, node)
		testkit.Equal(t, c.Now().Node, node, "Node must equal the configured value")
	})
}

func TestTime(t *testing.T) {
	t.Parallel()

	t.Run("returns the configured origin", func(t *testing.T) {
		t.Parallel()
		c := fake.New(origin)
		testkit.True(t, c.Time().Equal(origin), "Time() must equal the configured origin")
	})

	t.Run("advances the HLC state per call", func(t *testing.T) {
		t.Parallel()
		c := fake.New(origin)
		c.Time() // logical=1
		c.Time() // logical=2
		// Next Now() bumps logical to 3, confirming Time
		// advanced HLC state on each prior call.
		testkit.Equal(t, c.Now().Logical, uint32(3),
			"Logical must equal 3 after two Time() calls — Time must advance HLC state per Clock contract")
	})
}

func TestAdvance(t *testing.T) {
	t.Parallel()

	t.Run("moves Wall forward by the given duration", func(t *testing.T) {
		t.Parallel()
		c := fake.New(origin)
		c.Advance(5 * time.Second)
		want := origin.Add(5 * time.Second)
		testkit.True(t, c.Time().Equal(want), "Time() must equal origin + 5s")
	})

	t.Run("accumulates across calls", func(t *testing.T) {
		t.Parallel()
		c := fake.New(origin)
		c.Advance(3 * time.Second)
		c.Advance(2 * time.Second)
		want := origin.Add(5 * time.Second)
		testkit.True(t, c.Time().Equal(want),
			"Time() must equal origin + 5s after accumulated Advance calls")
	})

	t.Run("non-positive duration leaves time and logical unchanged", func(t *testing.T) {
		t.Parallel()
		// Logical state must survive a no-op Advance — verifying
		// time alone is insufficient because a `c.now.Add(0)` is
		// still a no-op on Wall but resets logical via the
		// fall-through path.
		for _, d := range []time.Duration{0, -time.Second} {
			c := fake.New(origin)
			c.Now() // logical=1
			c.Now() // logical=2
			c.Advance(d)
			got := c.Now() // logical=3 if Advance was a no-op
			testkit.Equal(t, got.Wall, origin.UnixNano(),
				"Wall must equal origin after non-positive Advance")
			testkit.Equal(t, got.Logical, uint32(3),
				"Logical must not reset after non-positive Advance")
		}
	})

	t.Run("resets logical when wall moves", func(t *testing.T) {
		t.Parallel()
		c := fake.New(origin)
		c.Now()
		c.Now()
		c.Advance(time.Second)
		testkit.Equal(t, c.Now().Logical, uint32(1),
			"Logical must reset to 1 after Wall advances")
	})
}

func TestSet(t *testing.T) {
	t.Parallel()

	target := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	c := fake.New(origin)
	c.Set(target)
	testkit.True(t, c.Time().Equal(target), "Time() must equal the Set target")
}

func TestUpdate(t *testing.T) {
	t.Parallel()

	t.Run("local ahead increments local logical", func(t *testing.T) {
		t.Parallel()
		c := fake.New(origin)
		// Observed wall is in 1970; local origin (2026) is ahead.
		got := c.Update(clock.Instant{Wall: 1, Logical: 99, Node: 7})
		testkit.Equal(t, got.Wall, origin.UnixNano(),
			"Wall must remain at origin (local ahead of observed)")
		testkit.Equal(t, got.Logical, uint32(1),
			"Logical must equal 1 (local fresh, observed-Logical ignored when local Wall ahead)")
	})

	t.Run("observed ahead adopts observed.Logical+1", func(t *testing.T) {
		t.Parallel()
		c := fake.New(origin)
		future := origin.Add(time.Hour).UnixNano()
		got := c.Update(clock.Instant{Wall: future, Logical: 7, Node: 9})
		testkit.Equal(t, got.Wall, future, "Wall must adopt observed.Wall when ahead")
		testkit.Equal(t, got.Logical, uint32(8), "Logical must be observed.Logical+1")
	})

	t.Run("tied wall, observed logical higher: take observed+1", func(t *testing.T) {
		t.Parallel()
		c := fake.New(origin)
		c.Now() // local logical = 1
		got := c.Update(clock.Instant{Wall: origin.UnixNano(), Logical: 5, Node: 9})
		// max(1, 5) + 1 = 6.
		testkit.Equal(t, got.Logical, uint32(6),
			"Logical must equal max(local=1, observed=5)+1")
	})

	t.Run("tied wall, local logical higher: take local+1", func(t *testing.T) {
		t.Parallel()
		c := fake.New(origin)
		// Burn local logical past observed to exercise the
		// max(local, observed) branch where local wins.
		for range 10 {
			c.Now()
		}
		got := c.Update(clock.Instant{Wall: origin.UnixNano(), Logical: 3, Node: 9})
		// max(10, 3) + 1 = 11.
		testkit.Equal(t, got.Logical, uint32(11),
			"Logical must equal max(local=10, observed=3)+1")
	})
}

func TestAwaitWaiters(t *testing.T) {
	t.Parallel()

	// Three goroutines block in Sleep; AwaitWaiters returns once
	// all three have registered, eliminating the test's
	// dependency on real-time pacing.
	const n = 3
	c := fake.New(origin)
	var wg sync.WaitGroup
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			clock.Sleep(c, time.Hour)
		}()
	}
	// Bounded rather than a bare wg.Wait(). The cleanup exists to
	// release the sleepers, so it only blocks at all when something
	// upstream is already broken — and an unbounded wait there turns
	// that into the whole binary hanging until `go test -timeout`,
	// reported against whichever test was running at the time.
	//
	// The interval is a watchdog, not a synchronisation budget:
	// Advance wakes every due waiter before it returns, so the
	// WaitGroup is already released by the time this runs.
	t.Cleanup(func() {
		c.Advance(2 * time.Hour)

		drained := make(chan struct{})
		go func() {
			wg.Wait()
			close(drained)
		}()

		const settle = 5 * time.Second

		select {
		case <-drained:
		case <-time.After(settle):
			t.Errorf("sleepers still blocked %s after advancing past every deadline",
				settle)
		}
	})
	c.AwaitWaiters(n)
}

// BenchmarkFakeAdvance is fake-specific — Advance is not part of
// the [clock.Clock] interface, so it stays here rather than in
// the generated bench contract.
func BenchmarkFakeAdvance(b *testing.B) {
	c := fake.New(origin)
	b.ReportAllocs()
	for b.Loop() {
		c.Advance(time.Nanosecond)
	}
}
