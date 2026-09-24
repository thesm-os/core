// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package task_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/task"
)

// barrier returns a function that each of n calls runs first. It
// returns once all n calls have started. A call that never starts
// leaves the others waiting, and within fails the test.
func barrier(n int) func() {
	var wg sync.WaitGroup
	wg.Add(n)

	return func() {
		wg.Done()
		wg.Wait()
	}
}

// TestQuorum covers the quorum decision. Each test starts every call
// at once and keeps them at a barrier until all have started, so the
// order in which results arrive fixes the decision. The rules Quorum
// shares with the other functions of the package are in TestScope.
func TestQuorum(t *testing.T) {
	t.Parallel()

	t.Run("returns nil at k successes and cancels the calls still running", func(t *testing.T) {
		t.Parallel()
		start := barrier(5)

		var (
			cancelled atomic.Int32
			err       error
		)
		within(t, func() {
			err = task.Quorum(t.Context(), 5, 2, indices(5), func(ctx context.Context, i, _ int) error {
				start()
				if i < 2 {
					return nil
				}

				if errors.Is(awaitCancel(ctx), context.Canceled) {
					cancelled.Add(1)
				}

				return errBoom
			})
		})

		testkit.NoError(t, err, "two successes must meet a quorum of two")
		testkit.Equal(t, cancelled.Load(), int32(3), "the three calls still running must be cancelled")
	})

	t.Run("returns ErrNoQuorum joined with the failures that decided it", func(t *testing.T) {
		t.Parallel()
		start := barrier(5)
		failures := []error{testkit.TestError("a"), testkit.TestError("b"), testkit.TestError("c")}

		var (
			causes [5]error
			err    error
		)
		within(t, func() {
			err = task.Quorum(t.Context(), 5, 3, indices(5), func(ctx context.Context, i, _ int) error {
				start()
				if i < len(failures) {
					return failures[i]
				}

				causes[i] = awaitCancel(ctx)

				return causes[i]
			})
		})

		testkit.ErrorIs(t, err, task.ErrNoQuorum, "three failures of five must make a quorum of three impossible")
		for i, f := range failures {
			testkit.ErrorIs(t, err, f, "failure "+strconv.Itoa(i)+" must be joined with ErrNoQuorum")
			testkit.True(t, strings.Contains(err.Error(), "item "+strconv.Itoa(i)+": "+f.Error()),
				"failure "+strconv.Itoa(i)+" must be wrapped with its index")
		}
		testkit.ErrorIsNot(t, err, context.Canceled, "the cancelled calls must not be joined")
		testkit.ErrorIs(t, causes[3], task.ErrNoQuorum, "the calls still running must be cancelled with the result")
	})

	t.Run("does not call fn after the decision", func(t *testing.T) {
		t.Parallel()

		var (
			runs atomic.Int32
			err  error
		)
		within(t, func() {
			err = task.Quorum(t.Context(), 1, 1, indices(5), func(context.Context, int, int) error {
				runs.Add(1)

				return nil
			})
		})

		testkit.NoError(t, err, "one success must meet a quorum of one")
		testkit.Equal(t, runs.Load(), int32(1), "no element may be called after the quorum is met")
	})

	t.Run("returns the cause of ctx when a failure decides it after ctx ended", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancelCause(t.Context())
		cause := testkit.TestError("shutting down")
		start := barrier(3)

		var err error
		within(t, func() {
			err = task.Quorum(ctx, 3, 3, indices(3), func(ctx context.Context, i, _ int) error {
				start()
				if i == 0 {
					cancel(cause)

					return nil
				}

				return awaitCancel(ctx)
			})
		})

		testkit.ErrorIs(t, err, cause, "the end of ctx must be the result")
		testkit.ErrorIsNot(t, err, task.ErrNoQuorum, "a quorum cut short by ctx must not report ErrNoQuorum")
	})

	t.Run("returns the cause of ctx when ctx ends with elements unclaimed", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancelCause(t.Context())
		cause := testkit.TestError("shutting down")

		var err error
		within(t, func() {
			err = task.Quorum(ctx, 1, 3, indices(3), func(context.Context, int, int) error {
				cancel(cause)

				return nil
			})
		})

		testkit.ErrorIs(t, err, cause, "the end of ctx must be the result")
	})

	t.Run("classifies ErrNoQuorum as its first classified failure", func(t *testing.T) {
		t.Parallel()
		err := task.Quorum(t.Context(), 1, 1, indices(1), func(context.Context, int, int) error {
			return errs.WithClass(errBoom, errs.Transient)
		})

		testkit.ErrorIs(t, err, task.ErrNoQuorum, "the only call failing must make the quorum impossible")
		testkit.Equal(t, errs.Classify(err), errs.Transient, "the failure's class must be the result's class")
	})

	for _, k := range []int{-1, 0, 4} {
		t.Run("returns ErrQuorumSize for a quorum of "+strconv.Itoa(k)+" of three", func(t *testing.T) {
			t.Parallel()

			var runs atomic.Int32
			err := task.Quorum(t.Context(), 1, k, indices(3), func(context.Context, int, int) error {
				runs.Add(1)

				return nil
			})

			testkit.ErrorIs(t, err, task.ErrQuorumSize, "a quorum outside one to the number of items must be refused")
			testkit.Equal(t, errs.Classify(err), errs.Invalid, "ErrQuorumSize must classify as Invalid")
			testkit.Equal(t, runs.Load(), int32(0), "a refused call must not run fn")
		})
	}
}
