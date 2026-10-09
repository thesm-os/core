// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package pool_test

import (
	"context"
	"math"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/pool"
)

// counter is the newFn of the Bounded pools of the cases. It returns a
// pointer to the number of values that it built, 1 for the first.
type counter struct {
	made atomic.Int64
}

// build returns a pointer to the number of values that c built, this one
// included.
func (c *counter) build() *int {
	n := int(c.made.Add(1))

	return &n
}

func TestBounded(t *testing.T) {
	t.Parallel()

	t.Run("NewBounded", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a pool whose Cap is the limit", func(t *testing.T) {
			t.Parallel()
			p, err := pool.NewBounded(7, new(counter).build)
			assert.NoError(t, err, "a positive limit must be accepted")
			assert.Equal(t, p.Cap(), 7, "Cap must return the limit")
		})

		t.Run("returns ErrLimit for a limit that is not positive", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(limit int) error {
				_, err := pool.NewBounded(limit, new(counter).build)

				return err
			}, pool.ErrLimit, "NewBounded must refuse every limit below 1",
				prop.Using(prop.Integer(math.MinInt, 0)), prop.Example(0), prop.Example(-1))
		})

		t.Run("returns ErrLimit as an Invalid error", func(t *testing.T) {
			t.Parallel()
			_, err := pool.NewBounded(0, new(counter).build)
			assert.Equal(t, errs.Classify(err), errs.Invalid, "ErrLimit must classify as Invalid")
		})

		t.Run("calls newFn no time", func(t *testing.T) {
			t.Parallel()
			c := new(counter)
			p, err := pool.NewBounded(4, c.build)
			assert.NoError(t, err, "a positive limit must be accepted")
			expect.Equal(t, c.made.Load(), int64(0), "NewBounded must not construct a value")
			expect.Equal(t, p.Created(), 0, "Created must count no value")
		})
	})

	t.Run("Get", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the value that newFn constructs", func(t *testing.T) {
			t.Parallel()
			p, _ := bounded(t, 2)
			first, err := p.Get(t.Context())
			assert.NoError(t, err, "Get must succeed below the limit")
			second, err := p.Get(t.Context())
			assert.NoError(t, err, "Get must succeed below the limit")
			expect.Equal(t, *first, 1, "the first Get must return the first value that newFn built")
			expect.Equal(t, *second, 2, "the second Get must return the second value that newFn built")
		})

		t.Run("constructs one value per call below the limit", func(t *testing.T) {
			t.Parallel()
			p, _ := bounded(t, 3)
			assert.Accumulates(t, func(ctx context.Context) error {
				_, err := p.Get(ctx)

				return err
			}, t.Context(), p.Created, "each Get below the limit must construct one value")
		})

		t.Run("returns a value that Put returned without a construction", func(t *testing.T) {
			t.Parallel()
			p, c := bounded(t, 4)
			v, err := p.Get(t.Context())
			assert.NoError(t, err, "Get must succeed")
			p.Put(v)
			got, err := p.Get(t.Context())
			assert.NoError(t, err, "Get must succeed")
			expect.Equal(t, got, v, "a returned value must be handed out again", expect.ByIdentity())
			expect.Equal(t, c.made.Load(), int64(1), "the reuse must not construct")
		})

		t.Run("waits at the limit for a Put", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				p, _ := bounded(t, 1)
				held, err := p.Get(t.Context())
				assert.NoError(t, err, "the first Get must succeed")
				var got atomic.Pointer[int]
				go func() {
					if v, err := p.Get(t.Context()); err == nil {
						got.Store(v)
					}
				}()
				synctest.Wait()
				assert.Nil(t, got.Load(), "Get must wait while every value is held")
				p.Put(held)
				synctest.Wait()
				assert.Equal(t, got.Load(), held, "the waiting Get must receive the returned value",
					assert.ByIdentity())
			})
		})

		t.Run("returns the error of ctx when ctx ends during the wait", func(t *testing.T) {
			t.Parallel()
			p, _ := bounded(t, 1)
			_, err := p.Get(t.Context())
			assert.NoError(t, err, "the first Get must succeed")
			assert.HonoursCancellation(t, func(ctx context.Context) error {
				_, err := p.Get(ctx)

				return err
			}, "a Get at the limit must return the error of a cancelled ctx")
		})

		t.Run("returns an available value under a cancelled ctx", func(t *testing.T) {
			t.Parallel()
			p, _ := bounded(t, 1)
			v, err := p.Get(t.Context())
			assert.NoError(t, err, "the first Get must succeed")
			p.Put(v)
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			got, err := p.Get(ctx)
			assert.NoError(t, err, "an available value must be served without a read of ctx")
			assert.Equal(t, got, v, "the available value must be returned", assert.ByIdentity())
		})

		t.Run("constructs at most limit values under concurrent calls", func(t *testing.T) {
			t.Parallel()
			const limit = 4
			p, c := bounded(t, limit)
			outcomes := history.Concurrently(32, 10*time.Second, func(int) (any, error) {
				for range 50 {
					v, err := p.Get(t.Context())
					if err != nil {
						return nil, err
					}
					p.Put(v)
				}

				return true, nil
			})
			for _, o := range outcomes {
				expect.True(t, o.Finished, "every client must finish")
				expect.NoError(t, o.Error, "every Get must succeed")
			}
			expect.InRange(t, c.made.Load(), 1, limit, "the pool must construct at most limit values")
			expect.Equal(t, int64(p.Created()), c.made.Load(), "Created must count the constructions")
			expect.Equal(t, int64(p.Len()), c.made.Load(), "every value must be back in the pool")
		})
	})

	t.Run("Put", func(t *testing.T) {
		t.Parallel()

		t.Run("returns every value that the pool issued without a wait", func(t *testing.T) {
			t.Parallel()
			p, _ := bounded(t, 3)
			values := make([]*int, 0, 3)
			for range 3 {
				v, err := p.Get(t.Context())
				assert.NoError(t, err, "Get must succeed")
				values = append(values, v)
			}
			assert.CompletesWithin(t, time.Second, func(context.Context) error {
				for _, v := range values {
					p.Put(v)
				}

				return nil
			}, "Put must not wait for room")
			assert.Equal(t, p.Len(), 3, "every returned value must be available again")
		})
	})

	t.Run("Len", func(t *testing.T) {
		t.Parallel()

		t.Run("counts only the values available to Get", func(t *testing.T) {
			t.Parallel()
			p, _ := bounded(t, 2)
			assert.Equal(t, p.Len(), 0, "a new pool must contain no value")
			v, err := p.Get(t.Context())
			assert.NoError(t, err, "Get must succeed")
			assert.Equal(t, p.Len(), 0, "a value that a caller holds must not count")
			p.Put(v)
			assert.Equal(t, p.Len(), 1, "a returned value must count")
		})
	})

	t.Run("Created", func(t *testing.T) {
		t.Parallel()

		t.Run("never falls under any sequence of calls", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Created must never fall", func(c *prop.Case) {
				p, _ := bounded(c, 3)
				gets := c.Draw(prop.List(prop.Boolean(), prop.MaxSize(30)), "gets")
				var held []*int
				i := 0
				assert.Monotonic(c, p.Created, func() error {
					get := gets[i]
					i++
					if get || len(held) == 0 {
						ctx, cancel := context.WithCancel(c.Context())
						if len(held) == p.Cap() {
							cancel()
						}
						v, err := p.Get(ctx)
						cancel()
						if err == nil {
							held = append(held, v)
						}

						return nil
					}
					p.Put(held[len(held)-1])
					held = held[:len(held)-1]

					return nil
				}, len(gets), "Created must count every construction and never fall")
			})
		})

		t.Run("returns after a Get that waited at the limit", func(t *testing.T) {
			t.Parallel()
			p, _ := bounded(t, 1)
			_, err := p.Get(t.Context())
			assert.NoError(t, err, "the first Get must succeed")
			assert.HonoursCancellation(t, func(ctx context.Context) error {
				_, err := p.Get(ctx)

				return err
			}, "the second Get must wait and end with ctx")
			assert.CompletesWithin(t, time.Second, func(context.Context) error {
				assert.Equal(t, p.Created(), 1, "Created must count one construction")

				return nil
			}, "Created must not wait for the lock")
		})
	})
}

