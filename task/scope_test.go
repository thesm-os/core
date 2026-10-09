// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package task_test

import (
	"context"
	"errors"
	"iter"
	"math"
	"runtime"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/task"
)

// returnBound is how long a case lets a call of the package run before
// it fails. Every function in the package blocks until its tasks return,
// so a lost release hangs the call. The bound turns that hang into a
// named failure, and a correct call returns in microseconds.
const returnBound = time.Second

// benchWarmup is the number of iterations that a benchmark runs before it
// measures. They start the goroutines whose state the runtime reuses
// later, so the measured iterations count only what a call allocates.
const benchWarmup = 100

// The errors of the tasks of the cases.
var (
	// errBoom is the error of a task that failed.
	errBoom = errors.New("task_test: boom")

	// errLate is the error of a task that fails after the first failure
	// cancelled it.
	errLate = errors.New("task_test: failed after the cancellation")

	// errStuck is returned by a task that waited a second for a
	// cancellation that never came, so a broken cancellation fails the
	// test instead of hanging it.
	errStuck = errors.New("task_test: the context was never cancelled")
)

// peak tracks the number of tasks running at once and the highest
// number observed.
type peak struct {
	running atomic.Int64
	max     atomic.Int64
}

// hold counts one task as running for a millisecond, long enough for the
// tasks of one call to overlap.
func (p *peak) hold() {
	n := p.running.Add(1)
	for {
		m := p.max.Load()
		if n <= m || p.max.CompareAndSwap(m, n) {
			break
		}
	}

	time.Sleep(time.Millisecond)
	p.running.Add(-1)
}

// entry runs n tasks through one function of the package. fn is the
// task, and it receives the task's index.
type entry struct {
	run     func(ctx context.Context, limit, n int, fn func(ctx context.Context, i int) error) error
	name    string
	limited bool // the function takes a limit
}

// ctxKey carries a value through the parent context.
type ctxKey struct{}

