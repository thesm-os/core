// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package telemetry

//go:generate testkit sentinel -o errors.gen_test.go

import (
	"errors"

	"go.thesmos.sh/core/errs"
)

// Sentinel errors of the constructors of this package. No method that
// emits returns an error.
var (
	// ErrConfig reports an argument of [NewShardedCounter],
	// [NewBoundedHistogram] or [NewRateLimitHandler] that the constructor
	// refuses: a nil instrument, handler or clock, a count or a rate below
	// 1, or an interval that is not positive. Classifies as
	// [go.thesmos.sh/core/errs.Invalid], because the same arguments fail
	// again.
	ErrConfig = errs.WithClass(errors.New("telemetry: invalid configuration"), errs.Invalid)
)
