// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package snappy

import (
	"encoding/binary"
	"math"
	"math/bits"
	"slices"
)

// The layout of the encoder.
const (
	// blockLen is the length of the parts of a source that the encoder
	// encodes apart. A copy refers back at most blockLen bytes, so its offset
	// fits 16 bits.
	blockLen = 1 << 16
	// inputMargin is the number of bytes at the end of a part in which the
	// encoder looks for no match.
	inputMargin = 16 - 1
	// minMatchLen is the length of the shortest part in which the encoder
	// looks for a match: a literal of one byte, a copy of one byte and the
	// margin.
	minMatchLen = 1 + 1 + inputMargin
	// maxTableBits is the base-2 logarithm of the size of the largest hash
	// table.
	maxTableBits = 14
	// hashMul is the multiplier of the hash of 4 bytes.
	hashMul = 0x1e35a7bd
)

// MaxEncodedLen returns the largest length of the encoding of n bytes,
// 32 + n + n/6. A literal of one byte followed by a copy of 5 bytes encodes 6
// bytes of the source in 7.
//
// Error modes:
//
//   - An n that is negative or above MaxBlockLen returns [ErrTooLarge].
//   - A bound above the largest int returns ErrTooLarge, on a platform whose
//     int has 32 bits.
//
// # Allocation contract
//
// MaxEncodedLen does not allocate.
func MaxEncodedLen(n int) (int, error) {
	return maxEncodedLen(n, math.MaxInt)
}

// AppendEncode appends the Snappy block encoding of src to dst and returns
// the extended slice. dst and src must not overlap.
//
// AppendEncode grows dst once, to room for MaxEncodedLen(len(src)) more
// bytes, and encodes src in parts of 64 KiB. In each part it hashes each
// sequence of 4 bytes into a table of up to 2^14 positions, and emits a copy
// where the sequence at the recorded position matches. After 32 positions
// without a match it steps over more positions, so a source that does not
// compress encodes at the speed of a copy. It writes the same bytes as
// golang/snappy v1.0.0.
//
// Error modes:
//
//   - A src of more than MaxBlockLen bytes returns [ErrTooLarge], and dst
//     unchanged.
//
// # Allocation contract
//
// AppendEncode does not allocate when dst has room for
// MaxEncodedLen(len(src)) more bytes. Otherwise it allocates the growth of
// dst. Its hash table of 32 KiB is in its own frame.
func AppendEncode(dst, src []byte) ([]byte, error) {
	bound, err := maxEncodedLen(len(src), math.MaxInt)
	if err != nil {
		return dst, err
	}
	out := slices.Grow(dst, bound)
	start := len(out)
	buf := out[start : start+bound]
	d := binary.PutUvarint(buf, uint64(len(src)))
	for len(src) > 0 {
		part := src[:min(len(src), blockLen)]
		src = src[len(part):]
		//dokimi:mutate-skip ror-boundary: encodePart ends the search of a part of minMatchLen bytes before it, and emits one literal too
		if len(part) < minMatchLen {
			d += emitLiteral(buf[d:], part)
		} else {
			d += encodePart(buf[d:], part)
		}
	}

	return out[:start+d], nil
}

// maxEncodedLen returns 32 + n + n/6 for n bytes. limit is the largest int of
// the platform. It does not allocate.
//
// Error modes:
//
//   - An n that is negative or above MaxBlockLen returns ErrTooLarge.
//   - A bound above limit returns ErrTooLarge.
func maxEncodedLen(n int, limit uint64) (int, error) {
	//dokimi:mutate-skip lcr-right,ror-false: a negative n converts to 2^63 or more, which the comparison below refuses too
	if n < 0 ||
		uint64(n) > MaxBlockLen {

		return 0, ErrTooLarge
	}
	bound := 32 + uint64(n) + uint64(n)/6
	if bound > limit {
		return 0, ErrTooLarge
	}

	return int(bound), nil
}