func TestScope(t *testing.T) {
	t.Parallel()

	for _, e := range entries() {
		t.Run(e.name, func(t *testing.T) {
			t.Parallel()

			t.Run("returns nil after running every task once", func(t *testing.T) {
				t.Parallel()

				runs := make([]atomic.Int32, 500)
				var err error
				assert.CompletesWithin(t, returnBound, func(ctx context.Context) error {
					err = e.run(ctx, 8, len(runs), func(_ context.Context, i int) error {
						runs[i].Add(1)

						return nil
					})

					return err
				}, "the call must return once its tasks return")

				assert.NoError(t, err, "tasks that all succeed must make the call succeed")
				got := make([]int32, len(runs))
				for i := range runs {
					got[i] = runs[i].Load()
				}
				assert.Equal(t, got, slices.Repeat([]int32{1}, len(runs)), "every task must run exactly once")
			})

			t.Run("cancels the other tasks with the first error", func(t *testing.T) {
				t.Parallel()

				var cause, err error
				// Task 1 fails only after task 0 has started, so task 0 is
				// running when the failure cancels it.
				started := make(chan struct{})
				assert.CompletesWithin(t, returnBound, func(ctx context.Context) error {
					err = e.run(ctx, 2, 2, func(ctx context.Context, i int) error {
						if i == 1 {
							<-started

							return errBoom
						}
						close(started)
						cause = awaitCancel(ctx)

						return cause
					})

					return err
				}, "the call must return once its tasks return")

				assert.ErrorIs(t, err, errBoom, "the first error must be the result")
				assert.ErrorIs(t, cause, errBoom, "the other task must see the first error as the cause")
			})

			t.Run("returns the first error when another task fails after the cancellation", func(t *testing.T) {
				t.Parallel()

				var err error
				started := make(chan struct{})
				assert.CompletesWithin(t, returnBound, func(ctx context.Context) error {
					err = e.run(ctx, 2, 2, func(ctx context.Context, i int) error {
						if i == 1 {
							<-started

							return errBoom
						}
						close(started)
						_ = awaitCancel(ctx)

						return errLate
					})

					return err
				}, "the call must return once its tasks return")

				assert.That(t, err).
					ErrorIs(errBoom, "the first error must be the result").
					ErrorIsNot(errLate, "an error after the cancellation must be discarded")
			})

			t.Run("records ErrExited for a task that calls runtime.Goexit", func(t *testing.T) {
				t.Parallel()

				var err error
				assert.CompletesWithin(t, returnBound, func(ctx context.Context) error {
					err = e.run(ctx, 1, 1, func(context.Context, int) error {
						runtime.Goexit()

						return nil
					})

					return err
				}, "the call must return once its task exits")

				assert.ErrorIs(t, err, task.ErrExited, "a task that did not return must not count as a success")
			})

			t.Run("derives the context of every task from ctx", func(t *testing.T) {
				t.Parallel()

				deadline := time.Now().Add(time.Hour)
				ctx, cancel := context.WithDeadline(context.WithValue(t.Context(), ctxKey{}, "span"), deadline)
				t.Cleanup(cancel)

				var (
					value any
					got   time.Time
				)
				assert.CompletesWithin(t, returnBound, func(context.Context) error {
					return e.run(ctx, 1, 1, func(ctx context.Context, _ int) error {
						value = ctx.Value(ctxKey{})
						got, _ = ctx.Deadline()

						return nil
					})
				}, "the call must return once its task returns")

				assert.Equal(t, value, any("span"), "a task must see the values of ctx")
				assert.True(t, got.Equal(deadline), "a task must see the deadline of ctx")
			})

			t.Run("cancels the context of its tasks when it returns", func(t *testing.T) {
				t.Parallel()

				// The task keeps the Err method of its context, which the case
				// calls after the call has returned. The call runs under the
				// test's context, which outlives the call, so only the call can
				// have cancelled the task's context.
				parent := t.Context()
				var errOf func() error
				assert.CompletesWithin(t, returnBound, func(context.Context) error {
					return e.run(parent, 1, 1, func(ctx context.Context, _ int) error {
						errOf = ctx.Err

						return nil
					})
				}, "the call must return once its task returns")

				assert.ErrorIs(t, errOf(), context.Canceled, "no task context may outlive the call")
			})

			t.Run("returns the cause of a cancelled ctx without running a task", func(t *testing.T) {
				t.Parallel()

				var runs atomic.Int32
				assert.HonoursCancellation(t, func(ctx context.Context) error {
					return e.run(ctx, 2, 3, func(context.Context, int) error {
						runs.Add(1)

						return nil
					})
				}, "skipped tasks must make the call fail with the cancellation")
				assert.Equal(t, runs.Load(), 0, "no task may start under a cancelled ctx")
			})

			t.Run("returns the cause of a passed deadline without running a task", func(t *testing.T) {
				t.Parallel()

				var runs atomic.Int32
				assert.HonoursDeadline(t, func(ctx context.Context) error {
					return e.run(ctx, 2, 3, func(context.Context, int) error {
						runs.Add(1)

						return nil
					})
				}, "skipped tasks must make the call fail with the deadline")
				assert.Equal(t, runs.Load(), 0, "no task may start under a passed deadline")
			})

			if !e.limited {
				return
			}

			t.Run("runs limit tasks at once", func(t *testing.T) {
				t.Parallel()

				var (
					p   peak
					err error
				)
				assert.CompletesWithin(t, returnBound, func(ctx context.Context) error {
					err = e.run(ctx, 3, 60, func(context.Context, int) error {
						p.hold()

						return nil
					})

					return err
				}, "the call must return once its tasks return")

				assert.NoError(t, err, "the call must succeed")
				assert.Equal(t, p.max.Load(), 3, "the tasks must fill the limit and stay within it")
			})

			t.Run("runs one task at a time at a limit of one", func(t *testing.T) {
				t.Parallel()

				var (
					p   peak
					err error
				)
				assert.CompletesWithin(t, returnBound, func(ctx context.Context) error {
					err = e.run(ctx, 1, 5, func(context.Context, int) error {
						p.hold()

						return nil
					})

					return err
				}, "the call must return once its tasks return")

				assert.NoError(t, err, "a limit of one must be accepted")
				assert.Equal(t, p.max.Load(), 1, "a limit of one must serialise the tasks")
			})

			t.Run("returns ErrLimit for a limit below one", func(t *testing.T) {
				t.Parallel()

				var runs atomic.Int32
				prop.ErrorIs(t, func(limit int) error {
					return e.run(t.Context(), limit, 3, func(context.Context, int) error {
						runs.Add(1)

						return nil
					})
				}, task.ErrLimit, "a limit below one must be refused",
					prop.Using(prop.Integer(math.MinInt, 0)), prop.Example(0), prop.Example(-1))
				assert.Equal(t, runs.Load(), 0, "a refused call must not run a task")
				assert.Equal(t, errs.Classify(task.ErrLimit), errs.Invalid, "ErrLimit must classify as Invalid")
			})
		})
	}
}

