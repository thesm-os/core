// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package task_test

import (
	"context"
	"errors"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/task"
)

// double is an existing function with the shape Map expects.
func double(_ context.Context, n int) (int, error) {
	return n * 2, nil
}

// TestEach covers Each. In the failure test, element 0 fails only once
// element 1 is running, and element 1 returns nil only once the failure
// has cancelled the context, so both workers are busy until the failure
// is recorded. Element 1's worker then goes back to claiming, and only
// the context check can stop it. Any third call was claimed after the
// failure.
func TestEach(t *testing.T) {
	t.Parallel()

	t.Run("passes every element with its index", func(t *testing.T) {
		t.Parallel()

		items := make([]string, 200)
		for i := range items {
			items[i] = strconv.Itoa(i)
		}

		var (
			mismatches atomic.Int32
			err        error
		)
		within(t, func() {
			err = task.Each(t.Context(), 4, items, func(_ context.Context, i int, item string) error {
				if item != strconv.Itoa(i) {
					mismatches.Add(1)
				}

				return nil
			})
		})

		testkit.NoError(t, err, "Each must succeed")
		testkit.Equal(t, mismatches.Load(), int32(0), "every call must receive the element at its index")
	})

	t.Run("returns nil for an empty slice without calling fn", func(t *testing.T) {
		t.Parallel()

		var runs atomic.Int32
		err := task.Each(t.Context(), 1, []int(nil), func(context.Context, int, int) error {
			runs.Add(1)

			return errBoom
		})

		testkit.NoError(t, err, "an empty slice must succeed")
		testkit.Equal(t, runs.Load(), int32(0), "an empty slice must not call fn")
	})

	t.Run("stops claiming indices after a failure", func(t *testing.T) {
		t.Parallel()

		var (
			runs     atomic.Int32
			err      error
			started1 = make(chan struct{})
		)
		within(t, func() {
			err = task.Each(t.Context(), 2, indices(1000), func(ctx context.Context, i, _ int) error {
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
		})

		testkit.ErrorIs(t, err, errBoom, "the failure must be the result")
		testkit.Equal(t, runs.Load(), int32(2), "no index may be claimed after the failure is recorded")
	})
}

func TestMap(t *testing.T) {
	t.Parallel()

	t.Run("returns the results in the order of items", func(t *testing.T) {
		t.Parallel()

		var (
			got []string
			err error
		)
		within(t, func() {
			got, err = task.Map(t.Context(), 8, indices(1000), func(_ context.Context, n int) (string, error) {
				return strconv.Itoa(n), nil
			})
		})

		testkit.NoError(t, err, "Map must succeed")
		testkit.Len(t, got, 1000, "Map must return one result per element")
		for i, s := range got {
			testkit.Equal(t, s, strconv.Itoa(i), "result "+strconv.Itoa(i)+" must belong to element "+strconv.Itoa(i))
		}
	})

	t.Run("returns nil results with the first error", func(t *testing.T) {
		t.Parallel()

		var (
			got []int
			err error
		)
		within(t, func() {
			got, err = task.Map(t.Context(), 2, indices(3), func(_ context.Context, n int) (int, error) {
				if n == 1 {
					return 0, errBoom
				}

				return n, nil
			})
		})

		testkit.ErrorIs(t, err, errBoom, "the failure must be the result")
		testkit.Equal(t, got, []int(nil), "a partial result must not be returned")
	})

	t.Run("accepts an existing function", func(t *testing.T) {
		t.Parallel()

		var (
			got []int
			err error
		)
		within(t, func() {
			got, err = task.Map(t.Context(), 2, []int{1, 2, 3}, double)
		})

		testkit.NoError(t, err, "Map must succeed")
		testkit.Equal(t, got, []int{2, 4, 6}, "Map must return what the function returned")
	})
}

// TestAll covers All. In the concurrency test each function waits for
// the other, so All passes only when both run at once.
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
					return errors.New("the functions did not run at once")
				}
			}
		}

		var err error
		within(t, func() {
			err = task.All(t.Context(), meet(a, b), meet(b, a))
		})

		testkit.NoError(t, err, "All must run its functions at once")
	})

	t.Run("returns nil for no functions", func(t *testing.T) {
		t.Parallel()

		testkit.NoError(t, task.All(t.Context()), "All with no functions must succeed")
	})
}

// TestZeroAlloc enforces the allocation contract of Each, Map, Stream
// and Quorum: allocations per call do not grow with the number of
// elements. testing.AllocsPerRun reads a process-global malloc
// counter, so this test does not call t.Parallel. Quorum runs with k
// equal to the number of elements, so it calls fn for every one.
//
// Both lengths exceed the limit, so both calls wait for their worker
// in the same way, and the runtime's allocations for that wait appear
// on both sides. An allocation per element would add 448 to one side.
// The deadline makes a Stream that never starts a worker return
// instead of hanging the test.
func TestZeroAlloc(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	noop := func(context.Context, int) error { return nil }
	short, long := indices(64), indices(512)

	cases := []struct {
		name  string
		short func()
		long  func()
	}{
		{
			name: "Each",
			short: func() {
				_ = task.Each(ctx, 1, short, func(ctx context.Context, i, _ int) error { return noop(ctx, i) })
			},
			long: func() { _ = task.Each(ctx, 1, long, func(ctx context.Context, i, _ int) error { return noop(ctx, i) }) },
		},
		{
			name:  "Map",
			short: func() { _, _ = task.Map(ctx, 1, short, double) },
			long:  func() { _, _ = task.Map(ctx, 1, long, double) },
		},
		{
			name:  "Stream",
			short: func() { _ = task.Stream(ctx, 1, sequence(len(short), nil), noop) },
			long:  func() { _ = task.Stream(ctx, 1, sequence(len(long), nil), noop) },
		},
		{
			name: "Quorum",
			short: func() {
				_ = task.Quorum(ctx, 1, len(short), short, func(ctx context.Context, i, _ int) error {
					return noop(ctx, i)
				})
			},
			long: func() {
				_ = task.Quorum(ctx, 1, len(long), long, func(ctx context.Context, i, _ int) error {
					return noop(ctx, i)
				})
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testkit.Equal(t, testing.AllocsPerRun(100, tc.long), testing.AllocsPerRun(100, tc.short),
				tc.name+" must allocate the same for 512 elements as for 64")
		})
	}
}

func BenchmarkEach(b *testing.B) {
	b.ReportAllocs()

	items := make([]int, b.N)
	var sink atomic.Int64

	b.ResetTimer()
	_ = task.Each(b.Context(), runtime.GOMAXPROCS(0)*2, items, func(_ context.Context, i, _ int) error {
		sink.Add(int64(i))

		return nil
	})
}

func BenchmarkAll(b *testing.B) {
	b.ReportAllocs()

	var sink atomic.Int64
	lookup := func(context.Context) error {
		sink.Add(1)

		return nil
	}
	fns := []func(context.Context) error{lookup, lookup, lookup}

	for b.Loop() {
		_ = task.All(b.Context(), fns...)
	}
}
