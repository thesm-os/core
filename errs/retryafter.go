// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package errs

import "time"

// RetryAfter returns the delay that err sets before the next attempt,
// and reports whether err sets one.
//
// An error reports a delay through a method RetryAfter() time.Duration
// on any error in its tree, and [WithRetryAfter] attaches one to an
// error whose type the producer did not define. RetryAfter walks err's
// chain and returns the first positive delay on it. At an error whose
// Unwrap returns []error, it returns the longest delay among the
// branches. A zero or negative delay counts as none.
//
// [Retryable] reports whether to retry, and the delay sets when, so a
// delay does not make an error retryable. A transport sets the delay
// from a server's instruction, such as HTTP's Retry-After header or
// gRPC's retry pushback, and converts an absolute time to a duration
// where it parses it.
//
// RetryAfter(nil) reports (0, false).
//
// # Allocation contract
//
// Zero alloc.
func RetryAfter(err error) (time.Duration, bool) {
	d := delayOf(err)

	return d, d > 0
}

// WithRetryAfter returns err tagged with the delay d. The result is
// errors.Is-comparable to err and keeps err's message and class, and
// [RetryAfter] returns d for it when d is positive.
//
// WithRetryAfter(nil, d) is nil, as [WithClass](nil, c) is.
//
// # Allocation contract
//
// One allocation for the wrapper.
func WithRetryAfter(err error, d time.Duration) error {
	if err == nil {
		return nil
	}

	return delayed{err: err, delay: d}
}

// delayOf returns the first positive delay on err's chain, the longest
// delay among the branches of the first join on it, or zero. The chain
// ends at a nil error or at an error without an Unwrap method.
func delayOf(err error) time.Duration {
	for {
		if x, ok := err.(interface{ RetryAfter() time.Duration }); ok {
			if d := x.RetryAfter(); d > 0 {
				return d
			}
		}

		// The switch is on the Unwrap contract itself, as in classOf,
		// and errors.As would allocate the pointer to its target.
		switch x := err.(type) { //nolint:errorlint // errors.As allocates, and the switch is on the Unwrap contract
		case interface{ Unwrap() error }:
			err = x.Unwrap()
		case interface{ Unwrap() []error }:
			var longest time.Duration
			for _, b := range x.Unwrap() {
				longest = max(longest, delayOf(b))
			}

			return longest
		default:
			return 0
		}
	}
}

// delayed is the [WithRetryAfter] wrapper. It is a value type, so the
// result is comparable, and it has Unwrap, so errors.Is, errors.As and
// [Classify] find the error it wraps.
type delayed struct {
	err   error
	delay time.Duration
}

// Error returns the wrapped error's message unchanged, as the message
// of a [WithClass] result is.
func (e delayed) Error() string { return e.err.Error() }

// Unwrap returns the wrapped error.
func (e delayed) Unwrap() error { return e.err }

// RetryAfter returns the delay that [WithRetryAfter] attached, which
// [RetryAfter] reads.
func (e delayed) RetryAfter() time.Duration { return e.delay }
