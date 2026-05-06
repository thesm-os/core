// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package ksuid

import (
	"go.thesmos.sh/core/id"
)

// alphabet is the canonical base62 alphabet:
// 0..9, A..Z, a..z. 62 characters.
const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// encodedLen is the canonical KSUID encoded length:
// 62^27 > 2^160 > 62^26, so 27 base62 chars cover every 160-bit
// value with leading-zero padding for shorter values.
const encodedLen = 27

// Format returns the canonical 27-character base62 encoding of
// u. Diagnostic and serialization use; allocates the result
// string.
//
// Returns the empty string if u is shorter than [id.Size160]
// (for example [id.Zero]).
//
// # Allocation contract
//
// Allocates the 27-byte result string.
func Format(u id.ID) string {
	b := u.Bytes()
	if len(b) < id.Size160 {
		return ""
	}
	// Work on a copy of the 20 bytes — the divide-by-62 loop
	// mutates the buffer.
	var src [id.Size160]byte
	copy(src[:], b[:id.Size160])

	var out [encodedLen]byte
	for i := encodedLen - 1; i >= 0; i-- {
		// Divide src (treated as big-endian 160-bit unsigned)
		// by 62. Last remainder is the lowest base62 digit.
		var carry uint16
		for j := range src {
			combined := carry*256 + uint16(src[j])
			//#nosec G115 -- combined/62 ≤ 1056, low byte fits
			src[j] = byte(combined / 62)
			carry = combined % 62
		}
		out[i] = alphabet[carry]
	}
	return string(out[:])
}

// Parse parses the canonical 27-character base62 encoding of a
// KSUID into an [id.ID] of size [id.Size160].
//
// Returns [ErrInvalidLength] if s is not exactly 27 characters,
// [ErrInvalidChar] if s contains a character outside the base62
// alphabet, or [ErrOverflow] if the encoded value exceeds 2^160.
//
// # Allocation contract
//
// Allocates the returned [id.ID] only.
func Parse(s string) (id.ID, error) {
	if len(s) != encodedLen {
		return id.Zero, ErrInvalidLength
	}
	var dst [id.Size160]byte
	for i := range encodedLen {
		v := decodeChar(s[i])
		if v < 0 {
			return id.Zero, ErrInvalidChar
		}
		// dst = dst * 62 + v, as a big-endian 160-bit unsigned.
		// v is guaranteed 0..61 by decodeChar; safe to widen.
		//#nosec G115 -- v ∈ [0, 61], well within uint16
		carry := uint16(v)
		for j := len(dst) - 1; j >= 0; j-- {
			combined := uint16(dst[j])*62 + carry
			//#nosec G115 -- low byte of combined; remainder is in carry
			dst[j] = byte(combined)
			carry = combined >> 8
		}
		if carry != 0 {
			return id.Zero, ErrOverflow
		}
	}
	return id.New160(dst), nil
}

// base62Table maps each byte to its base62 value (0..61), or -1
// for non-base62 bytes. The lookup-table form is faster than a
// branching switch and removes per-character conditional code
// from the hot path.
var base62Table = func() (t [256]int8) {
	for i := range t {
		t[i] = -1
	}
	for c := byte('0'); c <= '9'; c++ {
		t[c] = int8(c - '0')
	}
	for c := byte('A'); c <= 'Z'; c++ {
		t[c] = int8(c-'A') + 10
	}
	for c := byte('a'); c <= 'z'; c++ {
		t[c] = int8(c-'a') + 36
	}
	return t
}()

// decodeChar returns the base62 value (0..61) for c, or -1 if c
// is not a valid base62 character. Constant-time table lookup.
//
// # Allocation contract
//
// Zero alloc.
func decodeChar(c byte) int {
	return int(base62Table[c])
}
