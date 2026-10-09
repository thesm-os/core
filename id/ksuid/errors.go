// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package ksuid

//go:generate testkit sentinel -o errors.gen_test.go

import (
	"errors"

	"go.thesmos.sh/core/errs"
)

// Sentinel errors returned by [Parse]. Each classifies as
// [go.thesmos.sh/core/errs.Invalid]: the input is not a KSUID, so the
// same call never succeeds.
var (
	// ErrInvalidLength is returned when the input is not exactly
	// 27 characters long.
	ErrInvalidLength = errs.WithClass(errors.New("ksuid: invalid length, want 27 characters"), errs.Invalid)

	// ErrInvalidChar is returned when the input contains a
	// character outside the base62 alphabet.
	ErrInvalidChar = errs.WithClass(errors.New("ksuid: invalid character"), errs.Invalid)

	// ErrOverflow is returned when the decoded value exceeds
	// 2^160 — possible only if the input encodes a value larger
	// than the 20-byte KSUID range (the alphabet permits values
	// up to 62^27 - 1, which is larger than 2^160).
	ErrOverflow = errs.WithClass(errors.New("ksuid: encoded value exceeds 160 bits"), errs.Invalid)
)
