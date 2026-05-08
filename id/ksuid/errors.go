// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package ksuid

//go:generate testkit sentinel -o errors.gen_test.go

import "errors"

// Sentinel errors returned by [Parse].
var (
	// ErrInvalidLength is returned when the input is not exactly
	// 27 characters long.
	ErrInvalidLength = errors.New("ksuid: invalid length, want 27 characters")

	// ErrInvalidChar is returned when the input contains a
	// character outside the base62 alphabet.
	ErrInvalidChar = errors.New("ksuid: invalid character")

	// ErrOverflow is returned when the decoded value exceeds
	// 2^160 — possible only if the input encodes a value larger
	// than the 20-byte KSUID range (the alphabet permits values
	// up to 62^27 - 1, which is larger than 2^160).
	ErrOverflow = errors.New("ksuid: encoded value exceeds 160 bits")
)
