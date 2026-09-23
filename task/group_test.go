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

// slowCancel is how long a test waits before it cancels a group that
// another goroutine is waiting on. The waiter parks within
// microseconds, so the delay only makes sure the cancellation reaches
// a parked waiter.
const slowCancel = 50 * time.Millisecond

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
			within(t, func() {
				err = task.Run(t.Context(), 2, func(_ context.Context, g *task.Group) error {
					return g.Go(func(context.Context) error {
						// body has returned by the time this task starts
						// the next one.
						time.Sleep(5 * time.Millisecond)

						return g.Go(func(context.Context) error {
							time.Sleep(5 * time.Millisecond)
							second.Store(true)

							return nil
						})
					})
				})
			})

			testkit.NoError(t, err, "Run must succeed")
			testkit.True(t, second.Load(), "Run must wait for the task started last")
		})

		t.Run("returns a failure of body and cancels the tasks with it", func(t *testing.T) {
			t.Parallel()

			var (
				cause error
				err   error
			)
			within(t, func() {
				err = task.Run(t.Context(), 1, func(_ context.Context, g *task.Group) error {
					if goErr := g.Go(func(ctx context.Context) error {
						cause = awaitCancel(ctx)

						return nil
					}); goErr != nil {
						return goErr
					}

					return errBoom
				})
			})

			testkit.ErrorIs(t, err, errBoom, "a failure of body must be the result")
			testkit.ErrorIs(t, cause, errBoom, "the task must see the failure of body as the cause")
		})

		t.Run("cancels and waits for the tasks when body panics, then lets the panic continue", func(t *testing.T) {
			t.Parallel()

			var (
				recovered any
				cause     error
			)
			within(t, func() {
				defer func() { recovered = recover() }()

				_ = task.Run(t.Context(), 1, func(_ context.Context, g *task.Group) error {
					started := make(chan struct{})
					_ = g.Go(func(ctx context.Context) error {
						close(started)
						cause = awaitCancel(ctx)

						return nil
					})
					<-started

					panic("body") //nolint:forbidigo // the test is about a body that panics.
				})
			})

			testkit.Equal(t, recovered, any("body"), "the panic of body must reach the caller of Run")
			testkit.ErrorIs(t, cause, task.ErrExited,
				"the task must be cancelled and finish before the panic continues")
		})

		t.Run("cancels and waits for the tasks when body calls runtime.Goexit", func(t *testing.T) {
			t.Parallel()

			var cause error
			within(t, func() {
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
			})

			testkit.ErrorIs(
				t,
				cause,
				task.ErrExited,
				"the task must be cancelled and finish before the goroutine exits",
			)
		})
	})

	t.Run("Go", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the cause after a failure and does not start fn", func(t *testing.T) {
			t.Parallel()

			var (
				ran   atomic.Bool
				goErr error
			)
			within(t, func() {
				_ = task.Run(t.Context(), 1, func(ctx context.Context, g *task.Group) error {
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
			})

			testkit.ErrorIs(t, goErr, errBoom, "Go must return the cause of the failed group")
			testkit.False(t, ran.Load(), "Go must not start a task in a failed group")
		})

		t.Run("returns the cause when the group fails while it waits for a slot", func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(t.Context())
			t.Cleanup(cancel)

			var (
				ran   atomic.Bool
				goErr error
			)
			within(t, func() {
				_ = task.Run(ctx, 1, func(_ context.Context, g *task.Group) error {
					release := make(chan struct{})
					defer close(release)

					// The first task holds the only slot until Go below
					// has returned, so Go can only return by observing
					// the cancellation.
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
			})

			testkit.ErrorIs(t, goErr, context.Canceled, "a waiting Go must return when the group is cancelled")
			testkit.False(t, ran.Load(), "a waiting Go must not start its task after the cancellation")
		})

		t.Run("records a refusal, so Run fails when body ignores it", func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(t.Context())

			var err error
			within(t, func() {
				err = task.Run(ctx, 1, func(_ context.Context, g *task.Group) error {
					cancel()
					_ = g.Go(func(context.Context) error { return nil })

					return nil
				})
			})

			testkit.ErrorIs(t, err, context.Canceled, "Run must not report success after a refused task")
		})

		t.Run("returns ErrClosed after Run has returned", func(t *testing.T) {
			t.Parallel()

			var escaped *task.Group
			within(t, func() {
				_ = task.Run(t.Context(), 1, func(_ context.Context, g *task.Group) error {
					escaped = g

					return nil
				})
			})

			var ran atomic.Bool
			err := escaped.Go(func(context.Context) error {
				ran.Store(true)

				return nil
			})

			testkit.ErrorIs(t, err, task.ErrClosed, "a group used after Run returned must refuse")
			testkit.False(t, ran.Load(), "a closed group must not start a task")
		})
	})
}

func BenchmarkGroupGo(b *testing.B) {
	b.ReportAllocs()

	var sink atomic.Int64
	_ = task.Run(b.Context(), runtime.GOMAXPROCS(0)*2, func(_ context.Context, g *task.Group) error {
		for b.Loop() {
			_ = g.Go(func(context.Context) error {
				sink.Add(1)

				return nil
			})
		}

		return nil
	})
}
