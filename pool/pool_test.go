// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package pool_test

import (
	"sync"
	"sync/atomic"
	"testing"

	"go.thesmos.sh/core/pool"
)

func TestPool(t *testing.T) {
	t.Parallel()

	t.Run("Get on empty pool calls newFn", func(t *testing.T) {
		t.Parallel()
		var calls atomic.Int32
		p := pool.NewPool(func() *int {
			calls.Add(1)
			v := 42
			return &v
		})
		v := p.Get()
		if calls.Load() != 1 {
			t.Fatalf("newFn calls: got %d, want 1", calls.Load())
		}
		if *v != 42 {
			t.Fatalf("Get: got %d, want 42", *v)
		}
	})

	t.Run("Put then Get returns the same value when cached", func(t *testing.T) {
		t.Parallel()
		p := pool.NewPool(func() *int {
			v := 0
			return &v
		})
		original := p.Get()
		*original = 99
		p.Put(original)

		got := p.Get()
		// sync.Pool may evict under GC pressure; the only
		// guarantee is that Get may return a previously-Put
		// value. We test that *if* we get the same pointer
		// back, the state is preserved.
		if got == original && *got != 99 {
			t.Fatalf("preserved value: got %d, want 99", *got)
		}
	})

	t.Run("Get is type-safe (no any cast at call site)", func(t *testing.T) {
		t.Parallel()
		p := pool.NewPool(func() string { return "hello" })
		// Get returns string, not any — the assignment to a
		// typed variable would fail to compile if the signature
		// drifted to any.
		s := p.Get()
		if s != "hello" {
			t.Fatalf("Get: got %q, want hello", s)
		}
	})

	t.Run("Put is best-effort (no panic on full pool)", func(t *testing.T) {
		t.Parallel()
		p := pool.NewPool(func() int { return 0 })
		// Repeated Puts must not panic regardless of pool state.
		for i := range 100 {
			p.Put(i)
		}
	})

	t.Run("concurrent Get/Put is safe", func(t *testing.T) {
		t.Parallel()
		p := pool.NewPool(func() *int {
			v := 0
			return &v
		})
		const goroutines = 16
		const iterations = 1000
		var wg sync.WaitGroup
		wg.Add(goroutines)
		for range goroutines {
			go func() {
				defer wg.Done()
				for range iterations {
					v := p.Get()
					*v++
					p.Put(v)
				}
			}()
		}
		wg.Wait()
	})
}

// TestPoolZeroAlloc cannot run in parallel —
// testing.AllocsPerRun panics if any other test is running.
//
//nolint:paralleltest // see comment above
func TestPoolZeroAlloc(t *testing.T) {
	p := pool.NewPool(func() *int {
		v := 0
		return &v
	})
	// Warm the pool so Get hits the cache.
	v := p.Get()
	p.Put(v)

	t.Run("Get on warm pool", func(t *testing.T) {
		// Maintain the invariant: Put before each Get so the
		// pool is always warm for the next iteration.
		if got := testing.AllocsPerRun(100, func() {
			v := p.Get()
			p.Put(v)
		}); got != 0 {
			t.Fatalf("Get+Put cycle: %v allocs/op, want 0", got)
		}
	})
}

func BenchmarkPool(b *testing.B) {
	p := pool.NewPool(func() *resettable { return new(resettable) })
	p.Put(p.Get()) // warm
	b.ReportAllocs()
	for b.Loop() {
		v := p.Get()
		p.Put(v)
	}
}
