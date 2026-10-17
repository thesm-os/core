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

// encodedLen is the length of the canonical text of a KSUID. 62^26 is less
// than 2^160 and 62^27 is more, so 27 characters encode every 160-bit value,
// with leading zeros for the smaller values.
const encodedLen = 27

// chunks is the number of big-endian 32-bit words of a 160-bit value, which
// the encoder and the decoder compute with.
const chunks = 5

// Format returns the canonical text of the first 160 bits of u: 27
// characters of base62. It returns the empty string when u is shorter than
// [id.Size160], as [id.Zero] is. [AppendFormat] appends the same text to a
// buffer.
//
// # Allocation contract
//
// Allocates the 27-byte result string.
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
	// The 20 bytes go into 5 big-endian 32-bit words. Each of the 27 rounds
	// divides the words by 62 and writes the remainder as the next digit
	// from the end.
	var num [chunks]uint32
	for i := range chunks {
		num[i] = binary.BigEndian.Uint32(b[i*4:])
	}

	for i := encodedLen - 1; i >= 0; i-- {
		// The long division walks the words from the most significant,
		// and carries the remainder of each word into the next.
		var rem uint64
		for j := range chunks {
			x := rem<<32 | uint64(num[j])
			num[j] = uint32(x / 62)
			rem = x % 62
		}
		out[i] = alphabet[rem]
	}
}

// Parse returns the [id.ID] of size [id.Size160] whose canonical text is s.
// The text is case-sensitive.
//
// It returns [id.Zero] with each error:
//   - [ErrInvalidLength] when s is not 27 bytes long.
//   - [ErrInvalidChar] when s contains a byte outside the base62 alphabet.
//   - [ErrOverflow] when the value of s is 2^160 or more.
//
// # Allocation contract
//
// Zero alloc.
func Parse(s string) (id.ID, error) {
	return decode(s)
}

// decode returns the ID of the KSUID whose canonical text is s, a string or
// a byte slice. It accepts the texts that [Parse] accepts and returns what
// Parse returns. It reads a byte slice in place and does not allocate.
func decode[T ~string | ~[]byte](s T) (id.ID, error) {
	if len(s) != encodedLen {
		return id.Zero, ErrInvalidLength
	}

	// Each digit multiplies the 5 words by 62 and adds the digit, with the
	// carry moving from the least significant word to the most. A carry out
	// of the most significant word means a value of 2^160 or more.
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

	var raw [id.Size160]byte
	for i := range chunks {
		binary.BigEndian.PutUint32(raw[i*4:], num[i])
	}

	return id.New160(raw), nil
}

// base62Table maps each byte to its value in base62, 0 to 61, and every
// other byte to -1.
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
