// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package errs_test

import (
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/errs"
)

// throttledError is an error type that reports its own delay, the shape
// a producer that defines its error type uses in preference to
// [errs.WithRetryAfter].
type throttledError struct{ delay time.Duration }

func (throttledError) Error() string { return "errs_test: throttled" }

func (t throttledError) RetryAfter() time.Duration { return t.delay }

// The generators of the properties over delays: positive generates the
// delays that RetryAfter reports, and nonPositive the delays that count as
// none.
var (
	positive    = prop.Duration(1, math.MaxInt64)
	nonPositive = prop.Duration(math.MinInt64, 0)
)

func TestRetryAfter(t *testing.T) {
	t.Parallel()

	t.Run("reports no delay for nil", func(t *testing.T) {
		t.Parallel()
		d, ok := errs.RetryAfter(nil)
		assert.False(t, ok, "nil must report no delay")
		assert.Equal(t, d, time.Duration(0), "the delay of nil must be zero")
	})

	t.Run("reports no delay for an error without one", func(t *testing.T) {
		t.Parallel()
		d, ok := errs.RetryAfter(errSentinel)
		assert.False(t, ok, "an error without a delay must report none")
		assert.Equal(t, d, time.Duration(0), "the delay must be zero")
	})

	t.Run("reports the delay of an error type that has a RetryAfter method", func(t *testing.T) {
		t.Parallel()
		prop.ForAll(t, "RetryAfter must report the positive delay of a RetryAfter method", func(c *prop.Case) {
			delay := c.Draw(positive, "delay")
			d, ok := errs.RetryAfter(throttledError{delay: delay})
			assert.True(c, ok, "a RetryAfter method must be found")
			assert.Equal(c, d, delay, "the delay must be the method's")
		})
	})

	t.Run("reports a delay through a wrap", func(t *testing.T) {
		t.Parallel()
		d, ok := errs.RetryAfter(fmt.Errorf("fetch: %w", throttledError{delay: 5 * time.Second}))
		assert.True(t, ok, "a delay must be found through the chain")
		assert.Equal(t, d, 5*time.Second, "the delay must survive wrapping")
	})

	t.Run("reports the outermost positive delay on the chain", func(t *testing.T) {
		t.Parallel()
		err := errs.WithRetryAfter(errs.WithRetryAfter(errSentinel, 5*time.Second), 7*time.Second)
		d, _ := errs.RetryAfter(err)
		assert.Equal(t, d, 7*time.Second, "a layer must be able to replace a delay")
	})

	t.Run("reports the delay beneath a delay that counts as none", func(t *testing.T) {
		t.Parallel()
		prop.ForAll(t, "a delay that is not positive must not hide the delay beneath it", func(c *prop.Case) {
			inner := c.Draw(positive, "inner")
			outer := c.Draw(nonPositive, "outer")
			d, ok := errs.RetryAfter(errs.WithRetryAfter(errs.WithRetryAfter(errSentinel, inner), outer))
			assert.True(c, ok, "the positive delay beneath must be found")
			assert.Equal(c, d, inner, "the positive delay must be reported")
		})
	})

	t.Run("reports no delay for a delay that is not positive", func(t *testing.T) {
		t.Parallel()
		prop.False(t, func(delay time.Duration) bool {
			_, ok := errs.RetryAfter(errs.WithRetryAfter(errSentinel, delay))

			return ok
		}, "a zero or negative delay must count as none",
			prop.Using(nonPositive), prop.Example(time.Duration(0)), prop.Example(-time.Second))
	})

	t.Run("reports the longest delay of a join", func(t *testing.T) {
		t.Parallel()
		prop.Equal(t,
			func(delays []time.Duration) time.Duration {
				branches := make([]error, 0, len(delays)+1)
				for _, d := range delays {
					branches = append(branches, throttledError{delay: d})
				}
				d, _ := errs.RetryAfter(errors.Join(append(branches, errSentinel)...))

				return d
			},
			func(delays []time.Duration) time.Duration {
				var longest time.Duration
				for _, d := range delays {
					longest = max(longest, d)
				}

				return longest
			},
			"a join must report the longest positive delay of its branches",
			prop.Using(prop.List(prop.Duration(-time.Hour, time.Hour), prop.MaxSize(6))),
			prop.Example([]time.Duration{2 * time.Second, 9 * time.Second, 5 * time.Second}),
			prop.Example([]time.Duration{-time.Second}),
		)
	})

	t.Run("reports the delay of a join beneath a wrap", func(t *testing.T) {
		t.Parallel()
		joined := errors.Join(errSentinel, throttledError{delay: 3 * time.Second})
		d, _ := errs.RetryAfter(fmt.Errorf("op: %w", joined))
		assert.Equal(t, d, 3*time.Second, "a delay must be found in a join through a wrap")
	})
}

