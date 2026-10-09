// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package epoch

//go:generate go tool kanon -type=Epoch

import "encoding/binary"

// EpochSize is the length in bytes of the binary form of an [Epoch].
const EpochSize = 8

// AppendBinary appends the binary form of e to dst: the [EpochSize]
// bytes of its value, big-endian.
//
// The binary form never changes, so a persisted watermark, or a
// fence in a message, reads back to the same epoch in every build.
// The zero [Epoch] has a binary form, the number zero, which a new
// scope persists as the seed of its watermark.
//
// A kanon record does not use the binary form. It encodes an Epoch
// as a varint of its value, through [Epoch.ValidateKanon], so an
// epoch of 7 takes 2 bytes of the record.
//
// Implements [encoding.BinaryAppender].
//
// # Allocation contract
//
// Zero alloc when dst has capacity for [EpochSize] more bytes.
func (e Epoch) AppendBinary(dst []byte) ([]byte, error) {
	return binary.BigEndian.AppendUint64(dst, uint64(e)), nil
}

// MarshalBinary returns the binary form of e, which
// [Epoch.AppendBinary] describes. Implements
// [encoding.BinaryMarshaler].
func (e Epoch) MarshalBinary() ([]byte, error) {
	return e.AppendBinary(make([]byte, 0, EpochSize))
}

// UnmarshalBinary sets e to the epoch whose binary form is data.
//
// Returns [ErrSize] unless len(data) is [EpochSize], and leaves e
// unchanged. A truncated read is a decode error, never a panic and
// never a partial value.
//
// Implements [encoding.BinaryUnmarshaler].
func (e *Epoch) UnmarshalBinary(data []byte) error {
	if len(data) != EpochSize {
		return ErrSize
	}

	*e = Epoch(binary.BigEndian.Uint64(data))

	return nil
}
