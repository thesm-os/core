// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package resilience_test

import (
	"context"
	"runtime"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"

	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/resilience"
)

// The operations that the history of the concurrent permits records.
const (
	opAcquire = "acquire"
	opRelease = "release"
)

func TestBulkhead(t *testing.T) {
	t.Parallel()

	t.Run("NewBulkhead", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give resilience.BulkheadConfig
		}{
			{name: "returns ErrConfig for a nil Clock", give: resilience.BulkheadConfig{Limit: 1}},
			{
				name: "returns ErrConfig for a Limit of zero",
				give: resilience.BulkheadConfig{Clock: fake.New(originUTC)},
			},
			{
				name: "returns ErrConfig for a negative Limit",
				give: resilience.BulkheadConfig{Clock: fake.New(originUTC), Limit: -1},
			},
			{
				name: "returns ErrConfig for a negative Queue",
				give: resilience.BulkheadConfig{Clock: fake.New(originUTC), Limit: 1, Queue: -1},
			},
			{
				name: "returns ErrConfig for a negative Wait",
				give: resilience.BulkheadConfig{Clock: fake.New(originUTC), Limit: 1, Wait: -time.Second},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, err := resilience.NewBulkhead(tt.give)
				expect.ErrorIs(t, err, resilience.ErrConfig, "NewBulkhead must refuse the config")
				expect.Equal(t, errs.Classify(err), errs.Invalid, "ErrConfig must classify as Invalid")
			})
		}

		t.Run("returns a Bulkhead for a Limit alone", func(t *testing.T) {
			t.Parallel()
			_, err := resilience.NewBulkhead(resilience.BulkheadConfig{Clock: fake.New(originUTC), Limit: 1})
			assert.NoError(t, err, "a Limit alone must be a complete config")
		})
	})

	t.Run("Acquire", func(t *testing.T) {
		t.Parallel()

		t.Run("admits callers up to the limit", func(t *testing.T) {
			t.Parallel()
			b := newBulkhead(t, resilience.BulkheadConfig{Clock: fake.New(originUTC), Limit: 3})

			acquireAll(t, b, 3)
			assert.Equal(t, b.InFlight(), 3, "every permit must be taken")
		})

		t.Run("returns ErrFull at the limit without a queue", func(t *testing.T) {
			t.Parallel()
			b := newBulkhead(t, resilience.BulkheadConfig{Clock: fake.New(originUTC), Limit: 1})
			defer acquireAll(t, b, 1)[0]()

			_, err := b.Acquire(bounded(t))
			assert.ErrorIs(t, err, resilience.ErrFull, "a caller at the limit without a queue must be refused at once")
		})

		t.Run("admits the next caller after a release", func(t *testing.T) {
			t.Parallel()
			b := newBulkhead(t, resilience.BulkheadConfig{Clock: fake.New(originUTC), Limit: 1})
			acquireAll(t, b, 1)[0]()

			next, err := b.Acquire(bounded(t))
			assert.NoError(t, err, "a free permit must admit the next caller")
			next()
		})

		t.Run("gives a permit to a queued caller", func(t *testing.T) {
			t.Parallel()
			b := newBulkhead(t, resilience.BulkheadConfig{Clock: fake.New(originUTC), Limit: 1, Queue: 1})
			held := acquireAll(t, b, 1)[0]

			got := make(chan error, 1)
			go func() {
				release, err := b.Acquire(bounded(t))
				if err == nil {
					release()
				}
				got <- err
			}()

			assert.EventuallyTrue(t, patience, func() bool { return b.Queued() > 0 }, "the caller must queue")
			held()
			assert.NoError(t, await(t, got, "Acquire must return"), "the queued caller must receive the free permit")
		})

		t.Run("frees the queue slot of a caller that took a permit", func(t *testing.T) {
			t.Parallel()
			b := newBulkhead(t, resilience.BulkheadConfig{Clock: fake.New(originUTC), Limit: 1, Queue: 1})
			held := acquireAll(t, b, 1)[0]

			got := make(chan error, 1)
			go func() {
				release, err := b.Acquire(bounded(t))
				if err == nil {
					release()
				}
				got <- err
			}()

			assert.EventuallyTrue(t, patience, func() bool { return b.Queued() > 0 }, "the caller must queue")
			held()
			assert.NoError(t, await(t, got, "Acquire must return"), "the queued caller must receive the free permit")
			assert.Equal(t, b.Queued(), 0, "a caller that took a permit must leave the queue")
		})

		t.Run("returns ErrFull when the limit and the queue are full", func(t *testing.T) {
			t.Parallel()
			b := newBulkhead(t, resilience.BulkheadConfig{Clock: fake.New(originUTC), Limit: 1, Queue: 1})
			defer acquireAll(t, b, 1)[0]()

			blocked, cancel := context.WithCancel(t.Context())
			defer cancel()
			go func() {
				release, err := b.Acquire(blocked)
				if err == nil {
					release()
				}
			}()
			assert.EventuallyTrue(t, patience, func() bool { return b.Queued() > 0 }, "the caller must queue")

			_, err := b.Acquire(bounded(t))
			assert.ErrorIs(t, err, resilience.ErrFull, "a caller at a full limit and a full queue must be refused")
		})

		t.Run("returns ErrWaitTimeout for a caller that waited for Wait", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			b := newBulkhead(t, resilience.BulkheadConfig{Clock: c, Limit: 1, Queue: 1, Wait: 5 * time.Second})
			defer acquireAll(t, b, 1)[0]()

			got := make(chan error, 1)
			go func() {
				_, err := b.Acquire(bounded(t))
				got <- err
			}()

			c.AwaitWaiters(1)
			c.Advance(6 * time.Second)
			assert.ErrorIs(t, await(t, got, "Acquire must return"), resilience.ErrWaitTimeout,
				"a caller that waited for its Wait must time out")
		})

		t.Run("takes a permit that a release frees before the deadline", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			b := newBulkhead(t, resilience.BulkheadConfig{Clock: c, Limit: 1, Queue: 1, Wait: 5 * time.Second})
			held := acquireAll(t, b, 1)[0]

			got := make(chan error, 1)
			go func() {
				release, err := b.Acquire(bounded(t))
				if err == nil {
					release()
				}
				got <- err
			}()

			c.AwaitWaiters(1)
			held()
			assert.NoError(t, await(t, got, "Acquire must return"), "a permit freed before the deadline must be taken")
		})

		contexts := []struct {
			name string
			wait time.Duration
		}{
			{name: "returns the error of a context that ends inside the Wait", wait: time.Hour},
			{name: "returns the error of a context that ends in a queue without a Wait"},
		}
		for _, tt := range contexts {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				c := fake.New(originUTC)
				b := newBulkhead(t, resilience.BulkheadConfig{Clock: c, Limit: 1, Queue: 1, Wait: tt.wait})
				defer acquireAll(t, b, 1)[0]()

				ctx, cancel := context.WithCancel(t.Context())
				got := make(chan error, 1)
				go func() {
					_, err := b.Acquire(ctx)
					got <- err
				}()

				assert.EventuallyTrue(t, patience, func() bool { return b.Queued() > 0 }, "the caller must queue")
				cancel()

				err := await(t, got, "Acquire must return")
				expect.ErrorIs(t, err, context.Canceled, "a cancellation must surface as the error of the context")
				expect.ErrorIsNot(t, err, resilience.ErrFull, "a cancellation must not read as a rejection")
				expect.ErrorIsNot(t, err, resilience.ErrWaitTimeout, "a cancellation must not read as a timeout")
			})
		}

		// The callers take and release permits in rounds, and the history of
		// the calls must linearize against a semaphore of limit permits. The
		// queue admits every caller, so each Acquire waits for a permit.
		t.Run("keeps the limit across concurrent callers", func(t *testing.T) {
			t.Parallel()
			const (
				limit   = 2
				callers = 8
				rounds  = 25
			)

			b := newBulkhead(t, resilience.BulkheadConfig{Clock: fake.New(originUTC), Limit: limit, Queue: callers})
			h := history.New()
			outcomes := history.Concurrently(callers, patience, func(client int) (any, error) {
				for range rounds {
					call := h.Invoke(client, opAcquire, nil)
					release, err := b.Acquire(bounded(t))
					call.OK(err)
					if err != nil {
						return client, err
					}

					// The permit lasts across a scheduling point, so that the
					// holders overlap.
					runtime.Gosched()

					call = h.Invoke(client, opRelease, nil)
					release()
					call.OK(nil)
				}

				return client, nil
			})
			for _, o := range outcomes {
				assert.True(t, o.Finished, "every caller must return")
			}

			history.Linearizable(t, h, history.Spec[int]{
				Initial: func() int { return 0 },
				Next: func(held int, op history.Operation) []int {
					if op.Name == opRelease {
						return []int{held - 1}
					}
					if held == limit || !op.Returned(nil) {
						return nil
					}

					return []int{held + 1}
				},
			}, "an Acquire must take one of limit permits that the releases give back")
			expect.Equal(t, b.InFlight(), 0, "every permit must return")
		})

		t.Run("returns a release that gives the permit back once", func(t *testing.T) {
			t.Parallel()
			b := newBulkhead(t, resilience.BulkheadConfig{Clock: fake.New(originUTC), Limit: 1})
			release := acquireAll(t, b, 1)[0]
			release()
			release()

			assert.Equal(t, b.InFlight(), 0, "the permit must return")
			first, err := b.Acquire(bounded(t))
			assert.NoError(t, err, "the free permit must admit a caller")
			defer first()

			_, err = b.Acquire(bounded(t))
			assert.ErrorIs(t, err, resilience.ErrFull, "a second release must not admit a second holder")
		})
	})

	t.Run("InFlight", func(t *testing.T) {
		t.Parallel()

		t.Run("returns 0 once every permit returned", func(t *testing.T) {
			t.Parallel()
			b := newBulkhead(t, resilience.BulkheadConfig{Clock: fake.New(originUTC), Limit: 3})

			for _, release := range acquireAll(t, b, 3) {
				release()
			}

			assert.Equal(t, b.InFlight(), 0, "the releases must return every permit")
		})
	})
}

