// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package filter

import (
	"encoding/binary"
	"math/bits"
)

// The layout of the encoding of a fuse filter. Its integers are big-endian.
const (
	// fuseVersion is the version of the encoding, in its first byte.
	fuseVersion = 1
	// headerLen is the length of the header: the version, the width of the
	// fingerprints, the segment exponent, the segment count of 4 bytes and the
	// seed of 8 bytes.
	headerLen = 15
	// padLen is the number of zero bytes after the fingerprints. A query
	// reads every fingerprint with a load of 4 bytes, the last one included.
	padLen = 3
	// minExponent and maxExponent bound the segment exponent.
	minExponent = 2
	maxExponent = 18
)

// Fuse is a 3-wise binary fuse filter over its encoding, which
// [FuseBuilder.Append] builds. It reads the encoding in place and keeps a
// reference to it.
//
// A Fuse reports true for every hash of the set that built it. For another
// hash, it reports true with probability 2^-bits for fingerprints of bits
// bits: 6.25% at 4 bits, 0.39% at 8, 0.098% at 10, 0.024% at 12 and 0.0015%
// at 16. A query reads three fingerprints at once.
//
// The zero Fuse is a filter of no hashes. Its MayContain reports false for
// every hash, and its Bits returns 0.
//
// # Concurrency
//
// A Fuse is safe for concurrent use while its encoding does not change.
//
// # Allocation contract
//
// [ParseFuse] and the methods do not allocate.
type Fuse struct {
	fingerprints []byte // the fingerprint bytes of the encoding, then its three zero bytes
	seed         uint64
	span         uint64 // the segment count times the segment length, 0 for a filter of no hashes
	segment      uint64 // the segment length, 2^e fingerprints
	width        uint8
}

// ParseFuse checks the encoding data and returns the filter over it. It
// does not copy data, so the caller must not change data while it uses the
// filter.
//
// Error modes:
//
//   - An unknown version, an unsupported width of fingerprints, a segment
//     exponent outside 2 to 18, a length that does not match the header, or
//     a padding byte that is not zero returns [ErrCorrupt].
//
// # Allocation contract
//
// ParseFuse does not allocate.
func ParseFuse(data []byte) (Fuse, error) {
	if len(data) < headerLen+padLen || data[0] != fuseVersion {
		return Fuse{}, ErrCorrupt
	}
	width, e := data[1], data[2]
	switch width {
	case 4, 8, 10, 12, 16:
	default:
		return Fuse{}, ErrCorrupt
	}
	if e < minExponent || e > maxExponent {
		return Fuse{}, ErrCorrupt
	}
	segments := uint64(binary.BigEndian.Uint32(data[3:]))
	count := uint64(0)
	if segments > 0 {
		count = (segments + 2) << e
	}
	end := headerLen + count*uint64(width)/8
	if uint64(len(data)) != end+padLen || data[end] != 0 || data[end+1] != 0 || data[end+2] != 0 {
		return Fuse{}, ErrCorrupt
	}

	return Fuse{
		fingerprints: data[headerLen:],
		seed:         binary.BigEndian.Uint64(data[7:]),
		span:         segments << e,
		segment:      1 << e,
		width:        width,
	}, nil
}

// MayContain reports whether the filter may contain h. It returns true for
// every hash of the set that built the filter.
//
// Each width reads its fingerprints with a load of its own: a byte for 8
// bits, a big-endian word of 16 bits for 16 bits, and the byte or the 16-bit
// window that contains the fingerprint for 4, 10 and 12 bits, shifted by a
// count that the position sets.
func (f Fuse) MayContain(h uint64) bool {
	if f.span == 0 {
		return false
	}
	x := murmur64(h + f.seed)
	p0, p1, p2 := positions(x, f.segment, f.span)
	fp, d, be := x^x>>32, f.fingerprints, binary.BigEndian
	switch f.width {
	case 4:
		got := d[p0>>1]>>(^p0&1<<2) ^ d[p1>>1]>>(^p1&1<<2) ^ d[p2>>1]>>(^p2&1<<2)

		return fp&0xF == uint64(got&0xF)
	case 8:
		return fp&0xFF == uint64(d[p0]^d[p1]^d[p2])
	case 10:
		got := be.Uint16(d[5*p0>>2:])>>(^p0&3<<1) ^ be.Uint16(d[5*p1>>2:])>>(^p1&3<<1) ^
			be.Uint16(d[5*p2>>2:])>>(^p2&3<<1)

		return fp&0x3FF == uint64(got&0x3FF)
	case 12:
		got := be.Uint16(d[3*p0>>1:])>>(^p0&1<<2) ^ be.Uint16(d[3*p1>>1:])>>(^p1&1<<2) ^
			be.Uint16(d[3*p2>>1:])>>(^p2&1<<2)

		return fp&0xFFF == uint64(got&0xFFF)
	}

	return fp&0xFFFF == uint64(be.Uint16(d[2*p0:])^be.Uint16(d[2*p1:])^be.Uint16(d[2*p2:]))
}

// Bits returns the width of the fingerprints in bits, and 0 for the zero
// Fuse.
func (f Fuse) Bits() int {
	return int(f.width)
}

// murmur64 returns the finalizer of MurmurHash3 of x, which mixes a hash and
// the seed of a filter.
func murmur64(x uint64) uint64 {
	x ^= x >> 33
	x *= 0xff51afd7ed558ccd
	x ^= x >> 33
	x *= 0xc4ceb9fe1a85ec53

	return x ^ x>>33
}

// positions returns the three positions of the mixed hash x in a filter whose
// segments have segment fingerprints, a power of two, where span is the
// segment count times the segment length. The positions lie in three
// consecutive segments.
func positions(x, segment, span uint64) (p0, p1, p2 uint64) {
	p0, _ = bits.Mul64(x, span)
	p1 = (p0 + segment) ^ (x >> 18 & (segment - 1))
	p2 = (p0 + 2*segment) ^ (x & (segment - 1))

	return p0, p1, p2
}
