// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package ulid

import "go.thesmos.sh/core/id"

// ULID is a ULID as an array of its 16 bytes: a timestamp of 48 bits in
// milliseconds, big-endian, then 80 random bits. Every 16 bytes are a ULID.
// ULID is comparable, and == compares two ULIDs. A struct that embeds ULID
// gets its methods, so the struct encodes as the ULID in kanon, JSON and gob.
// A map keyed by such a struct hashes and compares 16 bytes.
//
// The zero ULID means no ULID, and [ULID.IsZero] reports it. Its binary form
// is no bytes, as for [id.Zero], and its text is 26 zeros. A [Generator]
// returns the zero ULID only for a clock at the Unix epoch and a source of
// zeros.
//
// # Concurrency
//
// A ULID is a value, so concurrent reads are safe. A write to a shared ULID
// needs the synchronisation of any other variable.
//
// # Allocation contract
//
// A value type of 16 bytes, passed by value. [ULID.String] and
// [ULID.MarshalText] allocate their result, and so does [ULID.MarshalBinary]
// for a ULID other than zero. The append methods allocate only to grow a dst
// without room for the text or the bytes, and the other methods do not
// allocate.
type ULID [id.Size128]byte

// FromID returns the ULID with the bytes of i and true for an ID of size
// [id.Size128]. It returns the zero ULID and true for [id.Zero], and the zero
// ULID and false for an ID of another size. The ID of 16 zero bytes converts
// to the zero ULID, whose [ULID.ID] is id.Zero.
//
// # Allocation contract
//
// Zero alloc.
func FromID(i id.ID) (ULID, bool) {
	k, ok := i.As128()

	return ULID(k), ok || i.IsZero()
}

// ID returns the ID of size [id.Size128] with the bytes of u, and [id.Zero]
// for the zero ULID. [FromID] converts the ID back to u.
//
// # Allocation contract
//
// Zero alloc.
func (u ULID) ID() id.ID {
	var i id.ID
	if !u.IsZero() {
		i = id.New128(u)
	}

	return i
}

// IsZero reports whether u is the zero ULID, whose 16 bytes are all zero.
// kanon and the option omitzero of encoding/json leave out a field of the
// zero ULID.
//
// # Allocation contract
//
// Zero alloc.
func (u ULID) IsZero() bool {
	return u == ULID{}
}

// Compare returns -1, 0 or +1 by the order of the bytes of u and other. The
// bytes of a ULID start with its timestamp, so a ULID of an earlier
// millisecond orders first.
//
// # Allocation contract
//
// Zero alloc.
func (u ULID) Compare(other ULID) int {
	return id.ID128(u).Compare(id.ID128(other))
}

// String returns the text that [Format] returns for the bytes of u: 26
// characters of Crockford's base32 in uppercase. The zero ULID has the text
// of 26 zeros.
//
// # Allocation contract
//
// Allocates the 26-byte result string.
func (u ULID) String() string {
	return Format(id.New128(u))
}

// AppendText appends the text that [ULID.String] returns to dst and returns
// the extended slice. The error is always nil. encoding/json writes a ULID
// as a JSON string of this text, and the key of a map as the name of an
// object member. Implements [encoding.TextAppender].
//
// # Allocation contract
//
// Zero alloc when dst has room for 26 more bytes.
func (u ULID) AppendText(dst []byte) ([]byte, error) {
	return AppendFormat(dst, id.New128(u)), nil
}

// MarshalText returns the text that [ULID.AppendText] appends, in a new
// slice. The error is always nil. Implements [encoding.TextMarshaler].
//
// # Allocation contract
//
// One allocation for the returned slice.
func (u ULID) MarshalText() ([]byte, error) {
	return u.AppendText(make([]byte, 0, encodedLen))
}