// encodePart writes the elements of a part of the source of minMatchLen to
// blockLen bytes to dst, which has room for its bound, and returns the number
// of bytes that it wrote. It does not allocate.
//
// The table has 2^8 to 2^14 entries: the smallest power of two that is at
// least the length of the part. Each entry is the position in the part of
// the last sequence of 4 bytes whose hash it is. A hash multiplies the 4
// bytes by hashMul modulo 2^32 and keeps the high bits.
func encodePart(dst, src []byte) int {
	// bits.Len16 returns at most 16, so the compiler proves that the shift is
	// below 64. The shift of each hash then compiles to one instruction.
	//nolint:gosec // G115: a part has at most blockLen bytes
	tableBits := min(max(bits.Len16(uint16(len(src)-1)), 8), maxTableBits)
	shift := uint64(32 - tableBits)
	var table [1 << maxTableBits]uint16
	const (
		tableMask = 1<<maxTableBits - 1
		mask32    = 1<<32 - 1
	)
	le := binary.LittleEndian

	// The search stops inputMargin bytes before the end of the part. The
	// encoding starts with a literal, because a copy has no earlier bytes to
	// refer to, so the search starts at the second byte.
	limit := len(src) - inputMargin
	d, emitted, s := 0, 0, 1
	cur := le.Uint32(src[s : s+4])
	next := uint64(cur*hashMul) >> shift
search:
	for {
		// After 32 positions without a match, the search moves two positions
		// at a time, after 32 more three, and so on. At each comparison, cur
		// is the sequence at s and next is the hash of the sequence at nextS.
		// The sequence that the hash loads becomes cur when the search moves
		// to nextS.
		skip := 32
		nextS := s
		var candidate int
		for {
			s = nextS
			step := skip >> 5
			nextS = s + step
			skip += step
			if nextS > limit {
				break search
			}
			candidate = int(table[next&tableMask])
			table[next&tableMask] = uint16(s)
			v := le.Uint32(src[nextS : nextS+4])
			next = uint64(v*hashMul) >> shift
			if cur == le.Uint32(src[candidate:candidate+4]) {
				break
			}
			cur = v
		}

		d += emitLiteral(dst[d:], src[emitted:s])
		for {
			// The sequence at s matches the one at candidate. The match extends
			// as far as the bytes agree, and a copy encodes it. The loop compares
			// 8 bytes at a time while 8 remain. The lowest set bit of their
			// difference is in the first byte that differs.
			base := s
			s += 4
			i := candidate + 4
			//dokimi:mutate-skip sbr-delete,ror-boundary: the loop of single bytes below extends the match as far
			for len(src)-s >= 8 {
				//dokimi:mutate-skip ror-true: 8 equal bytes move s and i by 8, and the loop of single bytes continues the match
				if x := le.Uint64(src[s:s+8]) ^ le.Uint64(src[i:i+8]); x != 0 {
					n := bits.TrailingZeros64(x) >> 3
					s += n
					i += n

					break
				}
				s += 8
				i += 8
			}
			for s < len(src) && src[i] == src[s] {
				s++
				i++
			}
			d += emitCopy(dst[d:], base-candidate, s-base)
			emitted = s
			if s >= limit {
				break search
			}

			// The table takes the positions s-1 and s from one load of 8 bytes.
			// When the sequence at s matches its candidate, the next copy starts
			// there without a literal.
			x := le.Uint64(src[s-1 : s+7])
			table[(x*hashMul&mask32)>>shift&tableMask] = uint16(s - 1)
			current := ((x >> 8) * hashMul & mask32) >> shift
			candidate = int(table[current&tableMask])
			table[current&tableMask] = uint16(s)
			if (x>>8)&mask32 != uint64(le.Uint32(src[candidate:candidate+4])) {
				//nolint:gosec // G115: the low 32 bits of x>>16 are the 4 bytes at s+1
				cur = uint32(x >> 16)
				next = uint64(cur*hashMul) >> shift
				s++

				break
			}
		}
	}
	if emitted < len(src) {
		d += emitLiteral(dst[d:], src[emitted:])
	}

	return d
}

// emitLiteral writes a literal of lit, of 1 to blockLen bytes, to dst and
// returns the number of bytes that it wrote. A literal of up to 60 bytes
// keeps its length in its tag, and a longer one in 1 or 2 bytes after it. It
// does not allocate.
func emitLiteral(dst, lit []byte) int {
	n, i := len(lit)-1, 1
	if n < 60 {
		//nolint:gosec // G115: n is below 60
		dst[0] = byte(n)<<2 | tagLiteral
	} else if n < 1<<8 {
		dst[0], dst[1] = 60<<2|tagLiteral, byte(n)
		i = 2
	} else {
		//nolint:gosec // G115: the conversions keep the two bytes of an n below 65536
		dst[0], dst[1], dst[2] = 61<<2|tagLiteral, byte(n), byte(n>>8)
		i = 3
	}

	return i + copy(dst[i:], lit)
}

// emitCopy writes the copies of length bytes, from 4 to blockLen, at offset,
// from 1 to 65535, to dst and returns the number of bytes that it wrote. It
// does not allocate.
//
// The format allows at most 64 bytes in one copy. emitCopy writes copies of
// 64 bytes while 68 bytes or more remain, and one copy of 60 bytes for a rest
// of 65 to 67 bytes. The last copy then has at least 4 bytes. It takes the 2
// bytes of a copy with a 1-byte offset when its length is below 12 and its
// offset below 2048. The other copies take a 2-byte offset.
func emitCopy(dst []byte, offset, length int) int {
	i := 0
	for length >= 68 {
		//nolint:gosec // G115: the conversions keep the two bytes of an offset below 65536
		dst[i], dst[i+1], dst[i+2] = 63<<2|tagCopy2, byte(offset), byte(offset>>8)
		i += 3
		length -= 64
	}
	if length > 64 {
		//nolint:gosec // G115: the conversions keep the two bytes of an offset below 65536
		dst[i], dst[i+1], dst[i+2] = 59<<2|tagCopy2, byte(offset), byte(offset>>8)
		i += 3
		length -= 60
	}
	if length >= 12 || offset >= 2048 {
		//nolint:gosec // G115: a length below 65, and the two bytes of an offset below 65536
		dst[i], dst[i+1], dst[i+2] = byte(length-1)<<2|tagCopy2, byte(offset), byte(offset>>8)

		return i + 3
	}
	//nolint:gosec // G115: a length below 12, and an offset below 2048
	dst[i], dst[i+1] = byte(offset>>8)<<5|byte(length-4)<<2|tagCopy1, byte(offset)

	return i + 2
}
