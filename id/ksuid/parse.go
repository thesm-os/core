// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package ksuid

import (
	"encoding/binary"
	"slices"

	"go.thesmos.sh/core/id"
)

// alphabet is the canonical base62 alphabet of 62 characters: 0..9, A..Z
// and a..z, in the order of their values.
const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// encodedLen is the canonical KSUID encoded length:
// 62^27 > 2^160 > 62^26, so 27 base62 chars cover every 160-bit
// value with leading-zero padding for shorter values.
const encodedLen = 27

// chunks splits the 20-byte 160-bit value into 5 big-endian
// uint32 chunks, halving the per-iteration work versus
// byte-level long division (5 vs 20 inner iterations per
// extracted base62 digit).
const chunks = 5

// Format returns the canonical 27-character base62 encoding of the
// first 160 bits of u, for diagnostics and serialization.
// [AppendFormat] appends the same text to a buffer.
//
// Returns the empty string if u is shorter than [id.Size160]
// (for example [id.Zero]).
//
// # Allocation contract
//
// Format allocates the 27-byte result string.
func Format(u id.ID) string {
	b := u.Bytes()
	if len(b) < id.Size160 {
		return ""
	}

	var out [encodedLen]byte
	encode(&out, b)

	return string(out[:])
}

// AppendFormat appends the text that [Format] returns for u to dst and
// returns the extended slice. It returns dst unchanged when u is shorter
// than [id.Size160].
//
// # Allocation contract
//
// AppendFormat does not allocate when dst has room for 27 more bytes.
// Otherwise it allocates once to grow dst.
func AppendFormat(dst []byte, u id.ID) []byte {
	b := u.Bytes()
	if len(b) < id.Size160 {
		return dst
	}

	// The text goes into the 27 bytes after the end of dst.
	n := len(dst)
	dst = slices.Grow(dst, encodedLen)[:n+encodedLen]
	encode((*[encodedLen]byte)(dst[n:]), b)

	return dst
}

// encode writes the canonical base62 text of the first 160 bits of b into
// out. The caller passes at least [id.Size160] bytes. encode does not
// allocate.
func encode(out *[encodedLen]byte, b []byte) {
	// Pack the 20 bytes as 5 big-endian uint32 chunks, then
	// divide-by-62 across the chunks. 62^27 > 2^160, so 27
	// extracted digits cover every 160-bit value with
	// leading-zero padding.
	var num [chunks]uint32
	for i := range chunks {
		num[i] = binary.BigEndian.Uint32(b[i*4:])
	}

	for i := encodedLen - 1; i >= 0; i-- {
		// Long division across the 5 chunks: walk MSB→LSB,
		// folding the running remainder into the next chunk's
		// 32-bit value before dividing by 62.
		var rem uint64
		for j := range chunks {
			x := rem<<32 | uint64(num[j])
			num[j] = uint32(x / 62)
			rem = x % 62
		}
		out[i] = alphabet[rem]
	}
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
// Parse does not allocate. It returns the [id.ID] by value.
func Parse(s string) (id.ID, error) {
	if len(s) != encodedLen {
		return id.Zero, ErrInvalidLength
	}

	// Multiply-and-add across 5 uint32 chunks: for each input
	// digit, num = num*62 + digit, propagating carry across
	// chunks. After 27 digits, num contains the decoded 160-bit
	// value; a non-zero carry past chunk 0 means the input
	// encoded a value > 2^160.
	var num [chunks]uint32
	for i := range encodedLen {
		v := base62Table[s[i]]
		if v < 0 {
			return id.Zero, ErrInvalidChar
		}
		// base62Table gives v in [0, 61] here. The multiply needs it
		// as a uint64.
		//#nosec G115 -- v is constrained to [0, 61]
		carry := uint64(v)
		for j := chunks - 1; j >= 0; j-- {
			x := uint64(num[j])*62 + carry
			num[j] = uint32(x)
			carry = x >> 32
		}
		if carry != 0 {
			return id.Zero, ErrOverflow
		}
	}

	var dst [id.Size160]byte
	for i := range chunks {
		binary.BigEndian.PutUint32(dst[i*4:], num[i])
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
