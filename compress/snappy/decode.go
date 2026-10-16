// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package snappy

import (
	"encoding/binary"
	"math"
	"slices"
)

// MaxBlockLen is the largest number of bytes that one block encodes, 2^32 - 1,
// the largest length that the preamble of the format states.
const MaxBlockLen = 1<<32 - 1

// The kinds of an element, in the low two bits of its tag byte.
const (
	tagLiteral = 0x00
	tagCopy1   = 0x01
	tagCopy2   = 0x02
	tagCopy4   = 0x03
)

// The sizes of the fixed moves of the decoder.
const (
	// chunkLen is the size of the move of a short literal or a short copy.
	// The compiler emits a move of chunkLen bytes as one load and one store.
	chunkLen = 16
	// fastLen is the size of the move of a literal of more than chunkLen
	// bytes. move writes it as four moves of chunkLen bytes.
	fastLen = 64
)

// DecodedLen returns the number of bytes that the block src decodes to, from
// its preamble.
//
// Error modes:
//
//   - A preamble that is not a varint of at most MaxBlockLen returns
//     [ErrCorrupt].
//   - A length above the largest int returns [ErrTooLarge], on a platform
//     whose int has 32 bits.
//
// # Allocation contract
//
// DecodedLen does not allocate.
func DecodedLen(src []byte) (int, error) {
	n, _, err := decodedLen(src, math.MaxInt)

	return n, err
}

// AppendDecode appends the bytes that the block src decodes to, to dst, and
// returns the extended slice. dst and src must not overlap.
//
// AppendDecode grows dst once, by the length that the preamble states, and
// then checks each element against src and against that length before it
// copies the element. A caller that decodes untrusted blocks checks
// [DecodedLen] first, because a preamble of 5 bytes states up to 4 GiB.
//
// Error modes:
//
//   - A malformed block returns [ErrCorrupt], and dst unchanged.
//   - A length above the largest int returns [ErrTooLarge], and dst
//     unchanged, on a platform whose int has 32 bits.
//
// # Allocation contract
//
// AppendDecode does not allocate when dst has room for the decoded bytes.
// Otherwise it allocates the growth of dst.
func AppendDecode(dst, src []byte) ([]byte, error) {
	n, preamble, err := decodedLen(src, math.MaxInt)
	if err != nil {
		return dst, err
	}
	out := slices.Grow(dst, n)
	start := len(out)
	if err := decode(out[start:start+n], src[preamble:]); err != nil {
		return dst, err
	}

	return out[:start+n], nil
}

// decodedLen returns the length that the preamble of src states and the
// length of the preamble. limit is the largest int of the platform. It does
// not allocate.
//
// Error modes:
//
//   - A preamble that is not a varint of at most MaxBlockLen returns
//     ErrCorrupt.
//   - A length above limit returns ErrTooLarge.
func decodedLen(src []byte, limit uint64) (n, preamble int, err error) {
	v, preamble := binary.Uvarint(src)
	if preamble <= 0 || v > MaxBlockLen {
		return 0, 0, ErrCorrupt
	}
	if v > limit {
		return 0, 0, ErrTooLarge
	}

	return int(v), preamble, nil
}

