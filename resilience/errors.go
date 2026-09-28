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
// [go.thesmos.sh/core/errs.Classify], so a retry wrapped around a
// breaker backs off rather than giving up. [ErrConfig] classifies as
// Invalid, because a retry cannot fix a configuration.
//
// [ErrFull] and [ErrWaitTimeout] have no class. A bulkhead bounds a
// dependency that is slow, and a breaker acts on one that has failed.
// Classified as Transient, a rejection would count as a failure against
// a breaker that trips on Transient, and would open its circuit against
// a dependency that is slow but healthy. A caller that retries a
// rejection tags it with [go.thesmos.sh/core/errs.WithClass].
var (
	// ErrConfig is returned by every constructor when a required
	// field is missing or out of range. A threshold left at zero
	// makes a primitive either useless or permanently closed, so the
	// constructor catches it at wiring time, before an outage
	// exposes it. Classifies as Invalid.
	ErrConfig = errs.WithClass(errors.New("resilience: invalid configuration"), errs.Invalid)

	// ErrOpen is returned by [Call] instead of reaching a dependency
	// whose circuit is open.
	//
	// Classifies as Transient: the dependency may recover, so a retry
	// wrapped around a breaker backs off rather than giving up.
	ErrOpen = errs.WithClass(errors.New("resilience: circuit open"), errs.Transient)

	// ErrFull is returned by [Bulkhead.Acquire] when the concurrency
	// limit and the queue are both full.
	//
	// Distinct from [ErrWaitTimeout]: this one means the dependency
	// is saturated right now, where a timeout means it is slow enough
	// that waiting was not worth it.
	ErrFull = errors.New("resilience: bulkhead limit and queue are full")

	// ErrWaitTimeout is returned by [Bulkhead.Acquire] when a queued
	// caller waited its full allowance without a permit coming free.
	ErrWaitTimeout = errors.New("resilience: timed out waiting for a permit")

	// ErrBudget is returned by [Do] when a retry would exceed the
	// [Retrier]'s budget.
	//
	// Classifies as Transient: the call itself may still be worth
	// retrying later, when the budget has recovered.
	ErrBudget = errs.WithClass(errors.New("resilience: retry budget exhausted"), errs.Transient)
)