// awaitCancel waits for ctx to be done and returns its cause, or returns
// errStuck after a second.
func awaitCancel(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-time.After(time.Second):
		return errStuck
	}
}

// indices returns the integers from 0 to n-1.
func indices(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}

	return out
}

// sequence yields the integers from 0 to n-1, then err if err is not nil.
func sequence(n int, err error) iter.Seq2[int, error] {
	return func(yield func(int, error) bool) {
		for i := range n {
			if !yield(i, nil) {
				return
			}
		}

		if err != nil {
			yield(0, err)
		}
	}
}

// entries returns every function of the package that fans out as an
// entry, so a rule that applies to all of them is tested against each.
// Quorum runs with k equal to the number of tasks, where it succeeds only
// when every task does.
func entries() []entry {
	return []entry{
		{
			name:    "Run",
			limited: true,
			run: func(ctx context.Context, limit, n int, fn func(ctx context.Context, i int) error) error {
				return task.Run(ctx, limit, func(_ context.Context, g *task.Group) error {
					for i := range n {
						if err := g.Go(func(ctx context.Context) error { return fn(ctx, i) }); err != nil {
							return err
						}
					}

					return nil
				})
			},
		},
		{
			name:    "Each",
			limited: true,
			run: func(ctx context.Context, limit, n int, fn func(ctx context.Context, i int) error) error {
				return task.Each(ctx, limit, indices(n), func(ctx context.Context, i, _ int) error {
					return fn(ctx, i)
				})
			},
		},
		{
			name:    "Map",
			limited: true,
			run: func(ctx context.Context, limit, n int, fn func(ctx context.Context, i int) error) error {
				_, err := task.Map(ctx, limit, indices(n), func(ctx context.Context, i int) (int, error) {
					return i, fn(ctx, i)
				})

				return err
			},
		},
		{
			name:    "Stream",
			limited: true,
			run: func(ctx context.Context, limit, n int, fn func(ctx context.Context, i int) error) error {
				return task.Stream(ctx, limit, sequence(n, nil), fn)
			},
		},
		{
			name:    "Quorum",
			limited: true,
			run: func(ctx context.Context, limit, n int, fn func(ctx context.Context, i int) error) error {
				return task.Quorum(ctx, limit, n, indices(n), func(ctx context.Context, i, _ int) error {
					return fn(ctx, i)
				})
			},
		},
		{
			name: "All",
			run: func(ctx context.Context, _, n int, fn func(ctx context.Context, i int) error) error {
				fns := make([]func(ctx context.Context) error, n)
				for i := range fns {
					fns[i] = func(ctx context.Context) error { return fn(ctx, i) }
				}

				return task.All(ctx, fns...)
			},
		},
	}
}
