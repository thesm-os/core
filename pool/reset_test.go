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
		testkit.Equal(t, v.value, 0, "freshly-allocated value must be Reset before first Get")
		testkit.Equal(t, v.resetCalls.Load(), int32(1),
			"Reset must be called exactly once on fresh value")
	})

	t.Run("Put auto-Resets before caching", func(t *testing.T) {
		t.Parallel()
		p := pool.NewResetPool(func() *resettable { return &resettable{} })
		v := p.Get()
		v.value = 99 // tenant-specific state
		v.resetCalls.Store(0)

		p.Put(v)

		// Put must have called Reset.
		testkit.Equal(t, v.resetCalls.Load(), int32(1),
			"Put must call Reset exactly once before caching")
		// And the value field must be zeroed.
		testkit.Equal(t, v.value, 0, "Put must zero the value field via Reset")
	})

	t.Run("Get after Put returns a Reset value", func(t *testing.T) {
		t.Parallel()
		p := pool.NewResetPool(func() *resettable { return &resettable{} })
		v := p.Get()
		v.value = 99
		p.Put(v)

		got := p.Get()
		// sync.Pool may evict; guard the same-pointer case.
		if got == v {
			testkit.Equal(t, got.value, 0, "recycled value must be Reset")
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

	testkit.Equal(t, testing.AllocsPerRun(100, func() {
		v := p.Get()
		p.Put(v)
	}), float64(0), "Get+Put cycle must be zero-alloc")
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