// decode writes the elements of src to dst, which has the length that the
// preamble states. It returns ErrCorrupt for an element that the format does
// not allow, and for elements that fill fewer bytes than dst has. It does not
// allocate.
//
// Short elements move in fixed sizes while dst and src have room for them. A
// literal of up to chunkLen bytes moves as chunkLen bytes. A literal of up to
// fastLen bytes moves as fastLen bytes with move. A copy of up to chunkLen
// bytes whose offset is at least its length moves as chunkLen bytes. Its
// bytes come from before the copy, so the move reads them before it writes. A
// fixed move can write past the element. The elements after it write those
// bytes again.
//
// Each other element takes one call of copy. A copy whose offset is shorter
// than its length repeats a run. It copies forward byte by byte, because each
// byte reads the byte that the copy wrote offset bytes before it.
func decode(dst, src []byte) error {
	d, s := 0, 0
	for s < len(src) {
		tag := src[s]
		var length, offset uint64
		switch tag & 0x03 {
		case tagLiteral:
			length = uint64(tag>>2) + 1
			s++
			if length > 60 {
				// The tags 60 to 63 state the length minus one in the next 1 to 4
				// bytes, little-endian.
				k := int(length - 60)
				//dokimi:mutate-skip ror-boundary: with exactly k bytes left, the check of the literal against src below fails
				if k > len(src)-s {
					return ErrCorrupt
				}
				length = 0
				for i := range k {
					length |= uint64(src[s+i]) << (8 * i)
				}
				length++
				s += k
			}
			//nolint:gosec // G115: d is at most len(dst), and s at most len(src)
			if length > uint64(len(dst)-d) || length > uint64(len(src)-s) {
				return ErrCorrupt
			}
			n := int(length)
			//dokimi:mutate-skip ror-boundary,lcr-false: the copy of the last branch writes the same bytes as a fixed move
			if n <= chunkLen && len(dst)-d >= chunkLen && len(src)-s >= chunkLen {
				*(*[chunkLen]byte)(dst[d : d+chunkLen]) = [chunkLen]byte(src[s : s+chunkLen])
				//dokimi:mutate-skip ror-boundary,lcr-false: the copy of the last branch writes the same bytes as a fixed move
			} else if n <= fastLen && len(dst)-d >= fastLen && len(src)-s >= fastLen {
				move((*[fastLen]byte)(dst[d:d+fastLen]), (*[fastLen]byte)(src[s:s+fastLen]))
			} else {
				copy(dst[d:], src[s:s+n])
			}
			d += n
			s += n

			continue
		case tagCopy1:
			if len(src)-s < 2 {
				return ErrCorrupt
			}
			length = 4 + uint64(tag>>2&0x07)
			offset = uint64(tag&0xe0)<<3 | uint64(src[s+1])
			s += 2
		case tagCopy2:
			if len(src)-s < 3 {
				return ErrCorrupt
			}
			length = 1 + uint64(tag>>2)
			offset = uint64(binary.LittleEndian.Uint16(src[s+1 : s+3]))
			s += 3
		default:
			if len(src)-s < 5 {
				return ErrCorrupt
			}
			length = 1 + uint64(tag>>2)
			offset = uint64(binary.LittleEndian.Uint32(src[s+1 : s+5]))
			s += 5
		}
		if offset == 0 || offset > uint64(d) || length > uint64(len(dst)-d) {
			return ErrCorrupt
		}
		//nolint:gosec // G115: offset is at most d, and length at most len(dst) - d
		o, n := int(offset), int(length)
		//dokimi:mutate-skip ror-boundary,lcr-false: the copy and the loop of the other branches write the same bytes as a fixed move
		if o >= n && n <= chunkLen && len(dst)-d >= chunkLen {
			*(*[chunkLen]byte)(dst[d : d+chunkLen]) = [chunkLen]byte(dst[d-o : d-o+chunkLen])
			//dokimi:mutate-skip ror-boundary: a copy whose offset equals its length reads no byte that it writes, so the loop writes the same bytes
		} else if o >= n {
			copy(dst[d:d+n], dst[d-o:])
		} else {
			to := dst[d : d+n]
			from := dst[d-o:][:n]
			for i := range to {
				to[i] = from[i]
			}
		}
		d += n
	}
	if d != len(dst) {
		return ErrCorrupt
	}

	return nil
}

// move writes the fastLen bytes of src to dst in four moves of chunkLen
// bytes. The compiler emits a move of chunkLen bytes as one load and one
// store. It emits a move of fastLen bytes between arrays that can overlap as a
// call of memmove. move does not allocate.
func move(dst, src *[fastLen]byte) {
	*(*[chunkLen]byte)(dst[:chunkLen]) = [chunkLen]byte(src[:chunkLen])
	*(*[chunkLen]byte)(dst[chunkLen : 2*chunkLen]) = [chunkLen]byte(src[chunkLen : 2*chunkLen])
	*(*[chunkLen]byte)(dst[2*chunkLen : 3*chunkLen]) = [chunkLen]byte(src[2*chunkLen : 3*chunkLen])
	*(*[chunkLen]byte)(dst[3*chunkLen:]) = [chunkLen]byte(src[3*chunkLen:])
}
