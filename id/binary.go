// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package id

// AppendBinary appends the bytes of i, the slice [ID.Bytes] returns, to
// dst with no length header, and returns the extended slice. The length
// of the encoding is the size of i, so a container that stores the
// encoding also stores the size, and the zero [ID] encodes as no bytes.
//
// The encoding is a stable wire contract, and the layout does not
// change. AppendBinary appends what [ID.AppendKanon] appends, and the
// error is always nil. Implements [encoding.BinaryAppender].
//
// # Allocation contract
//
// Zero alloc when dst has capacity for i.Size() more bytes.
func (i ID) AppendBinary(dst []byte) ([]byte, error) {
	return i.AppendKanon(dst), nil
}

// MarshalBinary returns exactly i.Size() bytes, the encoding that
// [ID.AppendBinary] appends. Codecs that use a type's binary methods,
// such as encoding/gob, encode an ID through it, because an ID has no
// exported fields to encode. Implements [encoding.BinaryMarshaler].
//
// # Allocation contract
//
// One allocation for the returned slice, and none for the zero [ID].
//
// A call through the [encoding.BinaryMarshaler] interface costs a second
// allocation, because an ID is wider than a word and converting it to an
// interface copies it to the heap. Hot paths call [ID.AppendBinary]
// directly.
func (i ID) MarshalBinary() ([]byte, error) {
	return i.AppendBinary(make([]byte, 0, i.size))
}

// SizeKanon returns the length of the binary form of i, the bytes that
// [ID.AppendBinary] appends: [ID.Size], and 0 for [Zero]. kanon's
// generated code sizes an ID with it and writes the binary form once,
// in place. Implements [go.thesmos.sh/kanon.Sizer].
//
// # Allocation contract
//
// Zero alloc.
func (i ID) SizeKanon() int {
	return int(i.size)
}

// ExactKanon declares ID a [go.thesmos.sh/kanon.Exact] type. kanon's
// generated code then writes an ID field without an error path, and a
// canonical decode does not encode the decoded ID a second time. No code
// calls the method.
//
// An ID keeps the two guarantees of the declaration:
//
//   - [ID.AppendBinary] returns no error and appends [ID.SizeKanon] bytes
//     for every ID.
//   - [ID.UnmarshalBinary] accepts only the bytes that AppendBinary writes
//     for the ID that it decodes: no bytes for [Zero], and the 16, 20 or
//     32 bytes of an ID of that size.
func (ID) ExactKanon() {}

// AppendKanon appends the encoding of i that [ID.AppendBinary] describes
// to dst, and returns the extended slice. It has no error result, because
// every ID has an encoding, so kanon's generated code writes an ID
// through it without an error path in every position, the elements of a
// slice included. Implements [go.thesmos.sh/kanon.Appender].
//
// # Allocation contract
//
// Zero alloc when dst has capacity for i.Size() more bytes.
func (i ID) AppendKanon(dst []byte) []byte {
	return append(dst, i.bytes[:i.size]...)
}

// UnmarshalBinary sets i to the ID that data encodes: [Zero] for empty
// data, and otherwise the ID that [FromBytes] builds from data. It
// copies data into i and clears the rest of i's array, so the decoded i
// equals, under ==, the ID that FromBytes builds, whatever i held before.
//
// Returns [ErrSize] unless len(data) is 0, [Size128], [Size160] or
// [Size256], and leaves i unchanged. It accepts the empty data that
// FromBytes rejects, because the zero ID encodes as no bytes and must
// decode to itself. i does not alias data. Implements
// [encoding.BinaryUnmarshaler].
//
// # Allocation contract
//
// Zero alloc. It decodes into the receiver.
func (i *ID) UnmarshalBinary(data []byte) error {
	size, ok := idSize(len(data))
	if !ok {
		return ErrSize
	}

	i.size = size
	n := copy(i.bytes[:], data)
	clear(i.bytes[n:])

	return nil
}
