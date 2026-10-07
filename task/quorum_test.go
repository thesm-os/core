// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package task_test

import (
	"context"
	"errors"
	"math"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/task"
)

// quorumAllocs is the number of allocations of one call of Quorum whose
// calls all succeed, whatever the number of its elements: the derived
// context, its state, the quorum and the closure that its goroutines
// share.
const quorumAllocs = 4

// TestQuorum covers the quorum decision. Each case starts every call at
// once and keeps them at a barrier until all have started, so the order
// in which results arrive fixes the decision. The rules Quorum shares with
// the other functions of the package are in TestScope.
func TestQuorum(t *testing.T) {
	t.Parallel()

	t.Run("cancels the calls still running once k succeed", func(t *testing.T) {
		t.Parallel()
		start := barrier(5)

		var (
			cancelled atomic.Int32
			err       error
		)
		assert.CompletesWithin(t, returnBound, func(ctx context.Context) error {
			err = task.Quorum(ctx, 5, 2, indices(5), func(ctx context.Context, i, _ int) error {
				start()
				if i < 2 {
					return nil
				}

				if errors.Is(awaitCancel(ctx), context.Canceled) {
					cancelled.Add(1)
				}

				return errBoom
			})

			return err
		}, "Quorum must return once its calls return")

		assert.NoError(t, err, "two successes must meet a quorum of two")
		assert.Equal(t, cancelled.Load(), 3, "the three calls still running must be cancelled")
	})

	t.Run("returns ErrNoQuorum joined with the failures that decided it", func(t *testing.T) {
		t.Parallel()
		start := barrier(5)
		failures := []error{errors.New("task_test: a"), errors.New("task_test: b"), errors.New("task_test: c")}

		var (
			causes [5]error
			err    error
		)
		assert.CompletesWithin(t, returnBound, func(ctx context.Context) error {
			err = task.Quorum(ctx, 5, 3, indices(5), func(ctx context.Context, i, _ int) error {
				start()
				if i < len(failures) {
					return failures[i]
				}

				causes[i] = awaitCancel(ctx)

				return causes[i]
			})

			return err
		}, "Quorum must return once its calls return")

		assert.ErrorIs(t, err, task.ErrNoQuorum, "three failures of five must make a quorum of three impossible")
		for i, f := range failures {
			assert.ErrorIs(t, err, f, "failure "+strconv.Itoa(i)+" must be joined with ErrNoQuorum")
			assert.Contains(t, err.Error(), "item "+strconv.Itoa(i)+": "+f.Error(),
				"failure "+strconv.Itoa(i)+" must be wrapped with its index")
		}
		assert.ErrorIsNot(t, err, context.Canceled, "the cancelled calls must not be joined")
		assert.ErrorIs(t, causes[3], task.ErrNoQuorum, "the calls still running must be cancelled with the result")
	})

	t.Run("does not call fn after the decision", func(t *testing.T) {
		t.Parallel()

		var runs atomic.Int32
		err := task.Quorum(t.Context(), 1, 1, indices(5), func(context.Context, int, int) error {
			runs.Add(1)

			return nil
		})

		assert.NoError(t, err, "one success must meet a quorum of one")
		assert.Equal(t, runs.Load(), 1, "no element may be called after the quorum is met")
	})

	t.Run("returns the cause of ctx when a failure decides it after ctx ended", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancelCause(t.Context())
		cause := errors.New("task_test: shutting down")
		start := barrier(3)

		var err error
		assert.CompletesWithin(t, returnBound, func(context.Context) error {
			err = task.Quorum(ctx, 3, 3, indices(3), func(ctx context.Context, i, _ int) error {
				start()
				if i == 0 {
					cancel(cause)

					return nil
				}

				return awaitCancel(ctx)
			})

			return err
		}, "Quorum must return once its calls return")

		assert.ErrorIs(t, err, cause, "the end of ctx must be the result")
		assert.ErrorIsNot(t, err, task.ErrNoQuorum, "a quorum cut short by ctx must not report ErrNoQuorum")
	})

	t.Run("returns the cause of ctx when ctx ends with elements unclaimed", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancelCause(t.Context())
		cause := errors.New("task_test: shutting down")

		err := task.Quorum(ctx, 1, 3, indices(3), func(context.Context, int, int) error {
			cancel(cause)

			return nil
		})

		assert.ErrorIs(t, err, cause, "the end of ctx must be the result")
	})

	t.Run("classifies ErrNoQuorum as the class of its only failure", func(t *testing.T) {
		t.Parallel()
		err := task.Quorum(t.Context(), 1, 1, indices(1), func(context.Context, int, int) error {
			return errs.WithClass(errBoom, errs.Transient)
		})

		assert.ErrorIs(t, err, task.ErrNoQuorum, "the only call failing must make the quorum impossible")
		assert.Equal(t, errs.Classify(err), errs.Transient, "the failure's class must be the result's class")
	})

	t.Run("classifies ErrNoQuorum as the class of highest rank among its failures", func(t *testing.T) {
		t.Parallel()
		failures := []error{errs.WithClass(errBoom, errs.Transient), errs.WithClass(errLate, errs.Integrity)}
		err := task.Quorum(t.Context(), 1, 1, failures, func(_ context.Context, _ int, f error) error { return f })
		assert.Equal(t, errs.Classify(err), errs.Integrity, "Integrity must outrank Transient")
	})

	t.Run("classifies ErrNoQuorum whatever the order of its failures", func(t *testing.T) {
		t.Parallel()
		// One worker claims the items in order, so the failures are
		// recorded in the order of the slice.
		fail := func(_ context.Context, _ int, f error) error { return f }
		prop.Commutative(t, func(a, b errs.Class) errs.Class {
			failures := []error{errs.WithClass(errBoom, a), errs.WithClass(errLate, b)}

			return errs.Classify(task.Quorum(t.Context(), 1, 1, failures, fail))
		}, "the class must not depend on the order of the failures",
			prop.Using(prop.Integer[errs.Class](errs.Unspecified, errs.Integrity)))
	})

	t.Run("returns ErrQuorumSize for a quorum outside one to the number of items", func(t *testing.T) {
		t.Parallel()

		var runs atomic.Int32
		prop.ErrorIs(t, func(k int) error {
			return task.Quorum(t.Context(), 1, k, indices(3), func(context.Context, int, int) error {
				runs.Add(1)

				return nil
			})
		}, task.ErrQuorumSize, "a quorum outside one to three of three must be refused",
			prop.Using(prop.OneOf(prop.Integer(math.MinInt, 0), prop.Integer(4, math.MaxInt))),
			prop.Example(-1), prop.Example(0), prop.Example(4))
		assert.Equal(t, runs.Load(), 0, "a refused call must not run fn")
		assert.Equal(t, errs.Classify(task.ErrQuorumSize), errs.Invalid, "ErrQuorumSize must classify as Invalid")
	})
}

