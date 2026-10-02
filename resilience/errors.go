// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package resilience

//go:generate testkit sentinel -o errors.gen_test.go

import (
	"errors"

	"go.thesmos.sh/core/errs"
)

// Sentinel errors returned by this package.
//
// [ErrOpen] and [ErrBudget] classify as Transient under
// [go.thesmos.sh/core/errs.Classify], so a retry wrapped around a breaker
// backs off and tries again later. [ErrConfig] and [ErrUnits] classify as
// Invalid, because the same call fails the same way every time.
//
// [ErrFull] and [ErrWaitTimeout] have no class. A bulkhead bounds a
// dependency that is slow, and a breaker acts on one that has failed.
// Classified as Transient, a rejection would count as a failure against a
// breaker that trips on Transient, and would open its circuit against a
// dependency that is slow but healthy. A caller that retries a rejection
// tags it with [go.thesmos.sh/core/errs.WithClass].
var (
	// ErrConfig is returned by every constructor when a required field
	// is missing or out of range. A threshold of zero makes a primitive
	// useless or permanently closed, so the constructor refuses it when
	// the caller wires the primitive. Classifies as Invalid.
	ErrConfig = errs.WithClass(errors.New("resilience: invalid configuration"), errs.Invalid)

	// ErrOpen is returned by [Call] instead of a call to a dependency
	// whose circuit is open. Classifies as Transient: the dependency may
	// recover, so a retry wrapped around a breaker backs off and tries
	// again later.
	ErrOpen = errs.WithClass(errors.New("resilience: circuit open"), errs.Transient)

	// ErrFull is returned by [Bulkhead.Acquire] when the concurrency
	// limit and the queue are both full: the dependency is saturated at
	// the time of the call. [ErrWaitTimeout] is distinct, and reports a
	// dependency too slow to wait for.
	ErrFull = errors.New("resilience: bulkhead limit and queue are full")

	// ErrWaitTimeout is returned by [Bulkhead.Acquire] when a queued
	// caller waited its full allowance and no permit came free.
	ErrWaitTimeout = errors.New("resilience: timed out waiting for a permit")

	// ErrBudget is returned by [Do] when a retry would exceed the
	// [Retrier]'s budget. Classifies as Transient: the call may succeed
	// later, once the budget has recovered.
	ErrBudget = errs.WithClass(errors.New("resilience: retry budget exhausted"), errs.Transient)

	// ErrUnits is returned by [Limiter.WaitN] for a negative number of
	// units, and for a number above the limiter's burst, which its bucket
	// never contains. A caller splits such work into calls of at most the
	// burst. Classifies as Invalid.
	ErrUnits = errs.WithClass(errors.New("resilience: units outside 0 to the limiter's burst"), errs.Invalid)
)