// TestBulkheadAllocs checks the allocation contract of Acquire and of the
// gauges. MaxAllocs counts the allocations of the whole process, so the
// test does not run in parallel.
//
//nolint:paralleltest // see above
func TestBulkheadAllocs(t *testing.T) {
	ctx := t.Context()

	t.Run("Acquire", func(t *testing.T) {
		t.Run("of a free permit", func(t *testing.T) {
			b := newBulkhead(t, resilience.BulkheadConfig{Clock: fake.New(originUTC), Limit: 1})

			var err error
			expect.MaxAllocs(t, func() {
				var release func()
				release, err = b.Acquire(ctx)
				release()
			}, 2, "Acquire must allocate the release of the permit alone")
			assert.NoError(t, err, "the test must measure a granted permit")
		})

		t.Run("of a full Bulkhead", func(t *testing.T) {
			b := newBulkhead(t, resilience.BulkheadConfig{Clock: fake.New(originUTC), Limit: 1})
			release, err := b.Acquire(ctx)
			assert.NoError(t, err, "the test must take the permit")
			defer release()

			expect.MaxAllocs(t, func() { _, err = b.Acquire(ctx) }, 0, "a rejection must not allocate")
			assert.ErrorIs(t, err, resilience.ErrFull, "the test must measure a rejection")
		})
	})

	t.Run("InFlight", func(t *testing.T) {
		b := newBulkhead(t, resilience.BulkheadConfig{Clock: fake.New(originUTC), Limit: 2})
		defer acquireAll(t, b, 1)[0]()

		var got int
		expect.MaxAllocs(t, func() { got = b.InFlight() }, 0, "InFlight must not allocate")
		assert.Equal(t, got, 1, "the test must measure a taken permit")
	})

	t.Run("Queued", func(t *testing.T) {
		b := newBulkhead(t, resilience.BulkheadConfig{Clock: fake.New(originUTC), Limit: 1})

		got := -1
		expect.MaxAllocs(t, func() { got = b.Queued() }, 0, "Queued must not allocate")
		assert.Equal(t, got, 0, "the test must measure an empty queue")
	})
}

