// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package task_test

import (
	"context"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/task"
)

// slowCancel is how long a case waits before it cancels a group that
// another goroutine is waiting on. The waiter parks within microseconds,
// so the delay only makes sure the cancellation reaches a parked waiter.
const slowCancel = 50 * time.Millisecond

// Allocation counts of a Group.
const (
	// runAllocs is the number of allocations of one call of Run whose body
	// starts no task: the derived context, its state, the Group and its
	// semaphore.
	runAllocs = 4

	// goAllocs is the number of allocations of one call of Group.Go: the
	// closure that starts the task's goroutine.
	goAllocs = 2
)

func TestGroup(t *testing.T) {
	t.Parallel()

	t.Run("Run", func(t *testing.T) {
		t.Parallel()

		t.Run("waits for a task that a task started after body returned", func(t *testing.T) {
			t.Parallel()

			var (
				second atomic.Bool
				err    error
			)
			assert.CompletesWithin(t, returnBound, func(ctx context.Context) error {
				err = task.Run(ctx, 2, func(_ context.Context, g *task.Group) error {
					return g.Go(func(context.Context) error {
						// body has returned by the time this task starts the
						// next one.
						time.Sleep(5 * time.Millisecond)

						return g.Go(func(context.Context) error {
							time.Sleep(5 * time.Millisecond)
							second.Store(true)

							return nil
						})
					})
				})

				return err
			}, "Run must return once its tasks return")

			assert.NoError(t, err, "Run must succeed")
			assert.True(t, second.Load(), "Run must wait for the task started last")
		})

		t.Run("returns a failure of body and cancels the tasks with it", func(t *testing.T) {
			t.Parallel()

			var cause, err error
			assert.CompletesWithin(t, returnBound, func(ctx context.Context) error {
				err = task.Run(ctx, 1, func(_ context.Context, g *task.Group) error {
					if goErr := g.Go(func(ctx context.Context) error {
						cause = awaitCancel(ctx)

						return nil
					}); goErr != nil {
						return goErr
					}

					return errBoom
				})

				return err
			}, "Run must return once its tasks return")

			assert.ErrorIs(t, err, errBoom, "a failure of body must be the result")
			assert.ErrorIs(t, cause, errBoom, "the task must see the failure of body as the cause")
		})

		t.Run("cancels and waits for the tasks when body panics, then lets the panic continue", func(t *testing.T) {
			t.Parallel()

			var (
				recovered any
				cause     error
			)
			// CompletesWithin runs its function on a goroutine of its own,
			// so the function recovers the panic there.
			assert.CompletesWithin(t, returnBound, func(ctx context.Context) error {
				defer func() { recovered = recover() }()

				return task.Run(ctx, 1, func(_ context.Context, g *task.Group) error {
					started := make(chan struct{})
					_ = g.Go(func(ctx context.Context) error {
						close(started)
						cause = awaitCancel(ctx)

						return nil
					})
					<-started

					panic("body") //nolint:forbidigo // the case is about a body that panics.
				})
			}, "Run must return once its task returns")

			assert.Equal(t, recovered, any("body"), "the panic of body must reach the caller of Run")
			assert.ErrorIs(t, cause, task.ErrExited, "the task must be cancelled and finish before the panic continues")
		})

		t.Run("cancels and waits for the tasks when body calls runtime.Goexit", func(t *testing.T) {
			t.Parallel()

			// runtime.Goexit ends the goroutine that calls Run, so the case
			// runs Run on a goroutine whose deferred close reports the end.
			var cause error
			exited := make(chan struct{})
			go func() {
				defer close(exited)

				_ = task.Run(t.Context(), 1, func(_ context.Context, g *task.Group) error {
					started := make(chan struct{})
					_ = g.Go(func(ctx context.Context) error {
						close(started)
						cause = awaitCancel(ctx)

						return nil
					})
					<-started

					runtime.Goexit()

					return nil
				})
			}()

			assert.CompletesWithin(t, returnBound, func(ctx context.Context) error {
				select {
				case <-exited:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}, "the goroutine of Run must exit once its task returns")
			assert.ErrorIs(t, cause, task.ErrExited, "the task must be cancelled and finish before the goroutine exits")
		})
	})

	t.Run("Go", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the cause after a failure without starting fn", func(t *testing.T) {
			t.Parallel()

			var (
				ran   atomic.Bool
				goErr error
			)
			assert.CompletesWithin(t, returnBound, func(ctx context.Context) error {
				return task.Run(ctx, 1, func(ctx context.Context, g *task.Group) error {
					if err := g.Go(func(context.Context) error { return errBoom }); err != nil {
						return err
					}
					<-ctx.Done()

					goErr = g.Go(func(context.Context) error {
						ran.Store(true)

						return nil
					})

					return nil
				})
			}, "Run must return once its tasks return")

			assert.ErrorIs(t, goErr, errBoom, "Go must return the cause of the failed group")
			assert.False(t, ran.Load(), "Go must not start a task in a failed group")
		})

		t.Run("returns the cause when the group fails while it waits for a slot", func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(t.Context())
			t.Cleanup(cancel)

			var (
				ran   atomic.Bool
				goErr error
			)
			assert.CompletesWithin(t, returnBound, func(context.Context) error {
				return task.Run(ctx, 1, func(_ context.Context, g *task.Group) error {
					release := make(chan struct{})
					defer close(release)

					// The first task holds the only slot until Go below has
					// returned, so Go can only return by observing the
					// cancellation.
					if err := g.Go(func(ctx context.Context) error {
						<-ctx.Done()
						<-release

						return nil
					}); err != nil {
						return err
					}

					time.AfterFunc(slowCancel, cancel)
					goErr = g.Go(func(context.Context) error {
						ran.Store(true)

						return nil
					})

					return nil
				})
			}, "Run must return once its tasks return")

			assert.ErrorIs(t, goErr, context.Canceled, "a waiting Go must return when the group is cancelled")
			assert.False(t, ran.Load(), "a waiting Go must not start its task after the cancellation")
		})

		t.Run("makes Run fail when body ignores a refusal", func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(t.Context())

			var err error
			assert.CompletesWithin(t, returnBound, func(context.Context) error {
				err = task.Run(ctx, 1, func(_ context.Context, g *task.Group) error {
					cancel()
					_ = g.Go(func(context.Context) error { return nil })

					return nil
				})

				return err
			}, "Run must return once body returns")

			assert.ErrorIs(t, err, context.Canceled, "Run must not report success after a refused task")
		})

		t.Run("returns ErrClosed after Run has returned", func(t *testing.T) {
			t.Parallel()

			var (
				escaped *task.Group
				ran     atomic.Bool
			)
			assert.FailsAfterClose(t, func() error {
				return task.Run(t.Context(), 1, func(_ context.Context, g *task.Group) error {
					escaped = g

					return nil
				})
			}, func() error {
				return escaped.Go(func(context.Context) error {
					ran.Store(true)

					return nil
				})
			}, task.ErrClosed, "a group used after Run returned must refuse")
			assert.False(t, ran.Load(), "a closed group must not start a task")
		})
	})
}

