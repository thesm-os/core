// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package errs_test

import (
	"errors"
	"fmt"
	"runtime"
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/errs"
)

// throttledError is an error type that reports its own delay, the shape
// a producer that defines its error type uses in preference to
// [errs.WithRetryAfter].
type throttledError struct{ delay time.Duration }

func (throttledError) Error() string { return "errs_test: throttled" }

func (t throttledError) RetryAfter() time.Duration { return t.delay }

func TestRetryAfter(t *testing.T) {
	t.Parallel()

	t.Run("reports no delay for nil", func(t *testing.T) {
		t.Parallel()
		d, ok := errs.RetryAfter(nil)
		testkit.False(t, ok, "nil must report no delay")
		testkit.Equal(t, d, time.Duration(0), "the delay of nil must be zero")
	})

	t.Run("reports no delay for an error without one", func(t *testing.T) {
		t.Parallel()
		d, ok := errs.RetryAfter(errSentinel)
		testkit.False(t, ok, "an error without a delay must report none")
		testkit.Equal(t, d, time.Duration(0), "the delay must be zero")
	})

	t.Run("reports the delay of an error type that has a RetryAfter method", func(t *testing.T) {
		t.Parallel()
		d, ok := errs.RetryAfter(throttledError{delay: 5 * time.Second})
		testkit.True(t, ok, "a RetryAfter method must be found")
		testkit.Equal(t, d, 5*time.Second, "the delay must be the method's")
	})

	t.Run("reports a delay through a wrap", func(t *testing.T) {
		t.Parallel()
		d, ok := errs.RetryAfter(fmt.Errorf("fetch: %w", throttledError{delay: 5 * time.Second}))
		testkit.True(t, ok, "a delay must be found through the chain")
		testkit.Equal(t, d, 5*time.Second, "the delay must survive wrapping")
	})

	t.Run("reports the outermost positive delay on the chain", func(t *testing.T) {
		t.Parallel()
		err := errs.WithRetryAfter(errs.WithRetryAfter(errSentinel, 5*time.Second), 7*time.Second)
		d, _ := errs.RetryAfter(err)
		testkit.Equal(t, d, 7*time.Second, "a layer must be able to replace a delay")
	})

	t.Run("reports the delay beneath a zero delay", func(t *testing.T) {
		t.Parallel()
		err := errs.WithRetryAfter(errs.WithRetryAfter(errSentinel, 5*time.Second), 0)
		d, ok := errs.RetryAfter(err)
		testkit.True(t, ok, "a zero delay must not hide the delay beneath it")
		testkit.Equal(t, d, 5*time.Second, "the positive delay must be reported")
	})

	t.Run("reports no delay for a zero delay", func(t *testing.T) {
		t.Parallel()
		d, ok := errs.RetryAfter(errs.WithRetryAfter(errSentinel, 0))
		testkit.False(t, ok, "a zero delay must count as none")
		testkit.Equal(t, d, time.Duration(0), "the delay must be zero")
	})

	t.Run("reports no delay for a negative delay", func(t *testing.T) {
		t.Parallel()
		d, ok := errs.RetryAfter(errs.WithRetryAfter(errSentinel, -time.Second))
		testkit.False(t, ok, "a negative delay must count as none")
		testkit.Equal(t, d, time.Duration(0), "the delay must be zero")
	})

	t.Run("reports the longest delay of a join", func(t *testing.T) {
		t.Parallel()
		// The longest delay is neither the first nor the last, and a
		// branch without a delay follows it.
		joined := errors.Join(
			throttledError{delay: 2 * time.Second},
			throttledError{delay: 9 * time.Second},
			errSentinel,
			throttledError{delay: 5 * time.Second},
		)
		d, ok := errs.RetryAfter(joined)
		testkit.True(t, ok, "a delay in a branch must be found")
		testkit.Equal(t, d, 9*time.Second, "a join must report its longest delay")
	})

	t.Run("reports no delay for a join without a positive one", func(t *testing.T) {
		t.Parallel()
		d, ok := errs.RetryAfter(errors.Join(errSentinel, throttledError{delay: -time.Second}))
		testkit.False(t, ok, "a join without a positive delay must report none")
		testkit.Equal(t, d, time.Duration(0), "the delay must be zero")
	})

	t.Run("reports the delay of a join beneath a wrap", func(t *testing.T) {
		t.Parallel()
		joined := errors.Join(errSentinel, throttledError{delay: 3 * time.Second})
		d, _ := errs.RetryAfter(fmt.Errorf("op: %w", joined))
		testkit.Equal(t, d, 3*time.Second, "a delay must be found in a join through a wrap")
	})
}

func TestWithRetryAfter(t *testing.T) {
	t.Parallel()

	t.Run("returns nil for nil", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, errs.WithRetryAfter(nil, time.Second), nil,
			"tagging the absence of an error must not produce one")
	})

	t.Run("keeps the message of the wrapped error", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, errs.WithRetryAfter(errSentinel, time.Second).Error(), errSentinel.Error(),
			"the delay must not leak into the error text")
	})

	t.Run("keeps errors.Is to the wrapped error", func(t *testing.T) {
		t.Parallel()
		testkit.ErrorIs(t, errs.WithRetryAfter(errSentinel, time.Second), errSentinel,
			"tagging must not break matching on the underlying sentinel")
	})

	t.Run("keeps the class of the wrapped error", func(t *testing.T) {
		t.Parallel()
		err := errs.WithRetryAfter(errs.WithClass(errSentinel, errs.Transient), time.Second)
		testkit.Equal(t, errs.Classify(err), errs.Transient, "a delay must not hide the class")
	})

	t.Run("leaves an unclassified error Unspecified", func(t *testing.T) {
		t.Parallel()
		err := errs.WithRetryAfter(errSentinel, time.Second)
		testkit.Equal(t, errs.Classify(err), errs.Unspecified, "a delay must not make an error retryable")
	})
}

func BenchmarkRetryAfter(b *testing.B) {
	err := fmt.Errorf("outer: %w", errs.WithRetryAfter(errSentinel, time.Second))
	b.ReportAllocs()
	var sink time.Duration
	for b.Loop() {
		sink, _ = errs.RetryAfter(err)
	}
	runtime.KeepAlive(sink)
}

func BenchmarkWithRetryAfter(b *testing.B) {
	b.ReportAllocs()
	var sink error
	for b.Loop() {
		sink = errs.WithRetryAfter(errSentinel, time.Second)
	}
	runtime.KeepAlive(sink)
}
