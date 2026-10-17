// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package ksuid

//go:generate testkit sentinel -o errors.gen_test.go

import (
	"errors"

	"go.thesmos.sh/core/errs"
)

// [Parse] and [KSUID.UnmarshalText] return these errors. Each classifies as
// [go.thesmos.sh/core/errs.Invalid]: a text that is not the text of a KSUID
// never parses, so a retry of the same call fails the same way.
var (
	// ErrInvalidLength reports a text that is not 27 bytes long.
	ErrInvalidLength = errs.WithClass(errors.New("ksuid: invalid length, want 27 characters"), errs.Invalid)

	// ErrInvalidChar reports a text with a byte that is not a character of
	// the base62 alphabet.
	ErrInvalidChar = errs.WithClass(errors.New("ksuid: invalid character"), errs.Invalid)

	// ErrOverflow reports a text whose value is 2^160 or more. The 27
	// characters of the alphabet encode values up to 62^27 - 1, and a KSUID
	// has 160 bits.
	ErrOverflow = errs.WithClass(errors.New("ksuid: encoded value exceeds 160 bits"), errs.Invalid)
)

// ErrInvalidBytes reports data that is not the binary form of a [KSUID]. The
// binary form is 20 bytes that are not all zero, and no bytes for the zero
// KSUID. [KSUID.UnmarshalBinary] returns ErrInvalidBytes for any other data.
// It classifies as [go.thesmos.sh/core/errs.Invalid].
var ErrInvalidBytes = errs.WithClass(
	errors.New("ksuid: invalid bytes, want none or 20 that are not all zero"), errs.Invalid,
)
