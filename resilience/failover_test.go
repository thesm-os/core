// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package resilience_test

import (
	"context"
	"errors"
	"math"
	"strconv"
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/resilience"
)

// authorities are the targets of the failovers of the tests.
var authorities = []string{"tsa-a", "tsa-b", "tsa-c"}

// codeError is an error with a code, which errors.As finds in the error
// of a failover.
type codeError struct{ code int }

func (e *codeError) Error() string { return "resilience_test: code " + strconv.Itoa(e.code) }

// classed returns errDependency with class c and, when d is positive, the
// delay d.
func classed(c errs.Class, d time.Duration) error {
	return errs.WithRetryAfter(errs.WithClass(errDependency, c), d)
}

// targets returns a function for Failover that returns the result of
// results for the index of its target, and records each index that it
// receives in *calls.
func targets(results []error, calls *[]int) func(context.Context, int) (int, error) {
	return func(_ context.Context, i int) (int, error) {
		*calls = append(*calls, i)
		if err := results[i]; err != nil {
			return 0, err
		}

		return i + 100, nil
	}
}

func TestFailover(t *testing.T) {
	t.Parallel()

	t.Run("refuses a configuration", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name    string
			targets []string
			start   int
		}{
			{name: "returns ErrConfig for no targets"},
			{name: "returns ErrConfig for a negative start", targets: authorities, start: -1},
			{name: "returns ErrConfig for a target named twice", targets: []string{"tsa-a", "tsa-b", "tsa-a"}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				b := mustBreaker(t, fake.New(originUTC))
				var calls []int

				_, err := resilience.Failover(t.Context(), b, tt.targets, tt.start, targets(nil, &calls))
				testkit.ErrorIs(t, err, resilience.ErrConfig, "Failover must refuse the configuration")
				testkit.Equal(t, errs.Classify(err), errs.Invalid, "ErrConfig must classify as Invalid")
				testkit.Equal(t, len(calls), 0, "a refused configuration must call no target")
			})
		}
	})

	t.Run("returns the value of the first target that succeeds", func(t *testing.T) {
		t.Parallel()
		b := mustBreaker(t, fake.New(originUTC))
		var calls []int

		got, err := resilience.Failover(t.Context(), b, authorities, 0,
			targets([]error{errTripping, nil, nil}, &calls))

		testkit.NoError(t, err, "a failover with a target that succeeds must succeed")
		testkit.Equal(t, got, 101, "Failover must return the value of the second target")
		testkit.Equal(t, calls, []int{0, 1}, "Failover must not call a target after a success")
	})

	t.Run("starts at start modulo the number of targets and wraps around", func(t *testing.T) {
		t.Parallel()
		b := mustBreaker(t, fake.New(originUTC))
		var calls []int

		_, err := resilience.Failover(t.Context(), b, authorities, 4,
			targets([]error{errTripping, errTripping, errTripping}, &calls))

		testkit.ErrorIs(t, err, errDependency, "a failover whose targets fail must fail")
		testkit.Equal(t, calls, []int{1, 2, 0}, "Failover must start at 4 mod 3 and wrap around")
	})

	t.Run("skips a target whose circuit is open", func(t *testing.T) {
		t.Parallel()
		b := mustBreaker(t, fake.New(originUTC))
		for range 2 {
			b.Allow("tsa-a")
			b.Record("tsa-a", true)
		}
		var calls []int

		got, err := resilience.Failover(t.Context(), b, authorities, 0, targets([]error{nil, nil, nil}, &calls))

		testkit.NoError(t, err, "a failover with a closed circuit must succeed")
		testkit.Equal(t, got, 101, "Failover must return the value of the second target")
		testkit.Equal(t, calls, []int{1}, "Failover must not call a target whose circuit is open")
	})

	t.Run("records each outcome in the circuit of its target", func(t *testing.T) {
		t.Parallel()
		b := mustBreaker(t, fake.New(originUTC))
		var calls []int

		for range 2 {
			_, err := resilience.Failover(t.Context(), b, authorities, 0,
				targets([]error{errTripping, nil, nil}, &calls))
			testkit.NoError(t, err, "each failover must succeed through the second target")
		}

		testkit.Equal(t, b.State("tsa-a"), resilience.Open, "two failures must open the first target's circuit")
		testkit.Equal(t, b.State("tsa-b"), resilience.Closed, "successes must keep the second target's circuit closed")
	})

	t.Run("returns the cause of a context that ended before a call", func(t *testing.T) {
		t.Parallel()
		b := mustBreaker(t, fake.New(originUTC))
		cause := errors.New("resilience_test: shutdown")
		ctx, cancel := context.WithCancelCause(t.Context())
		cancel(cause)
		var calls []int

		_, err := resilience.Failover(ctx, b, authorities, 0, targets([]error{nil, nil, nil}, &calls))

		testkit.ErrorIs(t, err, cause, "Failover must return the cause of the context")
		testkit.Equal(t, len(calls), 0, "Failover must call no target after the context ended")
	})

	t.Run("returns the error of a call whose context ended during it", func(t *testing.T) {
		t.Parallel()
		b := mustBreaker(t, fake.New(originUTC))

		for range 2 {
			ctx, cancel := context.WithCancel(t.Context())
			var calls []int

			_, err := resilience.Failover(ctx, b, authorities, 0, func(_ context.Context, i int) (int, error) {
				calls = append(calls, i)
				cancel()

				return 0, errTripping
			})

			testkit.ErrorIs(t, err, errTripping, "Failover must return the call's own error")
			testkit.Equal(t, calls, []int{0}, "Failover must call no target after the context ended")
		}

		testkit.Equal(t, b.State("tsa-a"), resilience.Closed,
			"a call whose context ended must record no failure")
	})

	t.Run("contains the error of each target in the order of the failover", func(t *testing.T) {
		t.Parallel()
		b := mustBreaker(t, fake.New(originUTC))
		for range 2 {
			b.Allow("tsa-b")
			b.Record("tsa-b", true)
		}
		var calls []int
		coded := &codeError{code: 7}

		_, err := resilience.Failover(t.Context(), b, authorities, 0,
			targets([]error{errTripping, nil, coded}, &calls))

		testkit.Equal(t, err.Error(), "resilience: every target failed: "+
			"tsa-a: resilience_test: dependency failed; "+
			"tsa-b: resilience: circuit open; "+
			"tsa-c: resilience_test: code 7",
			"the message must list each target and its error in order")
		testkit.ErrorIs(t, err, errDependency, "errors.Is must find the first target's error")
		testkit.ErrorIs(t, err, resilience.ErrOpen, "errors.Is must find the refusal of the open circuit")

		var found *codeError
		testkit.True(t, errors.As(err, &found), "errors.As must find the third target's error")
		testkit.True(t, found == coded, "errors.As must set the third target's error")
		testkit.False(t, errors.Is(err, context.Canceled), "errors.Is must not find an error that no target returned")

		var timeout interface{ Timeout() bool }
		testkit.False(t, errors.As(err, &timeout), "errors.As must not find a type that no target's error has")
	})

	t.Run("classifies by the target nearest to a remedy", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name      string
			results   []error
			wantClass errs.Class
			wantDelay time.Duration
		}{
			{
				name:      "is Transient when one target's error is Transient",
				results:   []error{classed(errs.Denied, 0), classed(errs.Transient, 0), classed(errs.Invalid, 0)},
				wantClass: errs.Transient,
			},
			{
				name:      "classifies as the join when no target's error is Transient",
				results:   []error{classed(errs.Invalid, 0), classed(errs.Denied, 0), classed(errs.NotFound, 0)},
				wantClass: errs.Denied,
			},
			{
				name: "reports the shortest delay among the Transient errors",
				results: []error{
					classed(errs.Transient, 5*time.Second),
					classed(errs.Denied, time.Hour),
					classed(errs.Transient, 2*time.Second),
				},
				wantClass: errs.Transient,
				wantDelay: 2 * time.Second,
			},
			{
				name: "reports no delay when a Transient error has none",
				results: []error{
					classed(errs.Transient, 5*time.Second),
					classed(errs.Transient, 0),
					classed(errs.Transient, 2*time.Second),
				},
				wantClass: errs.Transient,
			},
			{
				name: "reports the longest delay when no error is Transient",
				results: []error{
					classed(errs.Denied, time.Minute),
					classed(errs.Invalid, time.Hour),
					classed(errs.Denied, time.Second),
				},
				wantClass: errs.Denied,
				wantDelay: time.Hour,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				b := mustBreaker(t, fake.New(originUTC))
				var calls []int

				_, err := resilience.Failover(t.Context(), b, authorities, 0, targets(tt.results, &calls))

				testkit.Equal(t, errs.Classify(err), tt.wantClass, "the error must classify by the nearest remedy")
				testkit.Equal(t, errs.Retryable(err), tt.wantClass == errs.Transient,
					"the error must be retryable when it is Transient")

				delay, ok := errs.RetryAfter(err)
				testkit.Equal(t, delay, tt.wantDelay, "RetryAfter must report the delay of the nearest remedy")
				testkit.Equal(t, ok, tt.wantDelay > 0, "RetryAfter must report whether there is a delay")
			})
		}
	})

	t.Run("lets a panic of fn continue and releases the probe", func(t *testing.T) {
		t.Parallel()
		c := fake.New(originUTC)
		b := mustBreaker(t, c)
		for range 2 {
			b.Allow("tsa-a")
			b.Record("tsa-a", true)
		}
		c.Advance(31 * time.Second)

		got := testkit.Panics(t, func() {
			_, _ = resilience.Failover(t.Context(), b, authorities[:1], 0,
				func(ctx context.Context, _ int) (int, error) { return panicking(ctx) })
		}, "Failover must let the panic of fn continue")

		testkit.Equal(t, got, any("fn panicked"), "the recovered value must be the panic value of fn")
		testkit.True(t, b.Allow("tsa-a"), "the circuit must admit the next probe")
	})

	t.Run("composes with a retry around it", func(t *testing.T) {
		t.Parallel()

		t.Run("tries every target on each attempt", func(t *testing.T) {
			t.Parallel()
			b := mustBreaker(t, fake.New(originUTC))
			r := mustRetrier(t, retryConfig(fake.New(originUTC), noJitter))
			var calls []int
			denied := classed(errs.Denied, 0)

			got, err := resilience.Do(bounded(t), r, func(ctx context.Context) (int, error) {
				return resilience.Failover(ctx, b, authorities[:2], 0, func(_ context.Context, i int) (int, error) {
					calls = append(calls, i)
					if i == 0 || len(calls) < 4 {
						return 0, []error{denied, errTripping}[i]
					}

					return 7, nil
				})
			})

			testkit.NoError(t, err, "the second attempt must succeed through the second target")
			testkit.Equal(t, got, 7, "Do must return the value of the second target")
			testkit.Equal(t, calls, []int{0, 1, 0, 1}, "each attempt must try every target in order")
		})

		t.Run("stops when no target's error is Transient", func(t *testing.T) {
			t.Parallel()
			b := mustBreaker(t, fake.New(originUTC))
			r := mustRetrier(t, retryConfig(fake.New(originUTC), noJitter))
			var calls []int

			_, err := resilience.Do(bounded(t), r, func(ctx context.Context) (int, error) {
				return resilience.Failover(ctx, b, authorities[:2], 0,
					targets([]error{classed(errs.Denied, 0), classed(errs.Invalid, 0)}, &calls))
			})

			testkit.Equal(t, errs.Classify(err), errs.Denied, "Do must return the failover's error")
			testkit.Equal(t, calls, []int{0, 1}, "Do must not retry a failover that no retry can fix")
		})
	})
}