// TestGroupAllocs checks the allocation contracts of Run and Group.Go.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
func TestGroupAllocs(t *testing.T) {
	ctx := t.Context()

	t.Run("Run", func(t *testing.T) {
		body := func(context.Context, *task.Group) error { return nil }

		var err error
		expect.MaxAllocs(t, func() { err = task.Run(ctx, 1, body) }, runAllocs,
			"Run must allocate its context, its state and its Group")
		assert.NoError(t, err, "the test must measure a call that succeeds")
	})

	t.Run("Go", func(t *testing.T) {
		noop := func(context.Context) error { return nil }

		var goErr error
		err := task.Run(ctx, runtime.GOMAXPROCS(0)*2, func(_ context.Context, g *task.Group) error {
			expect.MaxAllocs(t, func() { goErr = g.Go(noop) }, goAllocs,
				"Go must allocate only the closure that starts the task")

			return nil
		})
		assert.NoError(t, goErr, "the test must measure tasks that start")
		assert.NoError(t, err, "the test must measure tasks that succeed")
	})
}

// BenchmarkGroup reports the cost of Run and Group.Go, and fails when a
// call allocates more than its allocation contract allows.
func BenchmarkGroup(b *testing.B) {
	b.Run("Run", func(b *testing.B) {
		body := func(context.Context, *task.Group) error { return nil }

		var err error

		c := bench.Start(b).Warmup(benchWarmup).MaxAllocs(runAllocs)
		defer c.End()

		for c.Loop() {
			err = task.Run(b.Context(), 1, body)
		}

		assert.NoError(b, err, "the benchmark must measure calls that succeed")
	})

	b.Run("Go", func(b *testing.B) {
		var sum atomic.Int64
		fn := func(ctx context.Context) error {
			sum.Add(1)

			return ctx.Err()
		}

		var goErr error
		err := task.Run(b.Context(), runtime.GOMAXPROCS(0)*2, func(_ context.Context, g *task.Group) error {
			c := bench.Start(b).Warmup(benchWarmup).MaxAllocs(goAllocs)
			defer c.End()

			for c.Loop() {
				goErr = g.Go(fn)
			}

			return nil
		})

		assert.NoError(b, goErr, "the benchmark must measure tasks that start")
		assert.NoError(b, err, "the benchmark must measure tasks that succeed")
	})
}