// BenchmarkBulkhead reports the cost of Acquire and of the gauges, and
// fails above the allocations that their contracts state.
func BenchmarkBulkhead(b *testing.B) {
	ctx := b.Context()

	b.Run("Acquire", func(b *testing.B) {
		b.Run("of a free permit", func(b *testing.B) {
			bh := newBulkhead(b, resilience.BulkheadConfig{Clock: fake.New(originUTC), Limit: 1})

			var err error

			c := bench.Start(b).MaxAllocs(2)
			defer c.End()

			for c.Loop() {
				var release func()
				release, err = bh.Acquire(ctx)
				release()
			}

			assert.NoError(b, err, "the benchmark must measure a granted permit")
		})

		b.Run("of a full Bulkhead", func(b *testing.B) {
			bh := newBulkhead(b, resilience.BulkheadConfig{Clock: fake.New(originUTC), Limit: 1})
			release, err := bh.Acquire(ctx)
			assert.NoError(b, err, "the benchmark must take the permit")
			defer release()

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				_, err = bh.Acquire(ctx)
			}

			assert.ErrorIs(b, err, resilience.ErrFull, "the benchmark must measure a rejection")
		})
	})

	b.Run("InFlight", func(b *testing.B) {
		bh := newBulkhead(b, resilience.BulkheadConfig{Clock: fake.New(originUTC), Limit: 2})
		defer acquireAll(b, bh, 1)[0]()

		var got int

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = bh.InFlight()
		}

		assert.Equal(b, got, 1, "the benchmark must measure a taken permit")
	})

	b.Run("Queued", func(b *testing.B) {
		bh := newBulkhead(b, resilience.BulkheadConfig{Clock: fake.New(originUTC), Limit: 1})

		got := -1

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = bh.Queued()
		}

		assert.Equal(b, got, 0, "the benchmark must measure an empty queue")
	})
}

// newBulkhead returns the Bulkhead of cfg, and fails tb when NewBulkhead
// refuses cfg.
func newBulkhead(tb testing.TB, cfg resilience.BulkheadConfig) *resilience.Bulkhead {
	tb.Helper()

	b, err := resilience.NewBulkhead(cfg)
	assert.NoError(tb, err, "NewBulkhead must accept the config")

	return b
}

// acquireAll takes n permits of b, and returns their releases, and fails tb
// when Acquire refuses one.
func acquireAll(tb testing.TB, b *resilience.Bulkhead, n int) []func() {
	tb.Helper()

	releases := make([]func(), 0, n)
	for range n {
		release, err := b.Acquire(bounded(tb))
		assert.NoError(tb, err, "Acquire must admit a caller below the limit")
		releases = append(releases, release)
	}

	return releases
}