// TestQuorumAllocs checks the allocation contract of Quorum: a fixed
// number of allocations per call whose calls succeed, whatever the number
// of elements. MaxAllocs counts the allocations of the whole process, so
// the test does not run in parallel.
//
//nolint:paralleltest // see above
func TestQuorumAllocs(t *testing.T) {
	ctx := t.Context()
	succeed := func(context.Context, int, int) error { return nil }

	for _, n := range []int{64, 512} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			items := indices(n)

			var err error
			expect.MaxAllocs(t, func() { err = task.Quorum(ctx, 1, n, items, succeed) }, quorumAllocs,
				"Quorum must allocate a fixed amount, whatever the number of elements")
			assert.NoError(t, err, "the test must measure a quorum that is met")
		})
	}
}

// BenchmarkQuorum measures one call that needs two immediate successes of
// five, the shape of a witness quorum.
func BenchmarkQuorum(b *testing.B) {
	witnesses := indices(5)

	var sum atomic.Int64
	cosign := func(_ context.Context, i, _ int) error {
		sum.Add(int64(i))

		return nil
	}

	var err error

	c := bench.Start(b).Warmup(benchWarmup).MaxAllocs(quorumAllocs)
	defer c.End()

	for c.Loop() {
		err = task.Quorum(b.Context(), len(witnesses), 2, witnesses, cosign)
	}

	assert.NoError(b, err, "the benchmark must measure a quorum that is met")
}

// barrier returns a function that each of n calls runs first. It returns
// once all n calls have started. A call that never starts leaves the
// others waiting, and the bound of CompletesWithin fails the case.
func barrier(n int) func() {
	var wg sync.WaitGroup
	wg.Add(n)

	return func() {
		wg.Done()
		wg.Wait()
	}
}
