// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package task_test

import (
	"context"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/task"
)

func TestStream(t *testing.T) {
	t.Parallel()

	t.Run("stops at an error from seq and returns it", func(t *testing.T) {
		t.Parallel()

		var err error
		within(t, func() {
			err = task.Stream(t.Context(), 2, sequence(10, errBoom), func(context.Context, int) error { return nil })
		})

		testkit.ErrorIs(t, err, errBoom, "an error from seq must be the result")
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
		within(t, func() {
			err = task.Stream(t.Context(), 2, seq, func(ctx context.Context, i int) error {
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
		})

		testkit.ErrorIs(t, err, errBoom, "the failure must be the result")
		testkit.True(t, yielded.Load() <= 6, "Stream must stop seq at the first yield after the failure")
	})

	t.Run("returns the cause when the context is done while it waits for a worker", func(t *testing.T) {
		t.Parallel()

		release := make(chan struct{})
		time.AfterFunc(slowCancel, func() { close(release) })

		var err error
		within(t, func() {
			// The only worker holds the first element until release, the
			// buffer holds the second, and the third waits in Stream
			// until the failure of the first cancels the context.
			err = task.Stream(t.Context(), 1, sequence(3, nil), func(_ context.Context, i int) error {
				if i == 0 {
					<-release

					return errBoom
				}

				return nil
			})
		})

		testkit.ErrorIs(t, err, errBoom, "the failure must be the result")
	})

	t.Run("returns ErrExited when its only worker exits", func(t *testing.T) {
		t.Parallel()

		var err error
		within(t, func() {
			err = task.Stream(t.Context(), 1, sequence(3, nil), func(context.Context, int) error {
				runtime.Goexit()

				return nil
			})
		})

		testkit.ErrorIs(t, err, task.ErrExited, "a worker that exits must fail the stream, not stall it")
	})

	t.Run("cancels and waits for the workers when seq panics, then lets the panic continue", func(t *testing.T) {
		t.Parallel()

		started := make(chan struct{})
		// seq panics only once fn is running, so the test observes the
		// wait and not an element skipped before it ran.
		seq := func(yield func(int, error) bool) {
			yield(0, nil)
			<-started
			panic("seq") //nolint:forbidigo // the test is about a sequence that panics.
		}

		var (
			recovered any
			cause     error
		)
		within(t, func() {
			defer func() { recovered = recover() }()

			_ = task.Stream(t.Context(), 1, seq, func(ctx context.Context, _ int) error {
				close(started)
				cause = awaitCancel(ctx)

				return nil
			})
		})

		testkit.Equal(t, recovered, any("seq"), "the panic of seq must reach the caller of Stream")
		testkit.ErrorIs(t, cause, task.ErrExited, "the worker must be cancelled and finish before the panic continues")
	})

	t.Run("returns nil for an empty sequence without calling fn", func(t *testing.T) {
		t.Parallel()

		var (
			runs atomic.Int32
			err  error
		)
		within(t, func() {
			err = task.Stream(t.Context(), 4, sequence(0, nil), func(context.Context, int) error {
				runs.Add(1)

				return errBoom
			})
		})

		testkit.NoError(t, err, "an empty sequence must succeed")
		testkit.Equal(t, runs.Load(), int32(0), "an empty sequence must not call fn")
	})
}

func BenchmarkStream(b *testing.B) {
	b.ReportAllocs()

	var sink atomic.Int64
	seq := sequence(b.N, nil)

	b.ResetTimer()
	_ = task.Stream(b.Context(), runtime.GOMAXPROCS(0)*2, seq, func(_ context.Context, i int) error {
		sink.Add(int64(i))

		return nil
	})
}
