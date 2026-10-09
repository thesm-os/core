// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package id

import (
	"bytes"
	"encoding/hex"
)

// Identifier sizes. Every [ID] that a [Generator] of this module
// returns has one of these sizes, which [ID.Size] reports.
const (
	// Size128 is the byte length of a 128-bit identifier, such as a
	// ULID or a UUID.
	Size128 = 16

	// Size160 is the byte length of a 160-bit identifier, such as a
	// KSUID.
	Size160 = 20

	// Size256 is the byte length of a 256-bit identifier, such as a
	// content address derived from a 256-bit hash (SHA-256,
	// SHA3-256) or a public-key fingerprint.
	Size256 = 32

	// MaxSize is the largest [ID.Size] of any identifier. The byte
	// array of [ID] has MaxSize bytes, so one [ID] type stores 128-,
	// 160- and 256-bit identifiers without a value type per size.
	MaxSize = Size256
)

// ID is an identifier of 128, 160 or 256 bits in one value type.
// [ID.Size] returns the length of the active prefix, and [ID.Bytes]
// returns the prefix.
//
// ID is comparable, so it can be a map key or a field of a
// comparable struct, and == compares two IDs. An ID is a value: a
// copy does not share storage with the original, so an identifier
// stored in an audit record does not change when the caller's
// variable does.
//
// # Allocation contract
//
// Construction, comparison and storage do not allocate. [ID.Bytes]
// allocates the copy that its slice refers to when the slice escapes, and
// [ID.String] allocates the returned string.
type ID struct {
	bytes [MaxSize]byte
	size  uint8
}

// Zero is the zero ID, which consumers read as "no identifier". A
// [Generator] must not return Zero, except an [id/constant]
// generator that the caller seeds with it.
var Zero = ID{}

// New128 returns an [ID] of size [Size128] with the bytes of b. A
// [Generator] whose primitive returns a 16-byte array, such as ULID
// or UUIDv4, builds its IDs with it.
//
// # Allocation contract
//
// Zero alloc.
func New128(b [Size128]byte) ID {
	var i ID
	copy(i.bytes[:], b[:])
	i.size = Size128
	return i
}

// New160 returns an [ID] of size [Size160] with the bytes of b.
//
// # Allocation contract
//
// Zero alloc.
func New160(b [Size160]byte) ID {
	var i ID
	copy(i.bytes[:], b[:])
	i.size = Size160
	return i
}

// New256 returns an [ID] of size [Size256] with the bytes of b.
//
// # Allocation contract
//
// Zero alloc.
func New256(b [Size256]byte) ID {
	var i ID
	copy(i.bytes[:], b[:])
	i.size = Size256
	return i
}

// FromBytes returns an [ID] of b, with the size taken from len(b).
// An identifier read from a wire, a database column or a proof body
// enters the type through it.
//
// Returns [Zero] and [ErrSize] unless len(b) is [Size128],
// [Size160] or [Size256]. The returned ID is a copy of b and does
// not alias it.
//
// # Allocation contract
//
// Zero alloc.
func FromBytes(b []byte) (ID, error) {
	// idSize returns 0 for the empty form of Zero and for every length
	// that no ID has, and FromBytes refuses both.
	size, _ := idSize(len(b))
	if size == 0 {
		return Zero, ErrSize
	}

	var i ID
	copy(i.bytes[:], b)
	i.size = size

	return i, nil
}

// Size returns the length of the active prefix of i: 16, 20 or 32,
// or 0 for [Zero].
func (i ID) Size() int {
	return int(i.size)
}

// Bytes returns the active prefix of i. Bytes has a value receiver,
// so the slice refers to a copy of i, and a write through the slice
// does not change i.
//
// # Allocation contract
//
// Bytes allocates the copy of i when the slice escapes the caller. The
// compiler inlines Bytes, so a caller whose slice does not escape keeps
// the copy on its stack. [ID.AppendBinary] copies the bytes into a buffer
// of the caller without an allocation.
func (i ID) Bytes() []byte {
	return i.bytes[:i.size]
}

// IsZero reports whether i is the zero [ID], which consumers read as
// "no identifier".
//
// IsZero compares the size only. [New128], [New160], [New256],
// [FromBytes] and [ID.UnmarshalBinary] are the only code that sets an
// ID's size. Each sets it to 16, 20 or 32 together with the bytes, apart
// from UnmarshalBinary of empty data, which sets size 0 and clears every
// byte. No ID other than the zero value has size 0.
//
// # Allocation contract
//
// Zero alloc.
func (i ID) IsZero() bool {
	return i.size == 0
}

// Equal reports whether i and other have the same size and the
// same active bytes. It returns the same result as i == other, for
// call sites that compare identifiers through a method.
func (i ID) Equal(other ID) bool {
	return i == other
}

// Compare returns -1, 0 or +1 by the lexicographic order of the
// active prefixes. When one prefix is a prefix of the other,
// [bytes.Compare] orders the shorter first. ULIDs and UUIDv7s sort by
// their creation time to the millisecond, and KSUIDs to the second.
// UUIDv4s sort in no useful order, because their bytes are random.
func (i ID) Compare(other ID) int {
	return bytes.Compare(i.bytes[:i.size], other.bytes[:other.size])
}

// String returns "id:" followed by the active prefix in
// hexadecimal, for diagnostic output, and "id:" for [Zero].
//
// No canonical encoding of an identifier starts with "id:", so an
// ID that fmt writes into a log line cannot pass for one. ULIDs use
// Crockford base32, UUIDs hyphenated hexadecimal and KSUIDs base62.
// The Format function of each generator's package returns the
// canonical encoding:
//
//   - [go.thesmos.sh/core/id/ulid.Format] for ULIDs
//   - [go.thesmos.sh/core/id/uuidv4.Format] for UUIDv4s
//   - [go.thesmos.sh/core/id/uuidv7.Format] for UUIDv7s
//   - [go.thesmos.sh/core/id/ksuid.Format] for KSUIDs
//
// # Allocation contract
//
// Allocates the result string.
func (i ID) String() string {
	return "id:" + hex.EncodeToString(i.bytes[:i.size])
}

// idSize returns the size of the ID whose binary form is n bytes long: 0
// for the empty form of [Zero], and 16, 20 or 32 for any other ID. It
// returns 0 and false for a length that no ID has. [FromBytes] and
// [ID.UnmarshalBinary] share it, so the two decode paths accept the same
// lengths, apart from the empty form, which FromBytes rejects.
func idSize(n int) (uint8, bool) {
	switch n {
	case 0:
		return 0, true
	case Size128:
		return Size128, true
	case Size160:
		return Size160, true
	case Size256:
		return Size256, true
	}

	return 0, false
}
