// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package epoch_test

import (
	"math"
	"runtime"
	"sync"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/epoch"
)

func TestCounter(t *testing.T) {
	t.Parallel()

	t.Run("zero-value Counter starts at Zero, Next returns 1", func(t *testing.T) {
		t.Parallel()
		var c epoch.Counter
		testkit.Equal(t, c.Current(), epoch.Zero,
			"Current on zero-value Counter must equal epoch.Zero")
		testkit.Equal(t, c.Next(), epoch.Epoch(1),
			"first Next on zero-value Counter must return 1")
	})

	t.Run("NewCounter advances from start", func(t *testing.T) {
		t.Parallel()
		c := epoch.NewCounter(100)
		testkit.Equal(t, c.Current(), epoch.Epoch(100),
			"Current after NewCounter(100) must equal 100")
		testkit.Equal(t, c.Next(), epoch.Epoch(101),
			"first Next after NewCounter(100) must return 101")
		testkit.Equal(t, c.Next(), epoch.Epoch(102),
			"second Next must return 102")
	})

	t.Run("Current does not advance the counter", func(t *testing.T) {
		t.Parallel()
		c := epoch.NewCounter(5)
		_ = c.Current()
		_ = c.Current()
		testkit.Equal(t, c.Next(), epoch.Epoch(6),
			"Next after Currents must reflect that Current did not advance")
	})

	t.Run("Next wraps at MaxUint64", func(t *testing.T) {
		t.Parallel()
		// Documented: at MaxUint64 the underlying counter wraps to
		// zero. Unreachable in practice; not guarded.
		c := epoch.NewCounter(math.MaxUint64 - 1)
		testkit.Equal(t, c.Next(), epoch.Epoch(math.MaxUint64),
			"Next at MaxUint64-1 must return MaxUint64")
		testkit.Equal(t, c.Next(), epoch.Zero,
			"Next at MaxUint64 must wrap to Zero")
	})
}

func TestCounterConcurrent(t *testing.T) {
	t.Parallel()

	const goroutines = 16
	const perGoroutine = 1000
	c := epoch.NewCounter(epoch.Zero)

	seen := make(chan epoch.Epoch, goroutines*perGoroutine)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			for range perGoroutine {
				seen <- c.Next()
			}
		}()
	}
	wg.Wait()
	close(seen)

	// Every value 1..goroutines*perGoroutine should appear exactly
	// once. The set of returned epochs is the contiguous range
	// (1, N] with no gaps and no duplicates.
	const total = goroutines * perGoroutine
	hits := make(map[epoch.Epoch]int, total)
	for e := range seen {
		hits[e]++
	}
	testkit.Equal(t, len(hits), total,
		"every Next call must produce a distinct epoch")
	for e := epoch.Epoch(1); e <= total; e++ {
		testkit.Equal(t, hits[e], 1,
			"epoch must appear exactly once across all goroutines")
	}
	testkit.Equal(t, c.Current(), epoch.Epoch(total),
		"final Current must equal total Next calls")
}

// TestCounterZeroAlloc cannot run in parallel —
// testing.AllocsPerRun panics if any other test is running.
//
//nolint:paralleltest // see comment above
func TestCounterZeroAlloc(t *testing.T) {
	c := epoch.NewCounter(epoch.Zero)

	t.Run("Next", func(t *testing.T) {
		testkit.Equal(t, testing.AllocsPerRun(100, func() { _ = c.Next() }),
			float64(0), "Counter.Next must be zero-alloc")
	})

	t.Run("Current", func(t *testing.T) {
		testkit.Equal(t, testing.AllocsPerRun(100, func() { _ = c.Current() }),
			float64(0), "Counter.Current must be zero-alloc")
	})
}

func BenchmarkNext(b *testing.B) {
	c := epoch.NewCounter(epoch.Zero)
	b.ReportAllocs()
	for b.Loop() {
		_ = c.Next()
	}
}

func BenchmarkCurrent(b *testing.B) {
	c := epoch.NewCounter(epoch.Epoch(1))
	b.ReportAllocs()
	var sink epoch.Epoch
	for b.Loop() {
		sink = c.Current()
	}
	runtime.KeepAlive(sink)
}

func BenchmarkNextParallel(b *testing.B) {
	c := epoch.NewCounter(epoch.Zero)
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = c.Next()
		}
	})
}
