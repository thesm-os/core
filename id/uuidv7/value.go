// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package uuidv7

import (
	"go.thesmos.sh/core/id"
	"go.thesmos.sh/core/id/internal/uuidtext"
)

// UUID is a UUID as an array of its 16 bytes. UUID is comparable, and ==
// compares two UUIDs. A struct that embeds UUID gets its methods, so the
// struct encodes as the UUID in kanon, JSON and gob. A map keyed by such a
// struct hashes and compares 16 bytes.
//
// The zero UUID means no UUID, and [UUID.IsZero] reports it. Its binary form
// is no bytes, as for [id.Zero]. Its text is the text of the Nil UUID of
// RFC 9562, "00000000-0000-0000-0000-000000000000". A [Generator] never
// returns the Nil UUID, because it sets the version bits.
//
// The codecs of UUID do not check the version or the variant of its bytes, as
// [Parse] does not, so every UUID encodes and decodes. [UUID.Valid] checks
// them.
//
// # Concurrency
//
// A UUID is a value, so concurrent reads are safe. A write to a shared UUID
// needs the synchronisation of any other variable.
//
// # Allocation contract
//
// A value type of 16 bytes, passed by value. [UUID.String] and
// [UUID.MarshalText] allocate their result, and so does [UUID.MarshalBinary]
// for a UUID other than zero. The append methods allocate only to grow a dst
// without room for the text or the bytes, and the other methods do not
// allocate.
type UUID [id.Size128]byte

// FromID returns the UUID with the bytes of i and true for an ID of size
// [id.Size128]. It returns the zero UUID and true for [id.Zero], and the zero
// UUID and false for an ID of another size. The ID of 16 zero bytes converts
// to the zero UUID, whose [UUID.ID] is id.Zero.
//
// # Allocation contract
//
// Zero alloc.
func FromID(i id.ID) (UUID, bool) {
	k, ok := i.As128()

	return UUID(k), ok || i.IsZero()
}

// ID returns the ID of size [id.Size128] with the bytes of u, and [id.Zero]
// for the zero UUID. [FromID] converts the ID back to u.
//
// # Allocation contract
//
// Zero alloc.
func (u UUID) ID() id.ID {
	var i id.ID
	if !u.IsZero() {
		i = id.New128(u)
	}

	return i
}

// IsZero reports whether u is the zero UUID, whose 16 bytes are all zero.
// kanon and the option omitzero of encoding/json leave out a field of the
// zero UUID.
//
// # Allocation contract
//
// Zero alloc.
func (u UUID) IsZero() bool {
	return u == UUID{}
}

// Valid reports whether u is a UUIDv7: version 7 in the high 4 bits of byte 6
// and the variant of RFC 9562 in the high 2 bits of byte 8. It returns what
// [Valid] returns for the ID of u. It reports false for the zero UUID,
// because the Nil UUID has neither.
//
// # Allocation contract
//
// Zero alloc.
func (u UUID) Valid() bool {
	return Valid(u.ID())
}

// Compare returns -1, 0 or +1 by the order of the bytes of u and other. The
// bytes of a UUIDv7 start with its Unix milliseconds, so a UUIDv7 of an
// earlier millisecond orders first.
//
// # Allocation contract
//
// Zero alloc.
func (u UUID) Compare(other UUID) int {
	return id.ID128(u).Compare(id.ID128(other))
}

// String returns the text that [Format] returns for the bytes of u, in
// lowercase hexadecimal. The zero UUID has the text of the Nil UUID.
//
// # Allocation contract
//
// Allocates the 36-byte result string.
func (u UUID) String() string {
	return Format(id.New128(u))
}

// AppendText appends the text that [UUID.String] returns to dst and returns
// the extended slice. The error is always nil. encoding/json writes a UUID
// as a JSON string of this text, and the key of a map as the name of an
// object member. Implements [encoding.TextAppender].
//
// # Allocation contract
//
// Zero alloc when dst has room for 36 more bytes.
func (u UUID) AppendText(dst []byte) ([]byte, error) {
	return AppendFormat(dst, id.New128(u)), nil
}

