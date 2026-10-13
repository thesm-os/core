// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package task_test

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/task"
)

// The ways in which a function of the property over Serve returns.
const (
	// kindFail returns the error of the function once the driver releases
	// it.
	kindFail = "fail"

	// kindEarly returns nil once the driver releases it, before its
	// context has ended unless an earlier return ended it.
	kindEarly = "early"

	// kindWait returns the cause of its context once its context ends.
	kindWait = "wait"
)

// failFormat is the text of the error of a function of the property that
// fails, with the index of the function.
const failFormat = "task_test: function %d failed"

// errCause is the cause with which a case ends the context of a call.
var errCause = errors.New("task_test: the service stopped")

// serveEntry runs n tasks through Serve, for the crash test.
var serveEntry = entry{
	name: "Serve",
	run: func(ctx context.Context, _, n int, fn func(ctx context.Context, i int) error) error {
		fns := make([]func(ctx context.Context) error, n)
		for i := range fns {
			fns[i] = func(ctx context.Context) error { return fn(ctx, i) }
		}

		return task.Serve(ctx, fns...)
	},
}

func TestServe(t *testing.T) {
	t.Parallel()

	t.Run("returns nil when ctx ends", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		var started atomic.Int32
		loop := func(ctx context.Context) error {
			if started.Add(1) == 2 {
				cancel()
			}
			<-ctx.Done()

			return nil
		}

		var err error
		assert.CompletesWithin(t, returnBound, func(context.Context) error {
			err = task.Serve(ctx, []func(context.Context) error{loop, loop}...)

			return err
		}, "Serve must return once its functions return")

		assert.NoError(t, err, "the end of ctx must stop Serve with nil")
	})

	t.Run("calls every function when ctx ended before the call", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		var calls atomic.Int32
		loop := func(ctx context.Context) error {
			calls.Add(1)
			<-ctx.Done()

			return nil
		}

		assert.NoError(t, task.Serve(ctx, []func(context.Context) error{loop, loop}...),
			"an ended ctx must stop Serve with nil")
		assert.Equal(t, calls.Load(), 2, "Serve must call every function")
	})

	stopped := []struct {
		name string
		give func(ctx context.Context) error
	}{
		{
			name: "returns nil for a function that returns the error of its ended context",
			give: func(ctx context.Context) error {
				<-ctx.Done()

				return ctx.Err()
			},
		},
		{
			name: "returns nil for a function that returns the cause of its ended context",
			give: func(ctx context.Context) error {
				<-ctx.Done()

				return context.Cause(ctx)
			},
		},
	}
	for _, tt := range stopped {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancelCause(t.Context())
			cancel(errCause)
			assert.NoError(t, task.Serve(ctx, []func(context.Context) error{tt.give, tt.give}...),
				"a call stopped by the end of ctx must not fail")
		})
	}

	t.Run("returns the error of the first function that fails", func(t *testing.T) {
		t.Parallel()

		var cause, err error
		assert.CompletesWithin(t, returnBound, func(ctx context.Context) error {
			err = task.Serve(ctx,
				func(context.Context) error { return errBoom },
				func(ctx context.Context) error {
					cause = awaitCancel(ctx)

					return cause
				})

			return err
		}, "Serve must return once its functions return")

		assert.ErrorIs(t, err, errBoom, "the first error must be the result")
		assert.ErrorIs(t, cause, errBoom, "the other function must see the first error as the cause")
	})

	t.Run("returns ErrReturned for a function that returns nil before its context ends", func(t *testing.T) {
		t.Parallel()

		var cause, err error
		assert.CompletesWithin(t, returnBound, func(ctx context.Context) error {
			err = task.Serve(ctx,
				func(ctx context.Context) error {
					cause = awaitCancel(ctx)

					return nil
				},
				func(context.Context) error { return nil })

			return err
		}, "Serve must return once its functions return")

		assert.ErrorIs(t, err, task.ErrReturned, "an early nil return must fail Serve")
		assert.Equal(t, err.Error(), task.ErrReturned.Error()+": function 1",
			"the error must name the index of the function")
		assert.ErrorIs(t, cause, task.ErrReturned, "the other function must see the early return as the cause")
		assert.Equal(t, errs.Classify(task.ErrReturned), errs.Invalid, "ErrReturned must classify as Invalid")
	})

	t.Run("returns the first error when another function fails after the cancellation", func(t *testing.T) {
		t.Parallel()

		var err error
		assert.CompletesWithin(t, returnBound, func(ctx context.Context) error {
			err = task.Serve(ctx,
				func(context.Context) error { return errBoom },
				func(ctx context.Context) error {
					_ = awaitCancel(ctx)

					return errLate
				})

			return err
		}, "Serve must return once its functions return")

		assert.That(t, err).
			ErrorIs(errBoom, "the first error must be the result").
			ErrorIsNot(errLate, "an error after the cancellation must be discarded")
	})

	t.Run("returns after every call has returned", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			release := make(chan struct{})
			done := make(chan struct{})
			var err error
			go func() {
				err = task.Serve(t.Context(),
					func(context.Context) error { return errBoom },
					func(context.Context) error {
						<-release

						return nil
					})
				close(done)
			}()

			synctest.Wait()
			early := false
			select {
			case <-done:
				early = true
			default:
			}
			close(release)
			<-done

			assert.False(t, early, "Serve must not return while a call runs")
			assert.ErrorIs(t, err, errBoom, "the first error must be the result")
		})
	})

	t.Run("records ErrExited for a function that calls runtime.Goexit", func(t *testing.T) {
		t.Parallel()

		var err error
		assert.CompletesWithin(t, returnBound, func(ctx context.Context) error {
			err = task.Serve(ctx, func(context.Context) error {
				runtime.Goexit()

				return nil
			})

			return err
		}, "Serve must return once its function exits")

		assert.ErrorIs(t, err, task.ErrExited, "a function that did not return must not count as a success")
	})

	t.Run("returns nil for no functions", func(t *testing.T) {
		t.Parallel()
		assert.NoError(t, task.Serve(t.Context()), "Serve with no functions must succeed")
	})

	// The driver releases the functions that do not wait for their context
	// one at a time, in a drawn order, and waits for each to return before
	// it releases the next. After the first, it also waits for the end of
	// the context of the calls, which Serve ends once it has recorded that
	// return. The first function of the order is then the first return that
	// Serve records.
	t.Run("returns the outcome of the function that returns first", func(t *testing.T) {
		t.Parallel()
		prop.ForAll(t, "Serve must return the outcome of the first return", func(c *prop.Case) {
			n := c.Draw(prop.Integer(1, 5), "functions")
			kinds := c.Draw(prop.List(prop.SampledFrom(kindFail, kindEarly, kindWait),
				prop.MinSize(n), prop.MaxSize(n)), "kinds")

			var active []int
			for i, k := range kinds {
				if k != kindWait {
					active = append(active, i)
				}
			}

			order := c.Draw(prop.Permutation(active...), "order")

			ctx, cancel := context.WithCancel(c.Context())
			defer cancel()

			release, returned := make([]chan struct{}, n), make([]chan struct{}, n)
			ended, errOf := make([]<-chan struct{}, n), make([]func() error, n)
			fns := make([]func(ctx context.Context) error, n)
			for i, k := range kinds {
				release[i], returned[i] = make(chan struct{}), make(chan struct{})
				fns[i] = func(ctx context.Context) error {
					defer close(returned[i])
					ended[i], errOf[i] = ctx.Done(), ctx.Err
					if k == kindWait {
						<-ctx.Done()

						return context.Cause(ctx)
					}
					<-release[i]
					if k == kindFail {
						return fmt.Errorf(failFormat, i)
					}

					return nil
				}
			}

			done := make(chan error, 1)
			go func() { done <- task.Serve(ctx, fns...) }()

			for j, i := range order {
				close(release[i])
				<-returned[i]
				if j == 0 {
					<-ended[i]
				}
			}

			if len(order) == 0 {
				cancel()
			}

			err := <-done
			assert.Total(c, func(i int) error {
				if errOf[i]() == nil {
					return fmt.Errorf("the context of function %d has not ended", i)
				}

				return nil
			}, indices(n), "the context of every call must have ended when Serve returns")

			if len(order) == 0 {
				assert.NoError(c, err, "Serve must return nil when every function waited for ctx")

				return
			}

			first := order[0]
			want := fmt.Sprintf(failFormat, first)
			if kinds[first] == kindEarly {
				assert.ErrorIs(c, err, task.ErrReturned, "an early nil return must fail Serve")
				want = fmt.Sprintf("%v: function %d", task.ErrReturned, first)
			}

			assert.HasError(c, err, "the first return must decide the result")
			assert.Equal(c, err.Error(), want, "Serve must return the outcome of the first return")
		})
	})
}
