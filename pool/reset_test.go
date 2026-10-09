// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package pool_test

import (
	"sync/atomic"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/pool"
)

// resettable is a [pool.Resettable] that counts the calls of Reset.
type resettable struct {
	value      int
	resetCalls atomic.Int32
}

// Reset zeroes value and counts the call.
func (r *resettable) Reset() {
	r.value = 0
	r.resetCalls.Add(1)
}

func TestResetPool(t *testing.T) {
	t.Parallel()

	t.Run("Get", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a value that newFn constructs after one Reset", func(t *testing.T) {
			t.Parallel()
			p := pool.NewResetPool(func() *resettable { return &resettable{value: 42} })
			v := p.Get()
			expect.Equal(t, v.value, 0, "a new value must be Reset before the first Get")
			expect.Equal(t, v.resetCalls.Load(), int32(1), "a new value must be Reset once")
		})

		t.Run("returns a value that Put returned after its Reset", func(t *testing.T) {
			t.Parallel()
			p := pool.NewResetPool(func() *resettable { return &resettable{} })
			v := p.Get()
			v.value = 99
			p.Put(v)
			// A sync.Pool may drop a value at a collection, so only a
			// Get that returns the same pointer states anything.
			if got := p.Get(); got == v {
				assert.Equal(t, got.value, 0, "a recycled value must be Reset")
			}
		})
	})

	t.Run("Put", func(t *testing.T) {
		t.Parallel()

		t.Run("calls Reset once before the value returns to the pool", func(t *testing.T) {
			t.Parallel()
			p := pool.NewResetPool(func() *resettable { return &resettable{} })
			v := p.Get()
			v.value = 99
			v.resetCalls.Store(0)
			p.Put(v)
			expect.Equal(t, v.resetCalls.Load(), int32(1), "Put must call Reset once")
			expect.Equal(t, v.value, 0, "Put must zero the value through Reset")
		})
	})
}

// TestResetPoolAllocs checks that a Get of a pooled pointer and its Put do
// not allocate. MaxAllocs counts the allocations of the whole process, so
// the test does not run in parallel.
func TestResetPoolAllocs(t *testing.T) {
	p := pool.NewResetPool(func() *resettable { return new(resettable) })
	p.Put(p.Get())

	t.Run("Get", func(t *testing.T) {
		var v *resettable
		expect.MaxAllocs(t, func() {
			v = p.Get()
			p.Put(v)
		}, 0, "a Get of a pooled pointer and its Put must not allocate")
		assert.NotNil(t, v, "the test must measure a Get that returns a value")
	})
}

// BenchmarkResetPool reports the cost of a Get, a write and the Put that
// resets the value, and fails when the cycle allocates.
func BenchmarkResetPool(b *testing.B) {
	p := pool.NewResetPool(func() *resettable { return new(resettable) })
	p.Put(p.Get())

	b.Run("Get", func(b *testing.B) {
		b.Run("of a pooled pointer followed by its Put", func(b *testing.B) {
			var v *resettable

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				v = p.Get()
				v.value = 42
				p.Put(v)
			}

			assert.Equal(b, v.value, 0, "the benchmark must measure a Put that resets the value")
		})
	})
}
