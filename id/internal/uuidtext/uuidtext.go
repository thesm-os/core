// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package uuidtext

import (
	"encoding/hex"
	"slices"

	"go.thesmos.sh/core/id"
)

// Len is the length in bytes of the text form of a UUID: 32 hexadecimal
// digits and 4 hyphens. The digits of bytes 0-3, 4-5, 6-7, 8-9 and 10-15
// of the UUID are at text bytes 0-7, 9-12, 14-17, 19-22 and 24-35, and the
// hyphens at text bytes 8, 13, 18 and 23.
const Len = 36

// hyphen separates the groups of hexadecimal digits of the text form.
const hyphen = '-'

// Errors are the errors that [Parse] and [DecodeBinary] return. A package
// passes its own sentinel errors, so that its callers match them with
// errors.Is.
type Errors struct {
	// Length is the error for a text that is not [Len] bytes long.
	Length error

	// Format is the error for a text without a hyphen at byte 8, 13, 18
	// or 23.
	Format error

	// Char is the error for a group that contains a byte other than a
	// hexadecimal digit.
	Char error

	// Bytes is the error for data that is not the binary form of a UUID
	// value: data whose length is not 0 or [id.Size128], and [id.Size128]
	// zero bytes.
	Bytes error
}

// Format returns the text form of u, in lowercase, or the empty string
// when u is not 128 bits. [AppendFormat] appends the same text to a
// buffer.
//
// # Allocation contract
//
// Allocates the returned string.
func Format(u id.ID) string {
	if u.Size() != id.Size128 {
		return ""
	}

	var text [Len]byte
	encode(&text, u.Bytes())

	return string(text[:])
}

// AppendFormat appends the text that [Format] returns for u to dst and
// returns the extended slice. It returns dst unchanged when u is not 128
// bits.
//
// # Allocation contract
//
// AppendFormat does not allocate when dst has room for [Len] more bytes.
// Otherwise it allocates once to grow dst.
func AppendFormat(dst []byte, u id.ID) []byte {
	if u.Size() != id.Size128 {
		return dst
	}

	// The text goes into the Len bytes after the end of dst.
	n := len(dst)
	dst = slices.Grow(dst, Len)[:n+Len]
	encode((*[Len]byte)(dst[n:]), u.Bytes())

	return dst
}

// Parse returns the 128-bit ID whose text form is s, a string or a byte
// slice. It accepts uppercase, lowercase and mixed-case hexadecimal digits,
// as the grammar of RFC 9562 allows, and does not check the version or the
// variant.
//
// It returns [id.Zero] and e.Length when s is not [Len] bytes long,
// e.Format when a hyphen is missing, and e.Char when a group contains a
// byte other than a hexadecimal digit.
//
// # Allocation contract
//
// Zero alloc. Parse reads a byte slice in place and does not convert it to a
// string.
func Parse[T ~string | ~[]byte](s T, e Errors) (id.ID, error) {
	if len(s) != Len {
		return id.Zero, e.Length
	}

	if s[8] != hyphen || s[13] != hyphen || s[18] != hyphen || s[23] != hyphen {
		return id.Zero, e.Format
	}

	var raw [id.Size128]byte
	if _, err := hex.Decode(raw[0:4], []byte(s[0:8])); err != nil {
		return id.Zero, e.Char
	}

	if _, err := hex.Decode(raw[4:6], []byte(s[9:13])); err != nil {
		return id.Zero, e.Char
	}

	if _, err := hex.Decode(raw[6:8], []byte(s[14:18])); err != nil {
		return id.Zero, e.Char
	}

	if _, err := hex.Decode(raw[8:10], []byte(s[19:23])); err != nil {
		return id.Zero, e.Char
	}

	if _, err := hex.Decode(raw[10:16], []byte(s[24:36])); err != nil {
		return id.Zero, e.Char
	}

	return id.New128(raw), nil
}

// encode writes the text form of the 16 bytes b into text.
func encode(text *[Len]byte, b []byte) {
	hex.Encode(text[0:8], b[0:4])
	text[8] = hyphen
	hex.Encode(text[9:13], b[4:6])
	text[13] = hyphen
	hex.Encode(text[14:18], b[6:8])
	text[18] = hyphen
	hex.Encode(text[19:23], b[8:10])
	text[23] = hyphen
	hex.Encode(text[24:36], b[10:16])
}
