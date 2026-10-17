// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package uuidv4

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

// Format returns the text form of u, "xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx",
// in lowercase hexadecimal, or the empty string when u is not 128 bits.
// Format does not check the version or the variant of u. [AppendFormat]
// appends the same text to a buffer.
//
// # Allocation contract
//
// Allocates the 36-byte result string.
func Format(u id.ID) string {
	return uuidtext.Format(u)
}

// AppendFormat appends the text that [Format] returns for u to dst and
// returns the extended slice. It returns dst unchanged when u is not 128
// bits.
//
// # Allocation contract
//
// AppendFormat does not allocate when dst has room for 36 more bytes.
// Otherwise it allocates once to grow dst.
func AppendFormat(dst []byte, u id.ID) []byte {
	return uuidtext.AppendFormat(dst, u)
}

// Parse returns the 128-bit [id.ID] whose text form is s. It accepts
// uppercase and lowercase hexadecimal digits, and does not check the
// version or the variant.
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
