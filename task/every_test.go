// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package task_test

import (
	"context"
	"errors"
	"math"
	"sync/atomic"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/rand"
	"go.thesmos.sh/core/rand/constant"
	"go.thesmos.sh/core/task"
)

// period is the period of every loop in TestEvery.
const period = time.Minute

// origin is the virtual time at which the fake clock of every case
// starts.
var origin = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// loop is one call to Every on its own goroutine. calls receives the
// virtual time of every call to fn, and done receives Every's result.
type loop struct {
	calls chan time.Time
	done  chan error
}

// startEvery runs Every with period on a new goroutine under c. fn
// receives the number of the call, starting at one.
func startEvery(
	ctx context.Context,
	c *fake.Clock,
	r rand.Rand,
	jitter time.Duration,
	fn func(ctx context.Context, n int) error,
) *loop {
	l := &loop{calls: make(chan time.Time, 16), done: make(chan error, 1)}
	n := 0

	go func() {
		l.done <- task.Every(ctx, c, r, period, jitter, func(ctx context.Context) error {
			l.calls <- c.Time()
			n++

			return fn(ctx, n)
		})
	}()

	return l
}

// call returns the virtual time of the next call to fn, or fails the test
// when there is none within a second.
func (l *loop) call(t *testing.T) time.Time {
	t.Helper()

	select {
	case at := <-l.calls:
		return at
	case <-time.After(time.Second):
		t.Fatal("fn was not called within a second")

		return time.Time{}
	}
}

// noCall fails the test when fn has been called since the last call to
// call. It first waits until Every waits on c again. Every sends on calls
// inside fn, before it registers its next timer, so a call that ran early
// is on calls by then.
func (l *loop) noCall(t *testing.T, c *fake.Clock) {
	t.Helper()

	c.AwaitWaiters(1)
	assert.Length(t, l.calls, 0, "fn must not be called before its time")
}

// result returns Every's result, or fails the test when Every has not
// returned within a second.
func (l *loop) result(t *testing.T) error {
	t.Helper()

	select {
	case err := <-l.done:
		return err
	case <-time.After(time.Second):
		t.Fatal("Every did not return within a second")

		return nil
	}
}

