// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package ulid

//go:generate testkit sentinel -o errors.gen_test.go

import (
	"errors"

	"go.thesmos.sh/core/errs"
)

// Sentinel errors returned by [ParseULID]. Each classifies as
// [go.thesmos.sh/core/errs.Invalid]: the input is not a ULID, so the
// same call never succeeds.
var (
	// ErrInvalidLength is returned when the input is not exactly
	// 26 characters long.
	ErrInvalidLength = errs.WithClass(errors.New("ulid: invalid length, want 26 characters"), errs.Invalid)

	// ErrInvalidChar is returned when the input contains a
	// character outside the Crockford base32 alphabet (after
	// case folding and I/L/O substitution).
	ErrInvalidChar = errs.WithClass(errors.New("ulid: invalid character"), errs.Invalid)

	// ErrInvalidTimestamp is returned when the leading character
	// of the 10-char timestamp half encodes more than the 48
	// bits the ULID format reserves for the timestamp — the
	// first character must be in the range '0'..'7' (Crockford
	// indices 0..7), since the 50-bit timestamp slot's top 2
	// bits are required to be zero.
	ErrInvalidTimestamp = errs.WithClass(
		errors.New("ulid: timestamp overflow, first char must be 0-7"), errs.Invalid)
)
