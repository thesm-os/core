// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package ulid

//go:generate testkit sentinel -o errors.gen_test.go

import (
	"errors"

	"go.thesmos.sh/core/errs"
)

// [ParseULID] and [ULID.UnmarshalText] return these errors. Each classifies
// as [go.thesmos.sh/core/errs.Invalid]: a text that is not the text of a ULID
// never parses, so a retry of the same call fails the same way.
var (
	// ErrInvalidLength reports a text that is not 26 bytes long.
	ErrInvalidLength = errs.WithClass(errors.New("ulid: invalid length, want 26 characters"), errs.Invalid)

	// ErrInvalidChar reports a text with a byte that is not a character of
	// Crockford's base32 alphabet. The alphabet includes the lowercase
	// letters and the substitutions I, L and O of the ULID specification.
	ErrInvalidChar = errs.WithClass(errors.New("ulid: invalid character"), errs.Invalid)

	// ErrInvalidTimestamp reports a text whose first character is above 7.
	// The first character encodes the top 2 of the 50 bits of the timestamp
	// half, and a 48-bit timestamp leaves both bits zero.
	ErrInvalidTimestamp = errs.WithClass(
		errors.New("ulid: timestamp overflow, first char must be 0-7"), errs.Invalid,
	)
)

// ErrInvalidBytes reports data that is not the binary form of a [ULID]. The
// binary form is 16 bytes that are not all zero, and no bytes for the zero
// ULID. [ULID.UnmarshalBinary] returns ErrInvalidBytes for any other data.
// It classifies as [go.thesmos.sh/core/errs.Invalid].
var ErrInvalidBytes = errs.WithClass(
	errors.New("ulid: invalid bytes, want none or 16 that are not all zero"), errs.Invalid,
)
