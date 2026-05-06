// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package uuidv4

import "errors"

// Sentinel errors returned by [Parse].
var (
	// ErrInvalidLength is returned when the input is not
	// exactly 36 characters long (32 hex + 4 hyphens).
	ErrInvalidLength = errors.New("uuidv4: invalid length, want 36 characters")

	// ErrInvalidFormat is returned when the input has the right
	// length but the hyphens are not at positions 8, 13, 18, 23
	// (the canonical RFC 4122 layout).
	ErrInvalidFormat = errors.New("uuidv4: invalid format, hyphens at wrong positions")

	// ErrInvalidChar is returned when one of the hex segments
	// contains a non-hex character.
	ErrInvalidChar = errors.New("uuidv4: invalid hex character")
)
