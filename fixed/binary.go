// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package fixed

//go:generate go tool kanon -type=Fixed64 -validate=valid

import "encoding/binary"

// AppendBinary appends the binary form of f to dst: the [Size] bytes
// of its raw value, big-endian, in two's complement.
//
// The binary form never changes, so a Fixed64 that a consumer signs or
// persists reads back to the same value in every build. The form
// contains no scale, because [Scale] is a constant of the type. When
// the scale changes, the major version of the module changes with it.
//
// Two's complement puts -1 at 0xffff_ffff_ffff_ffff and +1 at
// 0x0000_0000_0000_0001, so the binary form of a negative value sorts
// above that of a positive one. A caller that needs a key in numeric
// order flips the sign bit. The operators and [Fixed64.Compare] order
// values in memory.
//
// A kanon record does not use the binary form. It encodes a Fixed64
// as a zigzag varint of its raw value, through [Fixed64.ValidateKanon],
// so 12.34 takes 6 bytes of the record.
//
// Returns [ErrRange] for math.MinInt64, which the domain excludes, so
// a decoder accepts every binary form that AppendBinary writes.
// Implements [encoding.BinaryAppender].
//
// # Allocation contract
//
// Zero alloc when dst has capacity for [Size] more bytes.
func (f Fixed64) AppendBinary(dst []byte) ([]byte, error) {
	if err := f.valid(); err != nil {
		return dst, err
	}

	//nolint:gosec // G115: the conversion keeps the bit pattern, and
	// UnmarshalBinary reverses it.
	return binary.BigEndian.AppendUint64(dst, uint64(f)), nil
}

// MarshalBinary returns the binary form of f, which
// [Fixed64.AppendBinary] describes. Implements
// [encoding.BinaryMarshaler].
func (f Fixed64) MarshalBinary() ([]byte, error) {
	return f.AppendBinary(make([]byte, 0, Size))
}

// UnmarshalBinary sets f to the value whose binary form is data.
//
// Returns [ErrSize] unless len(data) is [Size], and [ErrRange] for the
// binary form of math.MinInt64, which the domain excludes. On an error
// f is unchanged, so a truncated read never leaves a partial value.
//
// Implements [encoding.BinaryUnmarshaler].
func (f *Fixed64) UnmarshalBinary(data []byte) error {
	if len(data) != Size {
		return ErrSize
	}

	//nolint:gosec // G115: the conversion reverses the one in
	// AppendBinary, and FromRaw rejects math.MinInt64.
	v, err := FromRaw(int64(binary.BigEndian.Uint64(data)))
	if err != nil {
		return err
	}

	*f = v

	return nil
}
