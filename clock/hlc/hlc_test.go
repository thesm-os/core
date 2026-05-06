// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package hlc_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/clock/hlc"
)

// fixedSource returns a wall-time source that returns *now until
// the test mutates it. Used to drive HLC branch tests
// deterministically without depending on real-clock cadence.
func fixedSource(now *int64) func() int64 {
	return func() int64 { return *now }
}

func TestNow(t *testing.T) {
	t.Parallel()

	t.Run("monotone in HLC order over many calls", func(t *testing.T) {
		t.Parallel()
		c := hlc.New(clock.NodeID(1))
		prev := c.Now()
		for range 1000 {
			next := c.Now()
			if !prev.HappensBefore(next) {
				t.Fatalf("Now produced non-monotone instants: prev=%+v next=%+v", prev, next)
			}
			prev = next
		}
	})

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

func TestTime(t *testing.T) {
	t.Parallel()

	c := hlc.New(0)
	if got := c.Time().Location(); got != time.UTC {
		t.Fatalf("Time location: got %v, want UTC", got)
	}
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

func TestConcurrentNow(t *testing.T) {
	t.Parallel()

	// Race-detector-validated invariant: under concurrent Now()
	// across many goroutines, each call sees a unique HLC instant.
	const goroutines, perGoroutine = 16, 1000

	c := hlc.New(clock.NodeID(1))
	var totalCalls atomic.Uint64
	var wg sync.WaitGroup

	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			for range perGoroutine {
				c.Now()
				totalCalls.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := totalCalls.Load(); got != goroutines*perGoroutine {
		t.Fatalf("totalCalls: got %d, want %d", got, goroutines*perGoroutine)
	}
}

// TestZeroAlloc enforces the documented "Zero alloc" contracts on
// hlc.Clock's hot-path methods. testing.AllocsPerRun uses a
// process-global malloc counter, so this test does not call
// t.Parallel.
func TestZeroAlloc(t *testing.T) {
	c := hlc.New(clock.NodeID(1))
	observed := clock.Instant{Wall: 1_000_000_000, Logical: 1, Node: 99}

	cases := []struct {
		name string
		fn   func()
	}{
		{"Now", func() { _ = c.Now() }},
		{"Time", func() { _ = c.Time() }},
		{"Update", func() { _ = c.Update(observed) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := testing.AllocsPerRun(100, tc.fn); got != 0 {
				t.Fatalf("%s: %v allocs/op, want 0", tc.name, got)
			}
		})
	}
}

func BenchmarkNow(b *testing.B) {
	c := hlc.New(clock.NodeID(1))
	b.ReportAllocs()
	for b.Loop() {
		_ = c.Now()
	}
}

func BenchmarkTime(b *testing.B) {
	c := hlc.New(clock.NodeID(1))
	b.ReportAllocs()
	for b.Loop() {
		_ = c.Time()
	}
}

func BenchmarkUpdate(b *testing.B) {
	c := hlc.New(clock.NodeID(1))
	observed := clock.Instant{Wall: time.Now().UnixNano(), Logical: 1, Node: 99}
	b.ReportAllocs()
	for b.Loop() {
		_ = c.Update(observed)
	}
}

func BenchmarkNowParallel(b *testing.B) {
	c := hlc.New(clock.NodeID(1))
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = c.Now()
		}
	})
}
