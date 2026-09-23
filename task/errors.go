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
	// ErrLimit is returned by [Each], [Map], [Stream] and [Run] when
	// limit is below one, before they start a goroutine or call the
	// caller's function.
	ErrLimit = errors.New("task: limit must be greater than zero")

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