func BenchmarkFailover(b *testing.B) {
	succeed := func(context.Context, int) (int, error) { return 1, nil }
	invalid := errs.WithClass(errDependency, errs.Invalid)

	b.Run("a first target that succeeds", func(b *testing.B) {
		br := mustBreaker(b, fake.New(originUTC))
		ctx := b.Context()

		var err error
		allocs(b, 0, func() { _, err = resilience.Failover(ctx, br, authorities, 0, succeed) })
		testkit.NoError(b, err, "the benchmark must measure a failover that succeeds")
	})

	b.Run("a second target that succeeds after the first fails", func(b *testing.B) {
		br := mustBreaker(b, fake.New(originUTC))
		ctx := b.Context()
		fn := func(_ context.Context, i int) (int, error) {
			if i == 0 {
				return 0, invalid
			}

			return 1, nil
		}

		var err error
		allocs(b, 0, func() { _, err = resilience.Failover(ctx, br, authorities, 0, fn) })
		testkit.NoError(b, err, "the benchmark must measure a failover that succeeds")
	})

	b.Run("every target fails with a Transient error", func(b *testing.B) {
		// A failure threshold above any count of runs keeps every circuit
		// closed, so each run calls every target.
		cfg := breakerConfig(fake.New(originUTC))
		cfg.FailureThreshold = math.MaxInt
		br, err := resilience.NewBreaker(cfg)
		testkit.NoError(b, err, "NewBreaker must accept a complete config")
		ctx := b.Context()
		transient := errs.WithClass(errDependency, errs.Transient)
		fn := func(context.Context, int) (int, error) { return 0, transient }

		allocs(b, 2, func() { _, err = resilience.Failover(ctx, br, authorities, 0, fn) })
		testkit.ErrorIs(b, err, errDependency, "the benchmark must measure a failover that fails")
	})

	b.Run("every target fails without a Transient error", func(b *testing.B) {
		br := mustBreaker(b, fake.New(originUTC))
		ctx := b.Context()
		fn := func(context.Context, int) (int, error) { return 0, invalid }

		var err error
		allocs(b, 5, func() { _, err = resilience.Failover(ctx, br, authorities, 0, fn) })
		testkit.ErrorIs(b, err, errDependency, "the benchmark must measure a failover that fails")
	})
}
