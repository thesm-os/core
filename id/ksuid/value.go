// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package ksuid

import "go.thesmos.sh/core/id"

// KSUID is a KSUID as an array of its 20 bytes: a timestamp of 32 bits in
// seconds since [Epoch], big-endian, then 128 random bits. Every 20 bytes are
// a KSUID. KSUID is comparable, and == compares two KSUIDs. A struct that
// embeds KSUID gets its methods, so the struct encodes as the KSUID in kanon,
// JSON and gob. A map keyed by such a struct hashes and compares 20 bytes.
//
// The zero KSUID means no KSUID, and [KSUID.IsZero] reports it. Its binary
// form is no bytes, as for [id.Zero], and its text is 27 zeros. A [Generator]
// returns the zero KSUID only for a clock at [Epoch] and a source of zeros.
//
// # Concurrency
//
// A KSUID is a value, so concurrent reads are safe. A write to a shared KSUID
// needs the synchronisation of any other variable.
//
// # Allocation contract
//
// A value type of 20 bytes, passed by value. [KSUID.String] and
// [KSUID.MarshalText] allocate their result, and so does
// [KSUID.MarshalBinary] for a KSUID other than zero. The append methods
// allocate only to grow a dst without room for the text or the bytes, and
// the other methods do not allocate.
type KSUID [id.Size160]byte

// FromID returns the KSUID with the bytes of i and true for an ID of size
// [id.Size160]. It returns the zero KSUID and true for [id.Zero], and the zero
// KSUID and false for an ID of another size. The ID of 20 zero bytes converts
// to the zero KSUID, whose [KSUID.ID] is id.Zero.
//
// # Allocation contract
//
// Zero alloc.
func FromID(i id.ID) (KSUID, bool) {
	k, ok := i.As160()

	return KSUID(k), ok || i.IsZero()
}

// ID returns the ID of size [id.Size160] with the bytes of k, and [id.Zero]
// for the zero KSUID. [FromID] converts the ID back to k.
//
// # Allocation contract
//
// Zero alloc.
func (k KSUID) ID() id.ID {
	var i id.ID
	if !k.IsZero() {
		i = id.New160(k)
	}

	return i
}

// IsZero reports whether k is the zero KSUID, whose 20 bytes are all zero.
// kanon and the option omitzero of encoding/json leave out a field of the
// zero KSUID.
//
// # Allocation contract
//
// Zero alloc.
func (k KSUID) IsZero() bool {
	return k == KSUID{}
}

// Compare returns -1, 0 or +1 by the order of the bytes of k and other. The
// bytes of a KSUID start with its timestamp, so a KSUID of an earlier second
// orders first.
//
// # Allocation contract
//
// Zero alloc.
func (k KSUID) Compare(other KSUID) int {
	return id.ID160(k).Compare(id.ID160(other))
}

// String returns the text that [Format] returns for the bytes of k: 27
// characters of base62. The zero KSUID has the text of 27 zeros.
//
// # Allocation contract
//
// Allocates the 27-byte result string.
func (k KSUID) String() string {
	return Format(id.New160(k))
}

// AppendText appends the text that [KSUID.String] returns to dst and returns
// the extended slice. The error is always nil. encoding/json writes a KSUID
// as a JSON string of this text, and the key of a map as the name of an
// object member. Implements [encoding.TextAppender].
//
// # Allocation contract
//
// Zero alloc when dst has room for 27 more bytes.
func (k KSUID) AppendText(dst []byte) ([]byte, error) {
	return AppendFormat(dst, id.New160(k)), nil
}

// MarshalText returns the text that [KSUID.AppendText] appends, in a new
// slice. The error is always nil. Implements [encoding.TextMarshaler].
//
// # Allocation contract
//
// One allocation for the returned slice.
func (k KSUID) MarshalText() ([]byte, error) {
	return k.AppendText(make([]byte, 0, encodedLen))
}

