// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package pool_test

import (
	"sync"
	"sync/atomic"
	"testing"

	"go.thesmos.sh/core/pool"
)

// resettable is a test helper that satisfies [pool.Resettable]
// while tracking how many times Reset has been called.
type resettable struct {
	value      int
	resetCalls atomic.Int32
}

func (r *resettable) Reset() {
	r.value = 0
	r.resetCalls.Add(1)
}

func TestResetPool(t *testing.T) {
	t.Parallel()

	t.Run("freshly-created value is Reset before first Get", func(t *testing.T) {
		t.Parallel()
		p := pool.NewResetPool(func() *resettable {
			return &resettable{value: 42} // dirty initial state
		})
		v := p.Get()
		// newFn returned value=42, but ResetPool's New callback
		// calls Reset which zeroes value.
		if v.value != 0 {
			t.Fatalf("freshly-allocated value: got %d, want 0 (Reset)",
				v.value)
		}
		if got := v.resetCalls.Load(); got != 1 {
			t.Fatalf("Reset calls on fresh value: got %d, want 1", got)
		}
	})

	t.Run("Put auto-Resets before caching", func(t *testing.T) {
		t.Parallel()
		p := pool.NewResetPool(func() *resettable { return &resettable{} })
		v := p.Get()
		v.value = 99 // tenant-specific state
		v.resetCalls.Store(0)

		p.Put(v)

		// Put must have called Reset.
		if got := v.resetCalls.Load(); got != 1 {
			t.Fatalf("Reset calls on Put: got %d, want 1", got)
		}
		// And the value field must be zeroed.
		if v.value != 0 {
			t.Fatalf("value after Put: got %d, want 0", v.value)
		}
	})

	t.Run("Get after Put returns a Reset value", func(t *testing.T) {
		t.Parallel()
		p := pool.NewResetPool(func() *resettable { return &resettable{} })
		v := p.Get()
		v.value = 99
		p.Put(v)

		got := p.Get()
		// sync.Pool may evict; guard the same-pointer case.
		if got == v && got.value != 0 {
			t.Fatalf("recycled value not Reset: got %d, want 0", got.value)
		}
	})

	t.Run("concurrent Get/Put is safe", func(t *testing.T) {
		t.Parallel()
		p := pool.NewResetPool(func() *resettable { return &resettable{} })
		const goroutines = 16
		const iterations = 500
		var wg sync.WaitGroup
		wg.Add(goroutines)
		for range goroutines {
			go func() {
				defer wg.Done()
				for i := range iterations {
					v := p.Get()
					v.value = i
					p.Put(v)
				}
			}()
		}
		wg.Wait()
	})
}

// TestResetPoolZeroAlloc cannot run in parallel —
// testing.AllocsPerRun panics if any other test is running.
//
//nolint:paralleltest // see comment above
func TestResetPoolZeroAlloc(t *testing.T) {
	p := pool.NewResetPool(func() *resettable { return &resettable{} })
	v := p.Get()
	p.Put(v)

	if got := testing.AllocsPerRun(100, func() {
		v := p.Get()
		p.Put(v)
	}); got != 0 {
		t.Fatalf("Get+Put cycle: %v allocs/op, want 0", got)
	}
}

func BenchmarkResetPool(b *testing.B) {
	p := pool.NewResetPool(func() *resettable { return new(resettable) })
	p.Put(p.Get()) // warm
	b.ReportAllocs()
	for b.Loop() {
		v := p.Get()
		v.value = 42
		p.Put(v)
	}
}
