// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package uuidv7

import (
	"go.thesmos.sh/core/id"
	"go.thesmos.sh/core/id/internal/uuidtext"
)

// parseErrors are the sentinel errors that [Parse] returns.
var parseErrors = uuidtext.Errors{
	Length: ErrInvalidLength,
	Format: ErrInvalidFormat,
	Char:   ErrInvalidChar,
}

// Format returns the text form of u, "xxxxxxxx-xxxx-7xxx-yxxx-xxxxxxxxxxxx",
// in lowercase hexadecimal, or the empty string when u is not 128 bits.
// Format does not check the version or the variant of u. [Valid] does.
//
// # Allocation contract
//
// Allocates the 36-byte result string.
func Format(u id.ID) string {
	return uuidtext.Format(u)
}

// Parse returns the 128-bit [id.ID] whose text form is s. It accepts
// uppercase and lowercase hexadecimal digits, and does not check the
// version or the variant. [Valid] does.
//
// It returns [id.Zero] and [ErrInvalidLength] when s is not 36 bytes long,
// [ErrInvalidFormat] when a hyphen is missing from byte 8, 13, 18 or 23,
// and [ErrInvalidChar] when a group contains a byte other than a
// hexadecimal digit.
//
// # Allocation contract
//
// Zero alloc.
func Parse(s string) (id.ID, error) {
	return uuidtext.Parse(s, parseErrors)
}
