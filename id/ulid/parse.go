// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package ulid

import (
	"encoding/binary"
	"slices"

	"go.thesmos.sh/core/id"
)

// alphabet is Crockford's base32 alphabet, without I, L, O and U.
const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// encodedLen is the length of the canonical text of a ULID: 26 characters
// of 5 bits each for the 128 bits of the ULID and 2 leading zero bits.
const encodedLen = 26

// Format returns the canonical text of the first 128 bits of u: 26
// characters of Crockford's base32 in uppercase, as the ULID specification
// defines it. It returns the empty string when u is shorter than
// [id.Size128], as [id.Zero] is. [AppendFormat] appends the same text to a
// buffer.
//
// The first 10 characters encode the 48-bit timestamp in 50 bits, so the
// first character is 0 to 7. The last 16 characters encode the 80 random
// bits.
//
// # Allocation contract
//
// Allocates the 26-byte result string.
func Format(u id.ID) string {
	b := u.Bytes()
	if len(b) < id.Size128 {
		return ""
	}

	var out [encodedLen]byte
	encode(&out, b)

	return string(out[:])
}

// AppendFormat appends the text that [Format] returns for u to dst and
// returns the extended slice. It returns dst unchanged when u is shorter
// than [id.Size128].
//
// # Allocation contract
//
// AppendFormat does not allocate when dst has room for 26 more bytes.
// Otherwise it allocates once to grow dst.
func AppendFormat(dst []byte, u id.ID) []byte {
	b := u.Bytes()
	if len(b) < id.Size128 {
		return dst
	}

	// The text goes into the 26 bytes after the end of dst.
	n := len(dst)
	dst = slices.Grow(dst, encodedLen)[:n+encodedLen]
	encode((*[encodedLen]byte)(dst[n:]), b)

	return dst
}

// encode writes the canonical text of the first 128 bits of b into out.
// The caller passes at least [id.Size128] bytes. encode does not
// allocate.
func encode(out *[encodedLen]byte, b []byte) {
	// The 10 characters of the timestamp half encode 50 bits: 2 zero bits
	// and the 48 bits of bytes 0 to 5. The loop writes them from the high
	// end down, so the first character encodes the top 3 timestamp bits.
	var ts uint64
	for _, x := range b[0:6] {
		ts = (ts << 8) | uint64(x)
	}
	for i := range 10 {
		shift := uint(50 - 5 - i*5)
		out[i] = alphabet[(ts>>shift)&0x1F]
	}

	// The 16 characters of the random half encode the 80 bits of bytes 6 to
	// 15. The first 12 characters encode the high 60 bits of hi. The last 4
	// encode the low 4 bits of hi and the 16 bits of lo.
	hi := binary.BigEndian.Uint64(b[6:14])
	lo := binary.BigEndian.Uint16(b[14:16])
	for i := range 12 {
		shift := uint(64 - 5 - i*5)
		out[10+i] = alphabet[(hi>>shift)&0x1F]
	}
	tail := uint32(hi&0x0F)<<16 | uint32(lo)
	for i := range 4 {
		shift := uint(20 - 5 - i*5)
		out[22+i] = alphabet[(tail>>shift)&0x1F]
	}
}

// ParseULID returns the [id.ID] of size [id.Size128] whose canonical text is
// s. It accepts both cases of a letter, and the substitutions of the ULID
// specification: I and L for 1, and O for 0.
//
// It returns [id.Zero] with each error:
//   - [ErrInvalidLength] when s is not 26 bytes long.
//   - [ErrInvalidTimestamp] when the first character is above 7, because the
//     50 bits of the timestamp half then encode more than 48 bits.
//   - [ErrInvalidChar] when s contains a byte outside Crockford's base32
//     alphabet.
//
// # Allocation contract
//
// Zero alloc.
func ParseULID(s string) (id.ID, error) {
	return decode(s)
}

// decode returns the ID of the ULID whose canonical text is s, a string or a
// byte slice. It accepts the texts that [ParseULID] accepts and returns what
// ParseULID returns. It reads a byte slice in place and does not allocate.
func decode[T ~string | ~[]byte](s T) (id.ID, error) {
	if len(s) != encodedLen {
		return id.Zero, ErrInvalidLength
	}

	// The first character encodes the top 2 of the 50 bits, which must be
	// zero, and the top 3 bits of the timestamp. The table gives -1 for a
	// byte outside the alphabet, which the loop below refuses.
	if crockfordTable[s[0]] > 7 {
		return id.Zero, ErrInvalidTimestamp
	}

	var raw [id.Size128]byte

	// The 10 characters of the timestamp half decode into the low 50 bits
	// of ts, whose top 2 bits are zero. The low 48 bits of ts go big-endian
	// into bytes 0 to 5.
	var ts uint64
	for i := range 10 {
		v := crockfordTable[s[i]]
		if v < 0 {
			return id.Zero, ErrInvalidChar
		}
		ts = (ts << 5) | uint64(v)
	}
	for i := 5; i >= 0; i-- {
		raw[i] = byte(ts & 0xFF)
		ts >>= 8
	}

	// The 16 characters of the random half decode into the 80 bits of bytes
	// 6 to 15. Each character shifts 5 bits into bits, and each full byte
	// moves from the high end of bits into raw.
	var bits uint64
	bitCount := 0
	out := 6
	for i := 10; i < encodedLen; i++ {
		v := crockfordTable[s[i]]
		if v < 0 {
			return id.Zero, ErrInvalidChar
		}
		bits = (bits << 5) | uint64(v)
		bitCount += 5
		if bitCount >= 8 {
			bitCount -= 8
			raw[out] = byte((bits >> bitCount) & 0xFF)
			out++
		}
	}

	return id.New128(raw), nil
}

// crockfordTable maps each byte to its value in Crockford's base32, 0 to 31,
// and every other byte to -1. Both cases of a letter map to one value. The
// table maps the substitutions of the ULID specification too: I and L to 1,
// and O to 0.
var crockfordTable = func() (t [256]int8) {
	for i := range t {
		t[i] = -1
	}
	// Digits 0..9 map directly.
	for c := byte('0'); c <= '9'; c++ {
		t[c] = int8(c - '0')
	}
	// The Crockford alphabet excludes I, L, O and U. The ULID
	// specification accepts I and L for 1 and O for 0, so that a
	// transcribed identifier decodes. U is no character of it.
	letterValue := map[byte]int8{
		'A': 10, 'B': 11, 'C': 12, 'D': 13, 'E': 14,
		'F': 15, 'G': 16, 'H': 17,
		'I': 1, // substitution
		'J': 18, 'K': 19,
		'L': 1, // substitution
		'M': 20, 'N': 21,
		'O': 0, // substitution
		'P': 22, 'Q': 23, 'R': 24, 'S': 25, 'T': 26,
		'V': 27, 'W': 28, 'X': 29, 'Y': 30, 'Z': 31,
	}
	for c, v := range letterValue {
		t[c] = v
		t[c+('a'-'A')] = v // lowercase variant
	}
	return t
}()
