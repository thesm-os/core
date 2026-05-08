// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package pool_test

import (
	"sync"
	"sync/atomic"
	"testing"

	"go.thesmos.sh/testkit"

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
		testkit.Equal(t, calls.Load(), int32(1), "Get on empty pool must call newFn exactly once")
		testkit.Equal(t, *v, 42, "Get must return the newFn-created value")
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
		if got == original {
			testkit.Equal(t, *got, 99,
				"if Get returns the same pointer, the value must be preserved")
		}
	})

	t.Run("Get is type-safe (no any cast at call site)", func(t *testing.T) {
		t.Parallel()
		p := pool.NewPool(func() string { return "hello" })
		// Get returns string, not any — the assignment to a
		// typed variable would fail to compile if the signature
		// drifted to any.
		testkit.Equal(t, p.Get(), "hello", "typed Get must return the typed value")
	})

	t.Run("Put is best-effort (no panic on full pool)", func(t *testing.T) {
		t.Parallel()
		p := pool.NewPool(func() int { return 0 })
		// Repeated Puts must not panic regardless of pool state.
		testkit.AssertNilSafe(t, func() {
			for i := range 100 {
				p.Put(i)
			}
		})
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
		testkit.Equal(t, testing.AllocsPerRun(100, func() {
			v := p.Get()
			p.Put(v)
		}), float64(0), "Get+Put cycle on warm pool must be zero-alloc")
	})
}

// BenchmarkPool exercises the typed [pool.Pool] Get/Put cycle —
// the same pattern HMAC, [rand/crypto.Rand.Uint64], and other
// hot-path consumers in core use. Sub-benchmarks: sequential is
// the steady-state single-goroutine cost; parallel exercises
// per-P caching under fan-out.
func BenchmarkPool(b *testing.B) {
	p := pool.NewPool(func() *resettable { return new(resettable) })
	p.Put(p.Get()) // warm

	b.Run("sequential", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			v := p.Get()
			p.Put(v)
		}
	})

	b.Run("parallel", func(b *testing.B) {
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				v := p.Get()
				p.Put(v)
			}
		})
	})
}