// UnmarshalText sets u to the ULID whose canonical text is text, by the rules
// of [ParseULID]. It accepts both cases of a letter and the substitutions of
// the ULID specification, and decodes 26 zeros to the zero ULID. It reads
// text in place and does not convert it to a string. On an error u is
// unchanged. Implements [encoding.TextUnmarshaler].
//
// It returns [ErrInvalidLength], [ErrInvalidTimestamp] and [ErrInvalidChar]
// as ParseULID does. It returns ErrInvalidLength for the empty text too.
//
// # Allocation contract
//
// Zero alloc.
func (u *ULID) UnmarshalText(text []byte) error {
	i, err := decode(text)
	if err != nil {
		return err
	}

	copy(u[:], i.Bytes())

	return nil
}

// AppendBinary appends the binary form of u to dst and returns the extended
// slice. The binary form is the 16 bytes of u, and no bytes for the zero
// ULID. These are the bytes that the binary form of [ULID.ID] has, so a field
// that changes from [id.ID] to ULID keeps its encoding. The error is always
// nil. Implements [encoding.BinaryAppender].
//
// # Allocation contract
//
// Zero alloc when dst has room for 16 more bytes.
func (u ULID) AppendBinary(dst []byte) ([]byte, error) {
	return u.AppendKanon(dst), nil
}

// MarshalBinary returns the binary form that [ULID.AppendBinary] appends, in
// a new slice. The error is always nil. encoding/gob encodes a ULID through
// it. Implements [encoding.BinaryMarshaler].
//
// # Allocation contract
//
// One allocation for the returned slice, and none for the zero ULID.
func (u ULID) MarshalBinary() ([]byte, error) {
	return u.AppendKanon(make([]byte, 0, u.SizeKanon())), nil
}

// UnmarshalBinary sets u to the ULID whose binary form is data: the zero ULID
// for no bytes, and the ULID of 16 bytes that are not all zero. u does not
// alias data. On an error u is unchanged. Implements
// [encoding.BinaryUnmarshaler].
//
// It returns [ErrInvalidBytes] for data of another length. It also returns
// ErrInvalidBytes for 16 zero bytes, because the binary form of the zero ULID
// is no bytes.
//
// # Allocation contract
//
// Zero alloc.
func (u *ULID) UnmarshalBinary(data []byte) error {
	if len(data) == 0 {
		*u = ULID{}

		return nil
	}

	if len(data) != id.Size128 || ULID(data) == (ULID{}) {
		return ErrInvalidBytes
	}

	*u = ULID(data)

	return nil
}

// SizeKanon returns the length of the binary form of u: 16, and 0 for the
// zero ULID. kanon's generated code sizes a ULID with it. Implements
// [go.thesmos.sh/kanon.Sizer].
//
// # Allocation contract
//
// Zero alloc.
func (u ULID) SizeKanon() int {
	if u.IsZero() {
		return 0
	}

	return id.Size128
}

// ExactKanon declares ULID a [go.thesmos.sh/kanon.Exact] type. kanon's
// generated code then writes a ULID field without an error path, and a
// canonical decode does not encode the decoded ULID a second time. No code
// calls the method.
//
// A ULID keeps the two guarantees of the declaration:
//
//   - [ULID.AppendBinary] returns no error and appends [ULID.SizeKanon] bytes
//     for every ULID.
//   - [ULID.UnmarshalBinary] accepts only the bytes that AppendBinary writes
//     for the ULID that it decodes: no bytes for the zero ULID, and the 16
//     bytes of any other ULID.
func (ULID) ExactKanon() {}

// AppendKanon appends the binary form of u to dst, and returns the extended
// slice: the 16 bytes of u, and no bytes for the zero ULID. It has no error
// result, so kanon's generated code writes a ULID through it without an
// error path in every position. Implements [go.thesmos.sh/kanon.Appender].
//
// # Allocation contract
//
// Zero alloc when dst has room for 16 more bytes.
func (u ULID) AppendKanon(dst []byte) []byte {
	if u.IsZero() {
		return dst
	}

	return append(dst, u[:]...)
}
