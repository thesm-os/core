// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package uuidv4

//go:generate testkit sentinel -o errors.gen_test.go

import (
	"errors"

	"go.thesmos.sh/core/errs"
)

// Sentinel errors returned by [Parse]. Each classifies as
// [go.thesmos.sh/core/errs.Invalid]: the input is not a UUID in the
// canonical layout, so the same call never succeeds.
var (
	// ErrInvalidLength is returned when the input is not
	// exactly 36 characters long (32 hex + 4 hyphens).
	ErrInvalidLength = errs.WithClass(errors.New("uuidv4: invalid length, want 36 characters"), errs.Invalid)

	// ErrInvalidFormat is returned when the input has the right
	// length but the hyphens are not at positions 8, 13, 18, 23
	// (the canonical RFC 4122 layout).
	ErrInvalidFormat = errs.WithClass(
		errors.New("uuidv4: invalid format, hyphens at wrong positions"), errs.Invalid)

	// ErrInvalidChar is returned when one of the hex segments
	// contains a non-hex character.
	ErrInvalidChar = errs.WithClass(errors.New("uuidv4: invalid hex character"), errs.Invalid)
)
