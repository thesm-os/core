// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package id

// AppendBinary appends the bytes of i, the slice [ID.Bytes] returns, to
// dst with no length header, and returns the extended slice. The length
// of the encoding is the size of i, so a container that stores the
// encoding also stores the size, and the zero [ID] encodes as no bytes.
//
// The encoding is a stable wire contract, and the layout does not
// change. Implements [encoding.BinaryAppender].
//
// # Allocation contract
//
// Zero alloc when dst has capacity for i.Size() more bytes.
func (i ID) AppendBinary(dst []byte) ([]byte, error) {
	return append(dst, i.bytes[:i.size]...), nil
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

// UnmarshalBinary sets i to the ID that data encodes: [Zero] for empty
// data, and otherwise the ID that [FromBytes] builds from data.
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
	if len(data) == 0 {
		*i = Zero

		return nil
	}

	parsed, err := FromBytes(data)
	if err != nil {
		return err
	}
	*i = parsed

	return nil
}