// UnmarshalText sets k to the KSUID whose canonical text is text, by the
// rules of [Parse]. The text is case-sensitive, and 27 zeros decode to the
// zero KSUID. It reads text in place and does not convert it to a string. On
// an error k is unchanged. Implements [encoding.TextUnmarshaler].
//
// It returns [ErrInvalidLength], [ErrInvalidChar] and [ErrOverflow] as Parse
// does. It returns ErrInvalidLength for the empty text too.
//
// # Allocation contract
//
// Zero alloc.
func (k *KSUID) UnmarshalText(text []byte) error {
	i, err := decode(text)
	if err != nil {
		return err
	}

	copy(k[:], i.Bytes())

	return nil
}

// AppendBinary appends the binary form of k to dst and returns the extended
// slice. The binary form is the 20 bytes of k, and no bytes for the zero
// KSUID. These are the bytes that the binary form of [KSUID.ID] has, so a
// field that changes from [id.ID] to KSUID keeps its encoding. The error is
// always nil. Implements [encoding.BinaryAppender].
//
// # Allocation contract
//
// Zero alloc when dst has room for 20 more bytes.
func (k KSUID) AppendBinary(dst []byte) ([]byte, error) {
	return k.AppendKanon(dst), nil
}

// MarshalBinary returns the binary form that [KSUID.AppendBinary] appends, in
// a new slice. The error is always nil. encoding/gob encodes a KSUID through
// it. Implements [encoding.BinaryMarshaler].
//
// # Allocation contract
//
// One allocation for the returned slice, and none for the zero KSUID.
func (k KSUID) MarshalBinary() ([]byte, error) {
	return k.AppendKanon(make([]byte, 0, k.SizeKanon())), nil
}

// UnmarshalBinary sets k to the KSUID whose binary form is data: the zero
// KSUID for no bytes, and the KSUID of 20 bytes that are not all zero. k does
// not alias data. On an error k is unchanged. Implements
// [encoding.BinaryUnmarshaler].
//
// It returns [ErrInvalidBytes] for data of another length. It also returns
// ErrInvalidBytes for 20 zero bytes, because the binary form of the zero
// KSUID is no bytes.
//
// # Allocation contract
//
// Zero alloc.
func (k *KSUID) UnmarshalBinary(data []byte) error {
	if len(data) == 0 {
		*k = KSUID{}

		return nil
	}

	if len(data) != id.Size160 || KSUID(data) == (KSUID{}) {
		return ErrInvalidBytes
	}

	*k = KSUID(data)

	return nil
}

// SizeKanon returns the length of the binary form of k: 20, and 0 for the
// zero KSUID. kanon's generated code sizes a KSUID with it. Implements
// [go.thesmos.sh/kanon.Sizer].
//
// # Allocation contract
//
// Zero alloc.
func (k KSUID) SizeKanon() int {
	if k.IsZero() {
		return 0
	}

	return id.Size160
}

// ExactKanon declares KSUID a [go.thesmos.sh/kanon.Exact] type. kanon's
// generated code then writes a KSUID field without an error path, and a
// canonical decode does not encode the decoded KSUID a second time. No code
// calls the method.
//
// A KSUID keeps the two guarantees of the declaration:
//
//   - [KSUID.AppendBinary] returns no error and appends [KSUID.SizeKanon]
//     bytes for every KSUID.
//   - [KSUID.UnmarshalBinary] accepts only the bytes that AppendBinary writes
//     for the KSUID that it decodes: no bytes for the zero KSUID, and the 20
//     bytes of any other KSUID.
func (KSUID) ExactKanon() {}

// AppendKanon appends the binary form of k to dst, and returns the extended
// slice: the 20 bytes of k, and no bytes for the zero KSUID. It has no error
// result, so kanon's generated code writes a KSUID through it without an
// error path in every position. Implements [go.thesmos.sh/kanon.Appender].
//
// # Allocation contract
//
// Zero alloc when dst has room for 20 more bytes.
func (k KSUID) AppendKanon(dst []byte) []byte {
	if k.IsZero() {
		return dst
	}

	return append(dst, k[:]...)
}
