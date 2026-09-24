// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package task_test

import (
	"context"
	"iter"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/task"
)

// errBoom is the error of a task that failed.
var errBoom = testkit.TestError("boom")

// errStuck is returned by a task that waited a second for a
// cancellation that never came, so a broken cancellation fails the
// test instead of hanging it.
var errStuck = testkit.TestError("the context was never cancelled")

// within runs call on a new goroutine and fails the test when call has
// not returned within a second.
//
// Every function in the package blocks until its tasks return, so a
// lost release hangs the call instead of failing it. The deadline turns
// that hang into a named failure well inside the per-mutant budget of
// mutation testing, and a correct call returns in microseconds.
func within(t *testing.T, call func()) {
	t.Helper()

	done := make(chan struct{})
	go func() {
		defer close(done)
		call()
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("the call did not return within a second")
	}
}

// awaitCancel waits for ctx to be done and returns its cause, or
// returns errStuck after a second.
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

// sequence yields the integers from 0 to n-1, then err if err is not
// nil.
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

// peak tracks the number of tasks running at once and the highest
// number observed.
type peak struct {
	running atomic.Int64
	max     atomic.Int64
}

// hold counts one task as running for a millisecond, long enough for
// the tasks of one call to overlap.
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

// entries returns every function of the package that fans out as an
// entry, so a rule that applies to all of them is tested against each.
// Quorum runs with k equal to the number of tasks, where it succeeds
// only when every task does.
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

// ctxKey carries a value through the parent context.
type ctxKey struct{}

func TestScope(t *testing.T) {
	t.Parallel()

	for _, e := range entries() {
		t.Run(e.name, func(t *testing.T) {
			t.Parallel()

			t.Run("runs every task once and returns nil", func(t *testing.T) {
				t.Parallel()

				runs := make([]atomic.Int32, 500)
				var err error
				within(t, func() {
					err = e.run(t.Context(), 8, len(runs), func(_ context.Context, i int) error {
						runs[i].Add(1)

						return nil
					})
				})

				testkit.NoError(t, err, "tasks that all succeed must make the call succeed")
				for i := range runs {
					testkit.Equal(t, runs[i].Load(), int32(1), "task "+strconv.Itoa(i)+" must run exactly once")
				}
			})

			t.Run("returns the first error and cancels the other tasks with it", func(t *testing.T) {
				t.Parallel()

				var (
					cause error
					err   error
				)
				within(t, func() {
					err = e.run(t.Context(), 2, 2, func(ctx context.Context, i int) error {
						if i == 1 {
							return errBoom
						}
						cause = awaitCancel(ctx)

						return cause
					})
				})

				testkit.ErrorIs(t, err, errBoom, "the first error must be the result")
				testkit.ErrorIs(t, cause, errBoom, "the other task must see the first error as the cause")
			})

			t.Run("records ErrExited for a task that calls runtime.Goexit", func(t *testing.T) {
				t.Parallel()

				var err error
				within(t, func() {
					err = e.run(t.Context(), 1, 1, func(context.Context, int) error {
						runtime.Goexit()

						return nil
					})
				})

				testkit.ErrorIs(t, err, task.ErrExited, "a task that did not return must not count as a success")
			})

			t.Run("passes the values and the deadline of ctx to every task", func(t *testing.T) {
				t.Parallel()

				deadline := time.Now().Add(time.Hour)
				ctx, cancel := context.WithDeadline(context.WithValue(t.Context(), ctxKey{}, "span"), deadline)
				t.Cleanup(cancel)

				var (
					value any
					got   time.Time
				)
				within(t, func() {
					_ = e.run(ctx, 1, 1, func(ctx context.Context, _ int) error {
						value = ctx.Value(ctxKey{})
						got, _ = ctx.Deadline()

						return nil
					})
				})

				testkit.Equal(t, value, any("span"), "a task must see the values of ctx")
				testkit.True(t, got.Equal(deadline), "a task must see the deadline of ctx")
			})

			t.Run("cancels the context of its tasks when it returns", func(t *testing.T) {
				t.Parallel()

				var done <-chan struct{}
				within(t, func() {
					_ = e.run(t.Context(), 1, 1, func(ctx context.Context, _ int) error {
						done = ctx.Done()

						return nil
					})
				})

				select {
				case <-done:
				default:
					t.Fatal("no task context may outlive the call")
				}
			})

			t.Run("returns the cause of a cancelled ctx without running a task", func(t *testing.T) {
				t.Parallel()

				ctx, cancel := context.WithCancel(t.Context())
				cancel()

				var (
					runs atomic.Int32
					err  error
				)
				within(t, func() {
					err = e.run(ctx, 2, 3, func(context.Context, int) error {
						runs.Add(1)

						return nil
					})
				})

				testkit.ErrorIs(t, err, context.Canceled, "skipped tasks must make the call fail")
				testkit.Equal(t, runs.Load(), int32(0), "no task may start under a cancelled ctx")
			})

			if !e.limited {
				return
			}

			t.Run("runs limit tasks at once and never more", func(t *testing.T) {
				t.Parallel()

				var p peak
				var err error
				within(t, func() {
					err = e.run(t.Context(), 3, 60, func(context.Context, int) error {
						p.hold()

						return nil
					})
				})

				testkit.NoError(t, err, "the call must succeed")
				testkit.Equal(t, p.max.Load(), int64(3), "the tasks must fill the limit and stay within it")
			})

			t.Run("runs one task at a time at a limit of one", func(t *testing.T) {
				t.Parallel()

				var p peak
				var err error
				within(t, func() {
					err = e.run(t.Context(), 1, 5, func(context.Context, int) error {
						p.hold()

						return nil
					})
				})

				testkit.NoError(t, err, "a limit of one must be accepted")
				testkit.Equal(t, p.max.Load(), int64(1), "a limit of one must serialise the tasks")
			})

			for _, limit := range []int{0, -1} {
				t.Run("rejects a limit of "+strconv.Itoa(limit)+" without running a task", func(t *testing.T) {
					t.Parallel()

					var runs atomic.Int32
					err := e.run(t.Context(), limit, 3, func(context.Context, int) error {
						runs.Add(1)

						return nil
					})

					testkit.ErrorIs(t, err, task.ErrLimit, "a limit below one must be rejected")
					testkit.Equal(t, runs.Load(), int32(0), "a rejected call must not run a task")
				})
			}
		})
	}
}
