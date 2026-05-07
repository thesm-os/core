// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package fake_test

import (
	"sync"
	"testing"
	"time"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/coretest/clocktest"
	"go.thesmos.sh/testkit/bench"
	"go.thesmos.sh/testkit/model/action"
)

var origin = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func TestFakeClockContract(t *testing.T) {
	t.Parallel()
	clocktest.AssertClockContract(t,
		func() clock.Clock { return fake.New(origin) },
		clocktest.ClockContractAssertions()...,
	)
}

// TestFakeClockModel runs the property-based state-machine model
// against [fake.Clock]. Ref-less SUT-only mode via action.Stress;
// see TestHLCClockModel for rationale.
func TestFakeClockModel(t *testing.T) {
	t.Parallel()
	clocktest.AssertClockModel(t, factoryFake(), modelActionsFake()...)
}

// FuzzFakeClock — coverage-guided fuzz wrapper around the model
// property.
func FuzzFakeClock(f *testing.F) {
	clocktest.FuzzClockModel(f, factoryFake(), modelActionsFake()...)
}

func factoryFake() func() clock.Clock {
	return func() clock.Clock { return fake.New(origin) }
}

func modelActionsFake() []clocktest.ClockModelOption {
	return []clocktest.ClockModelOption{
		clocktest.ClockModelActions(
			action.Stress("Now", func(c clock.Clock) { _ = c.Now() }),
			action.Stress("Time", func(c clock.Clock) { _ = c.Time() }),
			action.Stress("Update", func(c clock.Clock) { _ = c.Update(clock.Instant{}) }),
			action.Stress("NewTimer", func(c clock.Clock) { _ = c.NewTimer(0) }),
		),
	}
}

func TestNow(t *testing.T) {
	t.Parallel()

	t.Run("returns origin Wall on first call", func(t *testing.T) {
		t.Parallel()
		c := fake.New(origin)
		if got := c.Now().Wall; got != origin.UnixNano() {
			t.Fatalf("Wall: got %d, want %d", got, origin.UnixNano())
		}
	})

	t.Run("logical increments per call between advances", func(t *testing.T) {
		t.Parallel()
		c := fake.New(origin)
		first := c.Now()
		second := c.Now()
		if second.Logical != first.Logical+1 {
			t.Fatalf("Logical: got %d, want %d", second.Logical, first.Logical+1)
		}
	})

	t.Run("NewWithNode tags every instant with the configured node", func(t *testing.T) {
		t.Parallel()
		const node = clock.NodeID(7)
		c := fake.NewWithNode(origin, node)
		if got := c.Now().Node; got != node {
			t.Fatalf("Node: got %d, want %d", got, node)
		}
	})
}

func TestTime(t *testing.T) {
	t.Parallel()

	t.Run("returns the configured origin", func(t *testing.T) {
		t.Parallel()
		c := fake.New(origin)
		if got := c.Time(); !got.Equal(origin) {
			t.Fatalf("Time: got %v, want %v", got, origin)
		}
	})

	t.Run("advances the HLC state per call", func(t *testing.T) {
		t.Parallel()
		c := fake.New(origin)
		c.Time() // logical=1
		c.Time() // logical=2
		// Next Now() bumps logical to 3, confirming Time
		// advanced HLC state on each prior call.
		if got := c.Now().Logical; got != 3 {
			t.Fatalf("Logical: got %d, want 3 (Time must advance HLC state per Clock contract)", got)
		}
	})
}

