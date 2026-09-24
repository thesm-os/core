// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package task

//go:generate testkit sentinel -o errors.gen_test.go

import (
	"errors"

	"go.thesmos.sh/core/errs"
)

// Sentinel errors returned by this package.
var (
	// ErrLimit is returned by [Each], [Map], [Stream], [Quorum] and
	// [Run] when limit is below one, before they start a goroutine or
	// call the caller's function.
	ErrLimit = errors.New("task: limit must be greater than zero")

	// ErrPeriod is returned by [Every] for a period that is not
	// positive, a negative jitter, or a positive jitter without a
	// random source, before it calls the caller's function. It
	// classifies as Invalid.
	ErrPeriod = errs.WithClass(errors.New("task: period must be positive and jitter non-negative"), errs.Invalid)

	// ErrQuorumSize is returned by [Quorum] when k is not between one
	// and the number of items, before it calls the caller's function.
	// It classifies as Invalid.
	ErrQuorumSize = errs.WithClass(
		errors.New("task: quorum must be between 1 and the number of items"), errs.Invalid)

	// ErrNoQuorum is returned by [Quorum], joined with the failures
	// that decided it, when fewer than k calls can still succeed. It
	// has no class of its own, so
	// [go.thesmos.sh/core/errs.Classify] returns the class of the
	// first classified failure joined with it.
	ErrNoQuorum = errors.New("task: quorum not met")

	// ErrClosed is returned by [Group.Go] when it is called after [Run]
	// has returned, because a group exists only inside the call to Run
	// that created it. ErrClosed classifies as Invalid under
	// [go.thesmos.sh/core/errs.Classify], and a retry cannot succeed.
	ErrClosed = errs.WithClass(errors.New("task: group closed"), errs.Invalid)

	// ErrExited is recorded when a task, or a function running on the
	// caller's goroutine, ends by [runtime.Goexit] instead of
	// returning. It classifies as Invalid, because a task that exits
	// without returning never reported its outcome, and the call cannot
	// report success without it.
	ErrExited = errs.WithClass(errors.New("task: exited without returning"), errs.Invalid)
)
