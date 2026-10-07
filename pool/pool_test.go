// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package pool_test

import (
	"sync/atomic"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"

	"go.thesmos.sh/core/pool"
)

func TestPool(t *testing.T) {
	t.Parallel()

	t.Run("Get", func(t *testing.T) {
		t.Parallel()

		t.Run("calls newFn once for an empty pool", func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			p := pool.NewPool(func() *int {
				calls.Add(1)
				v := 42

				return &v
			})
			v := p.Get()
			expect.Equal(t, calls.Load(), int32(1), "Get of an empty pool must call newFn once")
			expect.Equal(t, *v, 42, "Get must return the value of newFn")
		})

		t.Run("returns the type of newFn", func(t *testing.T) {
			t.Parallel()
			p := pool.NewPool(func() string { return "hello" })
			assert.Equal(t, p.Get(), "hello", "Get must return the typed value")
		})

		t.Run("returns a value that Put returned with its state", func(t *testing.T) {
			t.Parallel()
			p := pool.NewPool(func() *int { return new(int) })
			original := p.Get()
			*original = 99
			p.Put(original)
			// A sync.Pool may drop a value at a collection, so only a
			// Get that returns the same pointer states anything.
			if got := p.Get(); got == original {
				assert.Equal(t, *got, 99, "a pooled value must keep its state")
			}
		})

		t.Run("returns a value of its own to each concurrent caller", func(t *testing.T) {
			t.Parallel()
			p := pool.NewPool(func() *atomic.Int32 { return new(atomic.Int32) })
			outcomes := history.Concurrently(16, 10*time.Second, func(int) (any, error) {
				shared := 0
				for range 1_000 {
					v := p.Get()
					if v.Add(1) != 1 {
						shared++
					}
					v.Add(-1)
					p.Put(v)
				}

				return shared, nil
			})
			for _, o := range outcomes {
				expect.True(t, o.Finished, "every client must finish")
				expect.Equal(t, o.Output, any(0), "no two callers may hold one value at once")
			}
		})
	})

	t.Run("Put", func(t *testing.T) {
		t.Parallel()

		t.Run("accepts any number of values", func(t *testing.T) {
			t.Parallel()
			p := pool.NewPool(func() int { return 0 })
			assert.NotPanics(t, func() {
				for i := range 100 {
					p.Put(i)
				}
			}, "Put must accept every value")
		})
	})
}

// TestPoolAllocs checks that a Get of a pooled pointer and its Put do not
// allocate. MaxAllocs counts the allocations of the whole process, so the
// test does not run in parallel.
//
//nolint:paralleltest // see above
func TestPoolAllocs(t *testing.T) {
	p := pool.NewPool(func() *int { return new(int) })
	p.Put(p.Get())

	t.Run("Get", func(t *testing.T) {
		var v *int
		expect.MaxAllocs(t, func() {
			v = p.Get()
			p.Put(v)
		}, 0, "a Get of a pooled pointer and its Put must not allocate")
		assert.NotNil(t, v, "the test must measure a Get that returns a value")
	})
}

// BenchmarkPool reports the cost of a Get and its Put, the pattern of the
// pools of HMAC and rand/crypto, on one goroutine and on goroutines in
// parallel, and fails when the pair allocates.
func BenchmarkPool(b *testing.B) {
	p := pool.NewPool(func() *resettable { return new(resettable) })
	p.Put(p.Get())

	b.Run("Get", func(b *testing.B) {
		b.Run("of a pooled pointer followed by its Put", func(b *testing.B) {
			var v *resettable

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				v = p.Get()
				p.Put(v)
			}

			assert.NotNil(b, v, "the benchmark must measure a Get that returns a value")
		})

		b.Run("of goroutines in parallel followed by its Put", func(b *testing.B) {
			var gets atomic.Int64

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			c.RunParallel(func(pb *bench.PB) {
				n := int64(0)
				for pb.Next() {
					p.Put(p.Get())
					n++
				}
				gets.Add(n)
			})

			assert.NotEqual(b, gets.Load(), int64(0), "the benchmark must measure Gets")
		})
	})
}