func TestAdvance(t *testing.T) {
	t.Parallel()

	t.Run("moves Wall forward by the given duration", func(t *testing.T) {
		t.Parallel()
		c := fake.New(origin)
		c.Advance(5 * time.Second)
		want := origin.Add(5 * time.Second)
		if got := c.Time(); !got.Equal(want) {
			t.Fatalf("Time: got %v, want %v", got, want)
		}
	})

	t.Run("accumulates across calls", func(t *testing.T) {
		t.Parallel()
		c := fake.New(origin)
		c.Advance(3 * time.Second)
		c.Advance(2 * time.Second)
		want := origin.Add(5 * time.Second)
		if got := c.Time(); !got.Equal(want) {
			t.Fatalf("Time: got %v, want %v", got, want)
		}
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
			if got.Wall != origin.UnixNano() {
				t.Fatalf("Wall: got %d, want %d (d=%v)", got.Wall, origin.UnixNano(), d)
			}
			if got.Logical != 3 {
				t.Fatalf("Logical: got %d, want 3 (d=%v should not reset)", got.Logical, d)
			}
		}
	})

	t.Run("resets logical when wall moves", func(t *testing.T) {
		t.Parallel()
		c := fake.New(origin)
		c.Now()
		c.Now()
		c.Advance(time.Second)
		if got := c.Now().Logical; got != 1 {
			t.Fatalf("Logical: got %d, want 1 after advance", got)
		}
	})
}

func TestSet(t *testing.T) {
	t.Parallel()

	target := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	c := fake.New(origin)
	c.Set(target)
	if got := c.Time(); !got.Equal(target) {
		t.Fatalf("Time: got %v, want %v", got, target)
	}
}

func TestUpdate(t *testing.T) {
	t.Parallel()

	t.Run("local ahead increments local logical", func(t *testing.T) {
		t.Parallel()
		c := fake.New(origin)
		// Observed wall is in 1970; local origin (2026) is ahead.
		got := c.Update(clock.Instant{Wall: 1, Logical: 99, Node: 7})
		if got.Wall != origin.UnixNano() {
			t.Fatalf("Wall: got %d, want %d", got.Wall, origin.UnixNano())
		}
		if got.Logical != 1 {
			t.Fatalf("Logical: got %d, want 1", got.Logical)
		}
	})

	t.Run("observed ahead adopts observed.Logical+1", func(t *testing.T) {
		t.Parallel()
		c := fake.New(origin)
		future := origin.Add(time.Hour).UnixNano()
		got := c.Update(clock.Instant{Wall: future, Logical: 7, Node: 9})
		if got.Wall != future {
			t.Fatalf("Wall: got %d, want %d", got.Wall, future)
		}
		if got.Logical != 8 {
			t.Fatalf("Logical: got %d, want 8", got.Logical)
		}
	})

	t.Run("tied wall, observed logical higher: take observed+1", func(t *testing.T) {
		t.Parallel()
		c := fake.New(origin)
		c.Now() // local logical = 1
		got := c.Update(clock.Instant{Wall: origin.UnixNano(), Logical: 5, Node: 9})
		// max(1, 5) + 1 = 6.
		if got.Logical != 6 {
			t.Fatalf("Logical: got %d, want 6", got.Logical)
		}
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
		if got.Logical != 11 {
			t.Fatalf("Logical: got %d, want 11", got.Logical)
		}
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
	t.Cleanup(func() { c.Advance(2 * time.Hour); wg.Wait() })
	c.AwaitWaiters(n)
}

// BenchmarkFakeClock runs the standard Clock bench contract:
// auto hot-path measurement for Now/Time/Update/NewTimer plus
// PureAllocsWithin(0) gates for the documented zero-alloc methods.
// Replaces the prior hand-rolled BenchmarkNow/Time/Update +
// TestZeroAlloc.
func BenchmarkFakeClock(b *testing.B) {
	clocktest.BenchmarkClockContract(b,
		func() clock.Clock { return fake.New(origin) },
		clocktest.ClockBenchOnNow(bench.PureAllocsWithin[clock.Clock, clock.Instant](0)),
		clocktest.ClockBenchOnTime(bench.PureAllocsWithin[clock.Clock, time.Time](0)),
		clocktest.ClockBenchOnUpdate(bench.PureAllocsWithin[clock.Clock, clock.Instant](0)),
	)
}

// BenchmarkFakeAdvance is fake-specific (Advance is not part of
// the [clock.Clock] interface) so it stays here rather than in the
// generated contract.
func BenchmarkFakeAdvance(b *testing.B) {
	c := fake.New(origin)
	b.ReportAllocs()
	for b.Loop() {
		c.Advance(time.Nanosecond)
	}
}
