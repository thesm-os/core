// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package id

import "bytes"

// ID128 is a 128-bit identifier as an array of its 16 bytes. ULIDs and UUIDs
// have this size. [ID.As128] converts an [ID] of size [Size128] to an ID128,
// and [ID128.ID] converts the ID128 back.
//
// The zero ID128 is the identifier whose 16 bytes are all zero. Its ID is not
// [Zero].
//
// # Allocation contract
//
// ID128 is a value type of 16 bytes. Construction, comparison and conversion
// do not allocate. [ID128.String] allocates its result.
type ID128 [Size128]byte

// ID returns the ID of size [Size128] with the bytes of k. k.ID().As128()
// returns k and true.
//
// # Allocation contract
//
// Zero alloc.
func (k ID128) ID() ID {
	return New128(k)
}

// Compare returns -1, 0 or +1 by the order of the bytes of k and other. It
// returns what [ID.Compare] returns for their IDs.
//
// # Allocation contract
//
// Zero alloc.
func (k ID128) Compare(other ID128) int {
	return bytes.Compare(k[:], other[:])
}

// String returns "id:" followed by the 16 bytes of k in hexadecimal. It
// returns what [ID.String] returns for the ID of k.
//
// # Allocation contract
//
// Allocates the result string.
func (k ID128) String() string {
	return text(k[:])
}

// ID160 is a 160-bit identifier as an array of its 20 bytes. KSUIDs have this
// size. [ID.As160] converts an [ID] of size [Size160] to an ID160, and
// [ID160.ID] converts the ID160 back.
//
// The zero ID160 is the identifier whose 20 bytes are all zero. Its ID is not
// [Zero].
//
// # Allocation contract
//
// ID160 is a value type of 20 bytes. Construction, comparison and conversion
// do not allocate. [ID160.String] allocates its result.
type ID160 [Size160]byte

// ID returns the ID of size [Size160] with the bytes of k. k.ID().As160()
// returns k and true.
//
// # Allocation contract
//
// Zero alloc.
func (k ID160) ID() ID {
	return New160(k)
}

// Compare returns -1, 0 or +1 by the order of the bytes of k and other. It
// returns what [ID.Compare] returns for their IDs.
//
// # Allocation contract
//
// Zero alloc.
func (k ID160) Compare(other ID160) int {
	return bytes.Compare(k[:], other[:])
}

// String returns "id:" followed by the 20 bytes of k in hexadecimal. It
// returns what [ID.String] returns for the ID of k.
//
// # Allocation contract
//
// Allocates the result string.
func (k ID160) String() string {
	return text(k[:])
}

// ID256 is a 256-bit identifier as an array of its 32 bytes. SHA-256 content
// addresses and Ed25519 public keys have this size. [ID.As256] converts an
// [ID] of size [Size256] to an ID256, and [ID256.ID] converts the ID256 back.
//
// The zero ID256 is the identifier whose 32 bytes are all zero. Its ID is not
// [Zero].
//
// # Allocation contract
//
// ID256 is a value type of 32 bytes. Construction, comparison and conversion
// do not allocate. [ID256.String] allocates its result.
type ID256 [Size256]byte

// ID returns the ID of size [Size256] with the bytes of k. k.ID().As256()
// returns k and true.
//
// # Allocation contract
//
// Zero alloc.
func (k ID256) ID() ID {
	return New256(k)
}

// Compare returns -1, 0 or +1 by the order of the bytes of k and other. It
// returns what [ID.Compare] returns for their IDs.
//
// # Allocation contract
//
// Zero alloc.
func (k ID256) Compare(other ID256) int {
	return bytes.Compare(k[:], other[:])
}

// String returns "id:" followed by the 32 bytes of k in hexadecimal. It
// returns what [ID.String] returns for the ID of k.
//
// # Allocation contract
//
// Allocates the result string.
func (k ID256) String() string {
	return text(k[:])
}

// As128 returns the fixed-size form of i and reports whether i has the size
// [Size128]. It returns the zero [ID128] and false for [Zero] and for an ID of
// another size.
//
// # Allocation contract
//
// Zero alloc.
func (i ID) As128() (ID128, bool) {
	if i.size != Size128 {
		return ID128{}, false
	}

	return ID128(i.bytes[:Size128]), true
}

// As160 returns the fixed-size form of i and reports whether i has the size
// [Size160]. It returns the zero [ID160] and false for [Zero] and for an ID of
// another size.
//
// # Allocation contract
//
// Zero alloc.
func (i ID) As160() (ID160, bool) {
	if i.size != Size160 {
		return ID160{}, false
	}

	return ID160(i.bytes[:Size160]), true
}

// As256 returns the fixed-size form of i and reports whether i has the size
// [Size256]. It returns the zero [ID256] and false for [Zero] and for an ID of
// another size.
//
// # Allocation contract
//
// Zero alloc.
func (i ID) As256() (ID256, bool) {
	if i.size != Size256 {
		return ID256{}, false
	}

	return ID256(i.bytes), true
}