// MarshalText returns the text that [UUID.AppendText] appends, in a new
// slice. The error is always nil. Implements [encoding.TextMarshaler].
//
// # Allocation contract
//
// One allocation for the returned slice.
func (u UUID) MarshalText() ([]byte, error) {
	return u.AppendText(make([]byte, 0, uuidtext.Len))
}

// UnmarshalText sets u to the UUID whose text form is text, by the rules of
// [Parse]. It accepts uppercase and lowercase hexadecimal digits, and decodes
// the text of the Nil UUID to the zero UUID. It reads text in place and does
// not convert it to a string. On an error u is unchanged. Implements
// [encoding.TextUnmarshaler].
//
// It returns [ErrInvalidLength], [ErrInvalidFormat] and [ErrInvalidChar] as
// Parse does. It returns ErrInvalidLength for the empty text too.
//
// # Allocation contract
//
// Zero alloc.
func (u *UUID) UnmarshalText(text []byte) error {
	i, err := uuidtext.Parse(text, decodeErrors)
	if err != nil {
		return err
	}

	copy(u[:], i.Bytes())

	return nil
}

// AppendBinary appends the binary form of u to dst and returns the extended
// slice. The binary form is the 16 bytes of u, and no bytes for the zero
// UUID. These are the bytes that the binary form of [UUID.ID] has, so a field
// that changes from [id.ID] to UUID keeps its encoding. The error is always
// nil. Implements [encoding.BinaryAppender].
//
// # Allocation contract
//
// Zero alloc when dst has room for 16 more bytes.
func (u UUID) AppendBinary(dst []byte) ([]byte, error) {
	return uuidtext.AppendBinary(dst, u), nil
}

// MarshalBinary returns the binary form that [UUID.AppendBinary] appends, in
// a new slice. The error is always nil. encoding/gob encodes a UUID through
// it. Implements [encoding.BinaryMarshaler].
//
// # Allocation contract
//
// One allocation for the returned slice, and none for the zero UUID.
func (u UUID) MarshalBinary() ([]byte, error) {
	return u.AppendBinary(make([]byte, 0, u.SizeKanon()))
}

// UnmarshalBinary sets u to the UUID whose binary form is data: the zero UUID
// for no bytes, and the UUID of 16 bytes that are not all zero. u does not
// alias data. On an error u is unchanged. Implements
// [encoding.BinaryUnmarshaler].
//
// It returns [ErrInvalidBytes] for data of another length. It also returns
// ErrInvalidBytes for 16 zero bytes, because the binary form of the zero UUID
// is no bytes.
//
// # Allocation contract
//
// Zero alloc.
func (u *UUID) UnmarshalBinary(data []byte) error {
	b, err := uuidtext.DecodeBinary(data, decodeErrors)
	if err != nil {
		return err
	}

	*u = b

	return nil
}

// SizeKanon returns the length of the binary form of u: 16, and 0 for the
// zero UUID. kanon's generated code sizes a UUID with it. Implements
// [go.thesmos.sh/kanon.Sizer].
//
// # Allocation contract
//
// Zero alloc.
func (u UUID) SizeKanon() int {
	if u.IsZero() {
		return 0
	}

	return id.Size128
}

// ExactKanon declares UUID a [go.thesmos.sh/kanon.Exact] type. kanon's
// generated code then writes a UUID field without an error path, and a
// canonical decode does not encode the decoded UUID a second time. No code
// calls the method.
//
// A UUID keeps the two guarantees of the declaration:
//
//   - [UUID.AppendBinary] returns no error and appends [UUID.SizeKanon] bytes
//     for every UUID.
//   - [UUID.UnmarshalBinary] accepts only the bytes that AppendBinary writes
//     for the UUID that it decodes: no bytes for the zero UUID, and the 16
//     bytes of any other UUID.
func (UUID) ExactKanon() {}

// AppendKanon appends the binary form that [UUID.AppendBinary] appends to dst,
// and returns the extended slice. It has no error result, so kanon's
// generated code writes a UUID through it without an error path in every
// position. Implements [go.thesmos.sh/kanon.Appender].
//
// # Allocation contract
//
// Zero alloc when dst has room for 16 more bytes.
func (u UUID) AppendKanon(dst []byte) []byte {
	return uuidtext.AppendBinary(dst, u)
}
