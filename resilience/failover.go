// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package resilience

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"go.thesmos.sh/core/errs"
)

// stackedTargets is the number of failed targets whose errors a
// [Failover] keeps on its stack. A failover that has more failed targets
// allocates the list of their errors.
const stackedTargets = 8

// Failover calls fn for one target after another, starting at
// targets[start % len(targets)] and wrapping around, until a call
// succeeds. The targets name redundant dependencies of one kind, such as
// the time-stamp authorities of a deployment, and fn receives the index
// of its target in targets. A caller that rotates the first target passes
// a counter as start.
//
// Each call runs under its target's circuit of b, as [Call] runs it.
// Failover skips a target whose circuit refuses the call without waiting
// for it, and records the outcome of each call that it makes in that
// target's circuit.
//
// Failover returns the first success. When ctx ends during a call,
// Failover returns that call's error and calls no further target. When
// ctx has ended before a call, Failover returns context.Cause(ctx).
//
// When every target fails or refuses, Failover returns an error that
// contains the error of each target in the order in which Failover tried
// them, with [ErrOpen] for a refused one. errors.Is and errors.As find each
// of them. The error classifies by the target nearest to a remedy:
//
//   - The error is Transient when the error of any target is
//     [errs.Transient], ErrOpen included. A later attempt can succeed
//     through that target. [errs.RetryAfter] reports the shortest delay
//     among the Transient errors. When one of them has no delay, it
//     reports none.
//   - Otherwise the error classifies as errors.Join of the targets' errors
//     classifies. RetryAfter reports the longest delay among them.
//
// errors.Join classifies by the target furthest from a remedy instead. A
// [Do] around a failover would then stop at the first target that, for
// example, a revocation denies, while another target only timed out.
//
// Error modes: an empty targets, a negative start, and a name that appears
// twice in targets return [ErrConfig], classified Invalid, and call no
// target. Entries of one name would share one circuit.
//
// # Composing with a retry
//
// A retry goes around a failover, so each attempt tries every target
// once:
//
//	resilience.Do(ctx, retrier, func(ctx context.Context) (T, error) {
//	    return resilience.Failover(ctx, breaker, targets, start, fn)
//	})
//
// # Concurrency
//
// Safe for concurrent use, as b is. Failover calls fn on the calling
// goroutine, for one target at a time.
//
// # Allocation contract
//
// Zero alloc when a call succeeds after at most eight failed targets, for
// targets that have circuits, apart from what fn allocates. Failover calls
// fn and does not keep it, so the compiler can keep a closure of the
// caller on the caller's stack. The error of a failover whose every
// target fails takes two allocations, and three more when no target's
// error is Transient.
func Failover[T any](
	ctx context.Context, b *Breaker, targets []string, start int,
	fn func(ctx context.Context, i int) (T, error),
) (T, error) {
	var zero T

	if start < 0 || len(targets) == 0 || duplicated(targets) {
		return zero, ErrConfig
	}

	var stack [stackedTargets]failed

	tried := stack[:0]

	first := start % len(targets)
	for n := range len(targets) {
		if ctx.Err() != nil {
			return zero, context.Cause(ctx)
		}

		i := (first + n) % len(targets)

		probe, ok := b.admit(targets[i])
		if !ok {
			tried = append(tried, failed{err: ErrOpen, target: targets[i]})

			continue
		}

		v, err := run(ctx, b, targets[i], probe, func(ctx context.Context) (T, error) { return fn(ctx, i) })
		if err == nil {
			return v, nil
		}

		if ctx.Err() != nil {
			return v, err
		}

		tried = append(tried, failed{err: err, target: targets[i]})
	}

	return zero, newFailoverError(tried)
}

// duplicated reports whether targets names a target twice. It compares
// every pair, without an allocation, for the few targets of a failover.
func duplicated(targets []string) bool {
	for i, t := range targets {
		if slices.Contains(targets[i+1:], t) {
			return true
		}
	}

	return false
}

// failed is the error of one target that a [Failover] tried.
type failed struct {
	err    error
	target string
}

// failoverError is the error of a [Failover] whose every target failed or
// refused: the error of each target, in the order in which the failover
// tried them, and the class and the delay that Failover describes.
//
// It has no Unwrap method. [errs.RetryAfter] reads the delays of the
// errors that an Unwrap returns when the delay of their parent is zero,
// so it would report the delay of a target that is not the nearest to a
// remedy. Is and As find the error of each target instead.
type failoverError struct {
	tried []failed
	delay time.Duration
	class errs.Class
}

// newFailoverError returns the error of a failover that tried the
// targets of tried, each of which failed or refused. It copies tried,
// which may be on the failover's stack.
func newFailoverError(tried []failed) error {
	e := &failoverError{tried: slices.Clone(tried)}
	e.class, e.delay = remedy(e.tried)

	return e
}

// remedy returns the class and the delay of a failover whose targets
// failed with tried, as [Failover] describes: Transient with the shortest
// delay among the Transient errors when there is one, and otherwise the
// class and the delay of their join.
func remedy(tried []failed) (errs.Class, time.Duration) {
	var (
		shortest  time.Duration
		transient bool
	)

	for _, f := range tried {
		if !errs.Retryable(f.err) {
			continue
		}

		d, _ := errs.RetryAfter(f.err)
		if transient {
			shortest = min(shortest, d)
		} else {
			shortest, transient = d, true
		}
	}

	if transient {
		return errs.Transient, shortest
	}

	all := make([]error, len(tried))
	for i, f := range tried {
		all[i] = f.err
	}

	joined := errors.Join(all...)
	d, _ := errs.RetryAfter(joined)

	return errs.Classify(joined), d
}

// Error lists the error of each target in the order of the failover:
// "resilience: every target failed: a: <error of a>; b: <error of b>".
func (e *failoverError) Error() string {
	var sb strings.Builder

	sb.WriteString("resilience: every target failed")

	sep := ": "
	for _, f := range e.tried {
		sb.WriteString(sep)
		sb.WriteString(f.target)
		sb.WriteString(": ")
		sb.WriteString(f.err.Error())

		sep = "; "
	}

	return sb.String()
}

// Is reports whether errors.Is finds target in the error of a target of
// the failover.
func (e *failoverError) Is(target error) bool {
	for _, f := range e.tried {
		if errors.Is(f.err, target) {
			return true
		}
	}

	return false
}

// As sets target to the first error of a target of the failover that
// errors.As matches, in the order of the failover, and reports whether it
// found one.
func (e *failoverError) As(target any) bool {
	for _, f := range e.tried {
		if errors.As(f.err, target) {
			return true
		}
	}

	return false
}

// Class returns the class of the failover that [Failover] describes, for
// [errs.Classify].
func (e *failoverError) Class() errs.Class { return e.class }

// RetryAfter returns the delay of the failover that [Failover] describes,
// for [errs.RetryAfter].
func (e *failoverError) RetryAfter() time.Duration { return e.delay }
