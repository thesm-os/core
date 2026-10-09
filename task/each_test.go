// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package task_test

import (
	"context"
	"errors"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/task"
)

// sweepAllocs is the number of allocations of one call of Each, whatever
// the number of its elements: the derived context, its state, the sweep
// and the closure that its goroutines share.
const sweepAllocs = 4

// Generators of the properties over sweeps.
var (
	// limits generates the limit of a call.
	limits = prop.Integer(1, 16)

	// elements generates the elements of a call, the empty slice included.
	elements = prop.List(prop.Integer(-1000, 1000), prop.MaxSize(200))
)

func TestEach(t *testing.T) {
	t.Parallel()

	t.Run("passes every element with its index", func(t *testing.T) {
		t.Parallel()
		prop.ForAll(t, "Each must call fn with the element at each index", func(c *prop.Case) {
			items := c.Draw(elements, "items")

			var mismatches atomic.Int32
			err := task.Each(c.Context(), c.Draw(limits, "limit"), items, func(_ context.Context, i, item int) error {
				if item != items[i] {
					mismatches.Add(1)
				}

				return nil
			})
			assert.NoError(c, err, "Each must succeed")
			assert.Equal(c, mismatches.Load(), 0, "every call must receive the element at its index")
		})
	})

	t.Run("returns nil for an empty slice without calling fn", func(t *testing.T) {
		t.Parallel()

		var runs atomic.Int32
		err := task.Each(t.Context(), 1, []int(nil), func(context.Context, int, int) error {
			runs.Add(1)

			return errBoom
		})

		assert.NoError(t, err, "an empty slice must succeed")
		assert.Equal(t, runs.Load(), 0, "an empty slice must not call fn")
	})

	t.Run("stops claiming indices after a failure", func(t *testing.T) {
		t.Parallel()

		// Element 0 fails only once element 1 is running, and element 1
		// returns nil only once the failure has cancelled the context, so
		// both workers are busy until the failure is recorded. Element 1's
		// worker then goes back to claiming, and only the context check
		// can stop it. Any third call was claimed after the failure.
		var (
			runs     atomic.Int32
			err      error
			started1 = make(chan struct{})
		)
		assert.CompletesWithin(t, returnBound, func(ctx context.Context) error {
			err = task.Each(ctx, 2, indices(1000), func(ctx context.Context, i, _ int) error {
				runs.Add(1)
				switch i {
				case 0:
					<-started1

					return errBoom
				case 1:
					close(started1)
					_ = awaitCancel(ctx)

					return nil
				default:
					return nil
				}
			})

			return err
		}, "Each must return once its calls return")

		assert.ErrorIs(t, err, errBoom, "the failure must be the result")
		assert.Equal(t, runs.Load(), 2, "no index may be claimed after the failure is recorded")
	})
}

func TestMap(t *testing.T) {
	t.Parallel()

	t.Run("returns the results in the order of items", func(t *testing.T) {
		t.Parallel()
		prop.Equal(t, func(items []int) []string {
			// A failed call returns nil, which differs from every want.
			got, _ := task.Map(t.Context(), 8, items, func(_ context.Context, n int) (string, error) {
				return strconv.Itoa(n), nil
			})

			return got
		}, func(items []int) []string {
			want := make([]string, len(items))
			for i, n := range items {
				want[i] = strconv.Itoa(n)
			}

			return want
		}, "Map must return the result of each element at the element's index", prop.Using(elements))
	})

	t.Run("returns nil results with the first error", func(t *testing.T) {
		t.Parallel()

		var (
			got []int
			err error
		)
		assert.CompletesWithin(t, returnBound, func(ctx context.Context) error {
			got, err = task.Map(ctx, 2, indices(3), func(_ context.Context, n int) (int, error) {
				if n == 1 {
					return 0, errBoom
				}

				return n, nil
			})

			return err
		}, "Map must return once its calls return")

		assert.ErrorIs(t, err, errBoom, "the failure must be the result")
		assert.Nil(t, got, "a partial result must not be returned")
	})

	t.Run("accepts an existing function", func(t *testing.T) {
		t.Parallel()
		got, err := task.Map(t.Context(), 2, []int{1, 2, 3}, double)
		assert.NoError(t, err, "Map must succeed")
		assert.Equal(t, got, []int{2, 4, 6}, "Map must return what the function returned")
	})
}

