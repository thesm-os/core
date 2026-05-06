// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package epoch_test

import (
	"math"
	"sync"
	"testing"

	"go.thesmos.sh/core/epoch"
)

func TestCounter(t *testing.T) {
	t.Parallel()

	t.Run("zero-value Counter starts at Zero, Next returns 1", func(t *testing.T) {
		t.Parallel()
		var c epoch.Counter
		if got := c.Current(); got != epoch.Zero {
			t.Fatalf("Current on zero-value: got %d, want Zero", got)
		}
		if got := c.Next(); got != 1 {
			t.Fatalf("first Next on zero-value: got %d, want 1", got)
		}
	})

	t.Run("NewCounter advances from start", func(t *testing.T) {
		t.Parallel()
		c := epoch.NewCounter(100)
		if got := c.Current(); got != 100 {
			t.Fatalf("Current after NewCounter(100): got %d, want 100", got)
		}
		if got := c.Next(); got != 101 {
			t.Fatalf("Next after NewCounter(100): got %d, want 101", got)
		}
		if got := c.Next(); got != 102 {
			t.Fatalf("second Next: got %d, want 102", got)
		}
	})

	t.Run("Current does not advance the counter", func(t *testing.T) {
		t.Parallel()
		c := epoch.NewCounter(5)
		_ = c.Current()
		_ = c.Current()
		if got := c.Next(); got != 6 {
			t.Fatalf("Next after two Currents: got %d, want 6", got)
		}
	})

	t.Run("Next wraps at MaxUint64", func(t *testing.T) {
		t.Parallel()
		// Documented: at MaxUint64 the underlying counter wraps to
		// zero. Unreachable in practice; not guarded.
		c := epoch.NewCounter(math.MaxUint64 - 1)
		if got := c.Next(); got != math.MaxUint64 {
			t.Fatalf("Next at MaxUint64-1: got %d, want MaxUint64", got)
		}
		if got := c.Next(); got != epoch.Zero {
			t.Fatalf("Next at MaxUint64: got %d, want Zero (wrap)", got)
		}
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
	if got := len(hits); got != total {
		t.Fatalf("distinct epochs: got %d, want %d", got, total)
	}
	for e := epoch.Epoch(1); e <= total; e++ {
		if hits[e] != 1 {
			t.Fatalf("epoch %d: got %d occurrences, want 1", e, hits[e])
		}
	}
	if got := c.Current(); got != total {
		t.Fatalf("final Current: got %d, want %d", got, total)
	}
}

// TestCounterZeroAlloc cannot run in parallel —
// testing.AllocsPerRun panics if any other test is running.
//
//nolint:paralleltest // see comment above
func TestCounterZeroAlloc(t *testing.T) {
	c := epoch.NewCounter(epoch.Zero)

	t.Run("Next", func(t *testing.T) {
		if got := testing.AllocsPerRun(100, func() { _ = c.Next() }); got != 0 {
			t.Fatalf("Counter.Next: %v allocs/op, want 0", got)
		}
	})

	t.Run("Current", func(t *testing.T) {
		if got := testing.AllocsPerRun(100, func() { _ = c.Current() }); got != 0 {
			t.Fatalf("Counter.Current: %v allocs/op, want 0", got)
		}
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
	for b.Loop() {
		_ = c.Current()
	}
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
