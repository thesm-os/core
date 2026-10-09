// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package pool

//go:generate testkit sentinel -o errors.gen_test.go

import (
	"errors"

	"go.thesmos.sh/core/errs"
)

// Sentinel errors returned by [NewBounded].
var (
	// ErrLimit is returned when a pool's capacity is not positive. A
	// pool that can hold nothing would block every [Bounded.Get]
	// forever, so it is rejected at construction rather than
	// discovered at the first call. It classifies as
	// [go.thesmos.sh/core/errs.Invalid].
	ErrLimit = errs.WithClass(errors.New("pool: limit must be greater than zero"), errs.Invalid)
)