// TestAll covers All. In the concurrency case each function waits for the
// other, so All passes only when both run at once.
func TestAll(t *testing.T) {
	t.Parallel()

	t.Run("runs every function at once", func(t *testing.T) {
		t.Parallel()

		a, b := make(chan struct{}), make(chan struct{})
		meet := func(mine, theirs chan struct{}) func(context.Context) error {
			return func(context.Context) error {
				close(mine)
				select {
				case <-theirs:
					return nil
				case <-time.After(time.Second):
					return errors.New("task_test: the functions did not run at once")
				}
			}
		}

		var err error
		assert.CompletesWithin(t, returnBound, func(ctx context.Context) error {
			err = task.All(ctx, meet(a, b), meet(b, a))

			return err
		}, "All must return once its functions return")

		assert.NoError(t, err, "All must run its functions at once")
	})

	t.Run("returns nil for no functions", func(t *testing.T) {
		t.Parallel()
		assert.NoError(t, task.All(t.Context()), "All with no functions must succeed")
	})
}

// TestEachAllocs checks the allocation contracts of Each, Map and All: a
// fixed number of allocations per call, whatever the number of elements.
// The calls run on one worker, so each waits for its worker in the same
// way. MaxAllocs counts the allocations of the whole process, so the test
// does not run in parallel.
func TestEachAllocs(t *testing.T) {
	ctx := t.Context()
	noop := func(context.Context, int, int) error { return nil }

	t.Run("Each", func(t *testing.T) {
		for _, n := range []int{64, 512} {
			t.Run(strconv.Itoa(n), func(t *testing.T) {
				items := indices(n)

				var err error
				expect.MaxAllocs(t, func() { err = task.Each(ctx, 1, items, noop) }, sweepAllocs,
					"Each must allocate a fixed amount, whatever the number of elements")
				assert.NoError(t, err, "the test must measure a call that succeeds")
			})
		}

		t.Run("0", func(t *testing.T) {
			var err error
			expect.MaxAllocs(t, func() { err = task.Each(ctx, 1, []int(nil), noop) }, 0,
				"Each of no elements must not allocate")
			assert.NoError(t, err, "the test must measure a call that succeeds")
		})
	})

	t.Run("Map", func(t *testing.T) {
		for _, n := range []int{64, 512} {
			t.Run(strconv.Itoa(n), func(t *testing.T) {
				items := indices(n)

				var (
					got []int
					err error
				)
				expect.MaxAllocs(t, func() { got, err = task.Map(ctx, 1, items, double) }, sweepAllocs+1,
					"Map must allocate what Each allocates, and the results")
				assert.NoError(t, err, "the test must measure a call that succeeds")
				assert.Length(t, got, n, "the test must measure a result per element")
			})
		}
	})

	t.Run("All", func(t *testing.T) {
		fns := []func(context.Context) error{
			func(context.Context) error { return nil },
			func(context.Context) error { return nil },
			func(context.Context) error { return nil },
		}

		var err error
		expect.MaxAllocs(t, func() { err = task.All(ctx, fns...) }, sweepAllocs,
			"All must allocate what Each allocates")
		assert.NoError(t, err, "the test must measure a call that succeeds")
	})
}

// BenchmarkEach reports the cost of Each, Map and All over 1,024 elements
// or three functions, and fails when a call allocates more than its
// allocation contract allows.
func BenchmarkEach(b *testing.B) {
	items := indices(1024)
	limit := 8

	b.Run("Each", func(b *testing.B) {
		var sum atomic.Int64
		fn := func(_ context.Context, i, _ int) error {
			sum.Add(int64(i))

			return nil
		}

		var err error

		c := bench.Start(b).Warmup(benchWarmup).MaxAllocs(sweepAllocs)
		defer c.End()

		for c.Loop() {
			err = task.Each(b.Context(), limit, items, fn)
		}

		assert.NoError(b, err, "the benchmark must measure calls that succeed")
	})

	b.Run("Map", func(b *testing.B) {
		var (
			got []int
			err error
		)

		c := bench.Start(b).Warmup(benchWarmup).MaxAllocs(sweepAllocs + 1)
		defer c.End()

		for c.Loop() {
			got, err = task.Map(b.Context(), limit, items, double)
		}

		assert.NoError(b, err, "the benchmark must measure calls that succeed")
		assert.Length(b, got, len(items), "the benchmark must measure a result per element")
	})

	b.Run("All", func(b *testing.B) {
		var sum atomic.Int64
		lookup := func(context.Context) error {
			sum.Add(1)

			return nil
		}
		fns := []func(context.Context) error{lookup, lookup, lookup}

		var err error

		c := bench.Start(b).Warmup(benchWarmup).MaxAllocs(sweepAllocs)
		defer c.End()

		for c.Loop() {
			err = task.All(b.Context(), fns...)
		}

		assert.NoError(b, err, "the benchmark must measure calls that succeed")
	})
}

// double is an existing function with the shape Map expects.
func double(_ context.Context, n int) (int, error) {
	return n * 2, nil
}