// TestEvery drives Every with a fake clock. A case advances the clock only
// after AwaitWaiters reports that Every is waiting, so each assertion
// about when fn runs is deterministic.
//
// The jitter cases pin the draw with rand/constant. A constant of all ones
// draws one below the bound, and a constant of one draws zero. A constant
// zero never leaves the rejection band of rand.Uint64N.
//
// A case in which fn must not run has fn return errBoom. A loop that
// starts by mistake then ends at its first call, and the case fails
// instead of hanging.
func TestEvery(t *testing.T) {
	t.Parallel()

	t.Run("calls fn at once and then one period after each call returns", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())
		c := fake.New(origin)
		l := startEvery(ctx, c, nil, 0, func(context.Context, int) error { return nil })

		assert.Equal(t, l.call(t), origin, "the first call must run at once")
		c.AwaitWaiters(1)
		c.Advance(period - time.Nanosecond)
		l.noCall(t, c)
		c.Advance(time.Nanosecond)
		assert.Equal(t, l.call(t), origin.Add(period), "the second call must run one period later")

		cancel()
		assert.NoError(t, l.result(t), "cancelling ctx must stop the loop with nil")
	})

	t.Run("starts the wait when a slow call returns", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())
		c := fake.New(origin)
		l := startEvery(ctx, c, nil, 0, func(_ context.Context, n int) error {
			if n == 1 {
				c.Advance(5 * period)
			}

			return nil
		})

		l.call(t)
		c.AwaitWaiters(1)
		c.Advance(period - time.Nanosecond)
		l.noCall(t, c)
		c.Advance(time.Nanosecond)
		assert.Equal(t, l.call(t), origin.Add(6*period),
			"the next call must run one period after the slow call returned")

		cancel()
		assert.NoError(t, l.result(t), "cancelling ctx must stop the loop with nil")
	})

	t.Run("adds a jitter below its bound", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())
		c := fake.New(origin)
		l := startEvery(ctx, c, constant.New(math.MaxUint64), time.Second, func(context.Context, int) error {
			return nil
		})

		l.call(t)
		c.AwaitWaiters(1)
		c.Advance(period + time.Second - 2*time.Nanosecond)
		l.noCall(t, c)
		c.Advance(time.Nanosecond)
		assert.Equal(t, l.call(t), origin.Add(period+time.Second-time.Nanosecond),
			"the largest draw must wait one nanosecond less than period plus jitter")

		cancel()
		assert.NoError(t, l.result(t), "cancelling ctx must stop the loop with nil")
	})

	t.Run("waits the bare period for the smallest jitter draw", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())
		c := fake.New(origin)
		l := startEvery(ctx, c, constant.New(1), time.Second, func(context.Context, int) error { return nil })

		l.call(t)
		c.AwaitWaiters(1)
		c.Advance(period)
		assert.Equal(t, l.call(t), origin.Add(period), "a draw of zero must wait exactly one period")

		cancel()
		assert.NoError(t, l.result(t), "cancelling ctx must stop the loop with nil")
	})

	t.Run("returns fn's error", func(t *testing.T) {
		t.Parallel()
		c := fake.New(origin)
		l := startEvery(t.Context(), c, nil, 0, func(_ context.Context, n int) error {
			if n == 2 {
				return errBoom
			}

			return nil
		})

		l.call(t)
		c.AwaitWaiters(1)
		c.Advance(period)
		l.call(t)
		assert.ErrorIs(t, l.result(t), errBoom, "fn's error must end the loop")
	})

	t.Run("returns nil when a call returns the cause of its ended context", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancelCause(t.Context())
		cause := errors.New("task_test: shutting down")
		l := startEvery(ctx, fake.New(origin), nil, 0, func(ctx context.Context, _ int) error {
			cancel(cause)

			return context.Cause(ctx)
		})

		l.call(t)
		assert.NoError(t, l.result(t), "a call cut short by the end of ctx must stop the loop with nil")
	})

	t.Run("returns nil when a call returns the error of a context that ended with a cause", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancelCause(t.Context())
		l := startEvery(ctx, fake.New(origin), nil, 0, func(ctx context.Context, _ int) error {
			cancel(errBoom)

			return ctx.Err()
		})

		l.call(t)
		assert.NoError(t, l.result(t), "a call that returns ctx.Err() after the end of ctx must stop the loop with nil")
	})

	t.Run("returns a failure that coincides with the end of its context", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())
		l := startEvery(ctx, fake.New(origin), nil, 0, func(context.Context, int) error {
			cancel()

			return errBoom
		})

		l.call(t)
		assert.ErrorIs(t, l.result(t), errBoom, "a failure unrelated to ctx must be returned")
	})

	t.Run("returns nil without calling fn when ctx has already ended", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		calls := 0
		err := task.Every(ctx, fake.New(origin), nil, period, 0, func(context.Context) error {
			calls++

			return errBoom
		})
		assert.NoError(t, err, "an ended ctx must stop the loop with nil")
		assert.Equal(t, calls, 0, "fn must not run under an ended ctx")
	})

	t.Run("returns ErrPeriod for a period that is not positive", func(t *testing.T) {
		t.Parallel()

		var calls atomic.Int32
		prop.ErrorIs(t, func(p time.Duration) error {
			return task.Every(t.Context(), fake.New(origin), nil, p, 0, func(context.Context) error {
				calls.Add(1)

				return errBoom
			})
		}, task.ErrPeriod, "a period that is not positive must be refused",
			prop.Using(prop.Duration(math.MinInt64, 0)), prop.Example(time.Duration(0)), prop.Example(-time.Second))
		assert.Equal(t, calls.Load(), 0, "fn must not run for a refused period")
		assert.Equal(t, errs.Classify(task.ErrPeriod), errs.Invalid, "ErrPeriod must classify as Invalid")
	})

	t.Run("returns ErrPeriod for a negative jitter", func(t *testing.T) {
		t.Parallel()

		var calls atomic.Int32
		prop.ErrorIs(t, func(jitter time.Duration) error {
			return task.Every(t.Context(), fake.New(origin), nil, time.Second, jitter, func(context.Context) error {
				calls.Add(1)

				return errBoom
			})
		}, task.ErrPeriod, "a negative jitter must be refused",
			prop.Using(prop.Duration(math.MinInt64, -1)), prop.Example(-time.Nanosecond))
		assert.Equal(t, calls.Load(), 0, "fn must not run for a refused jitter")
	})

	t.Run("returns ErrPeriod for a positive jitter without a source", func(t *testing.T) {
		t.Parallel()

		calls := 0
		err := task.Every(t.Context(), fake.New(origin), nil, period, time.Second, func(context.Context) error {
			calls++

			return errBoom
		})
		assert.ErrorIs(t, err, task.ErrPeriod, "a jitter without a source must be refused")
		assert.Equal(t, calls, 0, "fn must not run without a jitter source")
	})
}