func TestWithRetryAfter(t *testing.T) {
	t.Parallel()

	t.Run("returns nil for nil", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, errs.WithRetryAfter(nil, time.Second),
			"tagging the absence of an error must not produce one")
	})

	t.Run("keeps the message of the wrapped error", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, errs.WithRetryAfter(errSentinel, time.Second).Error(), errSentinel.Error(),
			"the delay must not leak into the error text")
	})

	t.Run("keeps errors.Is to the wrapped error", func(t *testing.T) {
		t.Parallel()
		assert.ErrorIs(t, errs.WithRetryAfter(errSentinel, time.Second), errSentinel,
			"tagging must not break matching on the underlying sentinel")
	})

	t.Run("keeps the class of the wrapped error", func(t *testing.T) {
		t.Parallel()
		prop.Equal(t,
			func(c errs.Class) errs.Class {
				return errs.Classify(errs.WithRetryAfter(errs.WithClass(errSentinel, c), time.Second))
			},
			func(c errs.Class) errs.Class { return c },
			"a delay must not change the class", prop.Using(defined))
	})

	t.Run("leaves an unclassified error Unspecified", func(t *testing.T) {
		t.Parallel()
		err := errs.WithRetryAfter(errSentinel, time.Second)
		assert.Equal(t, errs.Classify(err), errs.Unspecified, "a delay must not make an error retryable")
	})
}

// TestRetryAfterAllocs checks the allocation contracts of RetryAfter and
// WithRetryAfter. MaxAllocs counts the allocations of the whole process,
// so the test does not run in parallel.
//
//nolint:paralleltest // see above
func TestRetryAfterAllocs(t *testing.T) {
	t.Run("RetryAfter", func(t *testing.T) {
		err := fmt.Errorf("outer: %w", errs.WithRetryAfter(errSentinel, time.Second))

		var got time.Duration
		expect.MaxAllocs(t, func() { got, _ = errs.RetryAfter(err) }, 0, "RetryAfter must not allocate")
		assert.Equal(t, got, time.Second, "the test must measure a delay")
	})

	t.Run("WithRetryAfter", func(t *testing.T) {
		var got error
		expect.MaxAllocs(t, func() { got = errs.WithRetryAfter(errSentinel, time.Second) }, 1,
			"WithRetryAfter must allocate only its wrapper")
		assert.ErrorIs(t, got, errSentinel, "the test must measure a tagged error")
	})
}

func BenchmarkRetryAfter(b *testing.B) {
	b.Run("RetryAfter", func(b *testing.B) {
		err := fmt.Errorf("outer: %w", errs.WithRetryAfter(errSentinel, time.Second))

		var got time.Duration

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got, _ = errs.RetryAfter(err)
		}

		assert.Equal(b, got, time.Second, "the benchmark must measure a delay")
	})

	b.Run("WithRetryAfter", func(b *testing.B) {
		var got error

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			got = errs.WithRetryAfter(errSentinel, time.Second)
		}

		assert.ErrorIs(b, got, errSentinel, "the benchmark must measure a tagged error")
	})
}
