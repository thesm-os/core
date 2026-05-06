// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package ulid

import (
	"encoding/binary"

	"go.thesmos.sh/core/id"
)

// alphabet is Crockford's base32 alphabet (no I, L, O, U).
const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// Format returns the canonical 26-character Crockford base32
// encoding of u.
//
// Layout:
//
//	chars  0..9 : 48-bit timestamp prefix
//	chars 10..25: 80-bit random suffix
//
// The timestamp half encodes 50 bits of base-32 over a 48-bit
// payload; the leading 2 bits are always zero. The random half
// encodes 80 bits of base-32 over an 80-bit payload exactly.
//
// Returns the empty string if u is shorter than [id.Size128]
// (for example [id.Zero]).
//
// # Allocation contract
//
// Allocates the 26-byte result string.
func Format(u id.ID) string {
	b := u.Bytes()
	if len(b) < id.Size128 {
		return ""
	}

	// 50 bits for the 48-bit timestamp half, 80 bits for the
	// 80-bit random half: 130 bits total = 26 base-32 chars.
	// The first character carries only 2 of the 5 bit slots,
	// always 0 for valid timestamps; we still encode it so the
	// output is exactly 26 chars.
	var out [26]byte

	// Timestamp half: bytes 0..5 → chars 0..9.
	// Pack the 48 timestamp bits into a uint64 (high 16 bits
	// zero), then emit 10 base-32 chars from the high end down.
	var ts uint64
	for _, x := range b[0:6] {
		ts = (ts << 8) | uint64(x)
	}
	// 10 chars × 5 bits = 50 bits; shift the 48-bit payload up by
	// 2 so the top char is the high 2 bits (always 0).
	ts <<= 2
	for i := range 10 {
		shift := uint(50 - 5 - i*5)
		out[i] = alphabet[(ts>>shift)&0x1F]
	}

	// Random half: bytes 6..15 → chars 10..25.
	// 16 chars × 5 bits = 80 bits, exactly the 80 random bits.
	// Encode in two uint64s: high 64 bits cover bytes 6..13, low
	// 16 bits in another uint16 cover bytes 14..15.
	hi := binary.BigEndian.Uint64(b[6:14])
	lo := binary.BigEndian.Uint16(b[14:16])

	// First 12 chars from hi (60 bits, top-down), last 4 from
	// (hi & 0x0F) || lo (4 + 16 = 20 bits).
	for i := range 12 {
		shift := uint(64 - 5 - i*5)
		out[10+i] = alphabet[(hi>>shift)&0x1F]
	}
	tail := uint32(hi&0x0F)<<16 | uint32(lo)
	for i := range 4 {
		shift := uint(20 - 5 - i*5)
		out[22+i] = alphabet[(tail>>shift)&0x1F]
	}

	return string(out[:])
}

// ParseULID parses the canonical 26-character Crockford base32
// encoding of a ULID into an [id.ID] of size [id.Size128].
//
// Returns [ErrInvalidLength] if s is not exactly 26 characters,
// or [ErrInvalidChar] if s contains a character outside the
// Crockford base32 alphabet (case-insensitive — both upper and
// lower case are accepted, plus the Crockford I/L → 1, O → 0
// substitutions per the original ULID spec).
//
// The first character must be in the range '0'..'7' since the
// 50-bit timestamp half encodes only 48 bits of payload (the
// top 2 bits are zero for any valid ULID); a leading character
// '8'..'Z' produces a value beyond the 48-bit timestamp range
// and ParseULID returns [ErrInvalidTimestamp].
//
// # Allocation contract
//
// Allocates the returned [id.ID] only — zero string-handling
// allocations beyond that.
func ParseULID(s string) (id.ID, error) {
	if len(s) != 26 {
		return id.Zero, ErrInvalidLength
	}
	// First char carries only 2 bits; reject values that would
	// push the timestamp beyond 48 bits.
	first := decodeChar(s[0])
	if first < 0 {
		return id.Zero, ErrInvalidChar
	}
	if first > 7 {
		return id.Zero, ErrInvalidTimestamp
	}

	var raw [id.Size128]byte

	// Decode the 10-char timestamp half into bits 49..0 of a
	// uint64, then write the low 48 bits into bytes 0..5.
	var ts uint64
	for i := range 10 {
		v := decodeChar(s[i])
		if v < 0 {
			return id.Zero, ErrInvalidChar
		}
		ts = (ts << 5) | uint64(v)
	}
	// ts now holds 50 bits with the top 2 always zero (enforced
	// above). Right-shift by 2 to recover the 48-bit timestamp,
	// then write big-endian into bytes 0..5.
	ts >>= 2
	for i := 5; i >= 0; i-- {
		raw[i] = byte(ts & 0xFF)
		ts >>= 8
	}

	// Decode the 16-char random half into 80 bits = 10 bytes.
	// Process 5 bits at a time into a rolling 64-bit register;
	// emit bytes from the high end.
	var bits uint64
	bitCount := 0
	out := 6
	for i := 10; i < 26; i++ {
		v := decodeChar(s[i])
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

// crockfordTable maps each byte to its Crockford base32 value
// (0..31), or -1 for non-Crockford bytes. Both upper- and
// lower-case letters map to the same value, and the original
// ULID spec's I/L → 1, O → 0 substitutions are baked in.
//
// The lookup-table form is faster than a branching switch and
// removes per-character conditional code from the hot path.
var crockfordTable = func() (t [256]int8) {
	for i := range t {
		t[i] = -1
	}
	// Digits 0..9 map directly.
	for c := byte('0'); c <= '9'; c++ {
		t[c] = int8(c - '0')
	}
	// The Crockford alphabet excludes I, L, O, U; the ULID
	// spec retroactively accepts I/L → 1, O → 0 to handle
	// transcription. U is reserved (not a valid char).
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

// decodeChar returns the Crockford base32 value (0..31) for c,
// or -1 if c is not a valid Crockford character. Constant-time
// table lookup.
//
// # Allocation contract
//
// Zero alloc.
func decodeChar(c byte) int {
	return int(crockfordTable[c])
}
