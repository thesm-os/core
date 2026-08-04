// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package fixed

import "encoding/binary"

// AppendBinary appends the canonical [Size]-byte encoding of f's raw
// value to dst:
//
//	Raw   int64   8 bytes, big-endian, two's complement
//
// The encoding is a stable wire contract. Fixed64 values are signed
// over and persisted in artefacts that must verify across builds and
// across years; the layout will not change within a major version.
//
// # The scale is not encoded
//
// [Scale] is a constant of the type, not a property of a value. A
// decoder that could disagree with an encoder about the scale is the
// bug this package prevents, and a field that can disagree is one
// somebody will set. When the scale changes, the major version
// changes with it.
//
// # Byte order is not numeric order
//
// Two's complement puts -1 at 0xffff_ffff_ffff_ffff and +1 at
// 0x0000_0000_0000_0001, so the encoded form sorts negatives above
// positives. A caller needing an order-preserving key must flip the
// sign bit itself. The alternative — offset-binary, so that byte
// order is
// numeric order — was rejected because it makes the wire form
// something other than the obvious int64 encoding, which every
// cross-language port would then have to be told about. Ordering in
// memory is already available through the operators and
// [Fixed64.Compare].
//
// Returns [ErrRange] for the out-of-contract math.MinInt64, so every
// value on the wire is one a decoder will accept. Implements
// [encoding.BinaryAppender].
//
// # Allocation contract
//
// Zero alloc when dst has capacity for [Size] more bytes.
func (f Fixed64) AppendBinary(dst []byte) ([]byte, error) {
	if f == outOfDomain {
		return dst, ErrRange
	}

	//nolint:gosec // G115: a bit-pattern reinterpretation, not a
	// numeric conversion — UnmarshalBinary reverses it exactly.
	return binary.BigEndian.AppendUint64(dst, uint64(f)), nil
}

// MarshalBinary returns the canonical [Size]-byte encoding of f.
// Implements [encoding.BinaryMarshaler]; see [Fixed64.AppendBinary]
// for the layout and the contract.
func (f Fixed64) MarshalBinary() ([]byte, error) {
	return f.AppendBinary(make([]byte, 0, Size))
}

// UnmarshalBinary sets f from the canonical encoding.
//
// Returns [ErrSize] unless len(data) is exactly [Size], and
// [ErrRange] when the decoded value is the excluded math.MinInt64. A
// truncated read is a decode error, never a panic and never a
// partially-filled value: f is left unmodified when data is rejected.
//
// Implements [encoding.BinaryUnmarshaler].
func (f *Fixed64) UnmarshalBinary(data []byte) error {
	if len(data) != Size {
		return ErrSize
	}

	//nolint:gosec // G115: the inverse reinterpretation of the one in
	// AppendBinary; FromRaw rejects the single out-of-domain result.
	v, err := FromRaw(int64(binary.BigEndian.Uint64(data)))
	if err != nil {
		return err
	}

	*f = v

	return nil
}
