// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package task_test

import (
	"context"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/task"
)

// streamAllocs is the number of allocations of one call of Stream,
// whatever the number of its elements: two for the derived context, the
// flow, the buffer, the closure that its workers share, the yield function
// and the done channel that the first wait for room in the buffer creates.
const streamAllocs = 7

func TestStream(t *testing.T) {
	t.Parallel()

	t.Run("stops at an error from seq and returns it", func(t *testing.T) {
		t.Parallel()

		var err error
		assert.CompletesWithin(t, returnBound, func(ctx context.Context) error {
			err = task.Stream(ctx, 2, sequence(10, errBoom), func(context.Context, int) error { return nil })

			return err
		}, "Stream must return once its calls return")

		assert.ErrorIs(t, err, errBoom, "an error from seq must be the result")
	})

	t.Run("stops seq after a failure", func(t *testing.T) {
		t.Parallel()

		// Element 0 fails only once element 1 is running, and element 1
		// returns only once the failure has cancelled the context, so
		// neither worker drains the buffer before the failure. seq can
		// yield the two running elements, the two the buffer holds, one
		// that waits for room, and one more that the failure refuses.
		var (
			yielded  atomic.Int64
			err      error
			started1 = make(chan struct{})
		)
		seq := func(yield func(int, error) bool) {
			for i := range 1_000_000 {
				yielded.Add(1)
				if !yield(i, nil) {
					return
				}
			}
		}
		assert.CompletesWithin(t, returnBound, func(ctx context.Context) error {
			err = task.Stream(ctx, 2, seq, func(ctx context.Context, i int) error {
				switch i {
				case 0:
					<-started1

					return errBoom
				case 1:
					close(started1)

					return awaitCancel(ctx)
				default:
					return nil
				}
			})

			return err
		}, "Stream must return once its calls return")

		assert.ErrorIs(t, err, errBoom, "the failure must be the result")
		assert.InRange(t, yielded.Load(), 2, 6, "Stream must stop seq at the first yield after the failure")
	})

	t.Run("skips an element that a worker receives after the first error", func(t *testing.T) {
		t.Parallel()

		// The one worker fails element 0 only once seq has put element 1
		// into the buffer, so the worker receives element 1 after the
		// failure.
		buffered := make(chan struct{})
		seq := func(yield func(int, error) bool) {
			if yield(0, nil) && yield(1, nil) {
				close(buffered)
			}
		}

		var (
			calls atomic.Int32
			err   error
		)
		assert.CompletesWithin(t, returnBound, func(ctx context.Context) error {
			err = task.Stream(ctx, 1, seq, func(_ context.Context, i int) error {
				calls.Add(1)
				if i == 0 {
					<-buffered

					return errBoom
				}

				return nil
			})

			return err
		}, "Stream must return once its calls return")

		assert.ErrorIs(t, err, errBoom, "the failure must be the result")
		assert.Equal(t, calls.Load(), 1, "fn must not run for an element received after the failure")
	})

	t.Run("returns the cause for an element that a worker skips after ctx ended", func(t *testing.T) {
		t.Parallel()

		// The one worker ends ctx only once seq has put element 1 into the
		// buffer, so the worker receives element 1 after ctx ended, and
		// seq yields nothing after it.
		ctx, cancel := context.WithCancel(t.Context())
		buffered := make(chan struct{})
		seq := func(yield func(int, error) bool) {
			if yield(0, nil) && yield(1, nil) {
				close(buffered)
			}
		}

		var err error
		assert.CompletesWithin(t, returnBound, func(context.Context) error {
			err = task.Stream(ctx, 1, seq, func(_ context.Context, i int) error {
				if i == 0 {
					<-buffered
					cancel()
				}

				return nil
			})

			return err
		}, "Stream must return once its calls return")

		assert.ErrorIs(t, err, context.Canceled, "a skipped element must make Stream fail with the cause")
	})

	t.Run("returns the cause when the context is done while it waits for a worker", func(t *testing.T) {
		t.Parallel()

		release := make(chan struct{})
		time.AfterFunc(slowCancel, func() { close(release) })

		var err error
		assert.CompletesWithin(t, returnBound, func(ctx context.Context) error {
			// The only worker holds the first element until release, the
			// buffer holds the second, and the third waits in Stream until
			// the failure of the first cancels the context.
			err = task.Stream(ctx, 1, sequence(3, nil), func(_ context.Context, i int) error {
				if i == 0 {
					<-release

					return errBoom
				}

				return nil
			})

			return err
		}, "Stream must return once its calls return")

		assert.ErrorIs(t, err, errBoom, "the failure must be the result")
	})

	t.Run("returns ErrExited when its only worker exits", func(t *testing.T) {
		t.Parallel()

		var err error
		assert.CompletesWithin(t, returnBound, func(ctx context.Context) error {
			err = task.Stream(ctx, 1, sequence(3, nil), func(context.Context, int) error {
				runtime.Goexit()

				return nil
			})

			return err
		}, "Stream must return once its worker exits")

		assert.ErrorIs(t, err, task.ErrExited, "a worker that exits must fail the stream, not stall it")
	})

	t.Run("cancels and waits for the workers when seq panics, then lets the panic continue", func(t *testing.T) {
		t.Parallel()

		started := make(chan struct{})
		// seq panics only once fn is running, so the case observes the wait
		// and not an element skipped before it ran.
		seq := func(yield func(int, error) bool) {
			yield(0, nil)
			<-started
			panic("seq") //nolint:forbidigo // the case is about a sequence that panics.
		}

		var (
			recovered any
			cause     error
		)
		// CompletesWithin runs its function on a goroutine of its own, so the
		// function recovers the panic there.
		assert.CompletesWithin(t, returnBound, func(ctx context.Context) error {
			defer func() { recovered = recover() }()

			return task.Stream(ctx, 1, seq, func(ctx context.Context, _ int) error {
				close(started)
				cause = awaitCancel(ctx)

				return nil
			})
		}, "Stream must return once its worker returns")

		assert.Equal(t, recovered, any("seq"), "the panic of seq must reach the caller of Stream")
		assert.ErrorIs(t, cause, task.ErrExited, "the worker must be cancelled and finish before the panic continues")
	})

	t.Run("returns nil for an empty sequence without calling fn", func(t *testing.T) {
		t.Parallel()

		var runs atomic.Int32
		err := task.Stream(t.Context(), 4, sequence(0, nil), func(context.Context, int) error {
			runs.Add(1)

			return errBoom
		})

		assert.NoError(t, err, "an empty sequence must succeed")
		assert.Equal(t, runs.Load(), 0, "an empty sequence must not call fn")
	})
}

// TestStreamAllocs checks the allocation contract of Stream: a fixed
// number of allocations per call, whatever the number of elements.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
func TestStreamAllocs(t *testing.T) {
	ctx := t.Context()
	noop := func(context.Context, int) error { return nil }

	for _, n := range []int{64, 512} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			seq := sequence(n, nil)

			var err error
			expect.MaxAllocs(t, func() { err = task.Stream(ctx, 1, seq, noop) }, streamAllocs,
				"Stream must allocate a fixed amount, whatever the number of elements")
			assert.NoError(t, err, "the test must measure a call that succeeds")
		})
	}
}

// BenchmarkStream reports the cost of Stream over 1,024 elements, and fails
// when a call allocates more than its allocation contract allows.
func BenchmarkStream(b *testing.B) {
	seq := sequence(1024, nil)

	var sum atomic.Int64
	fn := func(_ context.Context, i int) error {
		sum.Add(int64(i))

		return nil
	}

	var err error

	c := bench.Start(b).Warmup(benchWarmup).MaxAllocs(streamAllocs)
	defer c.End()

	for c.Loop() {
		err = task.Stream(b.Context(), 8, seq, fn)
	}

	assert.NoError(b, err, "the benchmark must measure calls that succeed")
}