// TestBoundedAllocs checks that a Get of an available value and its Put do
// not allocate. MaxAllocs counts the allocations of the whole process, so
// the test does not run in parallel.
func TestBoundedAllocs(t *testing.T) {
	p, _ := bounded(t, 1)
	ctx := t.Context()
	v, err := p.Get(ctx)
	assert.NoError(t, err, "Get must succeed")
	p.Put(v)

	t.Run("Get", func(t *testing.T) {
		expect.MaxAllocs(t, func() {
			v, err = p.Get(ctx)
			p.Put(v)
		}, 0, "a Get of an available value and its Put must not allocate")
		assert.NoError(t, err, "the test must measure a Get that succeeds")
	})
}

// BenchmarkBounded reports the cost of a Get of an available value and its
// Put, on one goroutine and on goroutines in parallel, and fails when the
// pair allocates.
func BenchmarkBounded(b *testing.B) {
	b.Run("Get", func(b *testing.B) {
		b.Run("of an available value followed by its Put", func(b *testing.B) {
			p, _ := bounded(b, 1)
			ctx := b.Context()
			var err error

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				var v *int
				v, err = p.Get(ctx)
				p.Put(v)
			}

			assert.NoError(b, err, "the benchmark must measure a Get that succeeds")
		})

		b.Run("of goroutines in parallel followed by its Put", func(b *testing.B) {
			p, _ := bounded(b, 8)
			ctx := b.Context()
			var failed atomic.Int64

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			c.RunParallel(func(pb *bench.PB) {
				for pb.Next() {
					v, err := p.Get(ctx)
					if err != nil {
						failed.Add(1)

						continue
					}
					p.Put(v)
				}
			})

			assert.Equal(b, failed.Load(), int64(0), "the benchmark must measure Gets that succeed")
		})
	})
}

// bounded returns a Bounded pool of limit values that a counter builds,
// and the counter. It fails tb when NewBounded refuses the limit.
func bounded(tb assert.TB, limit int) (*pool.Bounded[*int], *counter) {
	tb.Helper()
	c := new(counter)
	p, err := pool.NewBounded(limit, c.build)
	assert.NoError(tb, err, "NewBounded must accept a positive limit")

	return p, c
}
