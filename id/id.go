// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package id

import (
	"bytes"
	"encoding/hex"
)

// Identifier sizes. Every [ID] returned by a [Generator] in this
// module reports one of these via [ID.Size].
const (
	// Size128 is the byte length of a 128-bit identifier — ULID,
	// UUIDv4, and any other 128-bit identifier carried through
	// this seam.
	Size128 = 16

	// Size160 is the byte length of a 160-bit identifier —
	// KSUID, and any other 160-bit identifier.
	Size160 = 20

	// Size256 is the byte length of a 256-bit identifier —
	// content-addressed identifiers derived from a 256-bit hash
	// (SHA-256, SHA-3-256), public-key fingerprints, and similar
	// hash-based identifier shapes.
	Size256 = 32

	// MaxSize is the upper bound on [ID.Size] across every
	// identifier shape this seam carries. The underlying byte
	// array of [ID] is sized to this constant so a single [ID]
	// type holds 128-, 160-, or 256-bit identifiers without
	// per-shape value types.
	MaxSize = Size256
)

// ID is a fixed-max-size identifier covering 128-, 160-, and
// 256-bit shapes in a single value type. The [ID.Size] method
// reports the active prefix length; [ID.Bytes] returns a slice
// over that prefix.
//
// ID is comparable (`==` works) so it can be a map key, a struct
// field participating in equality, or compared in tests without
// [bytes.Equal]. Pass-by-value; value-typed mutations do not
// alias previously-stored identifiers — important for audit
// records where a stored identifier must stay frozen.
//
// # Allocation contract
//
// Construction, comparison, and storage are zero-alloc. [String]
// allocates the returned hex string.
type ID struct {
	bytes [MaxSize]byte
	size  uint8
}

// Zero is the reserved zero value. Generators MUST NOT produce
// [Zero] except via [id/constant] explicitly seeded with it;
// consumers treat [Zero] as "no identifier."
var Zero = ID{}

// New128 wraps a 16-byte identifier in an [ID] of size [Size128].
// Used by [Generator] implementations whose underlying primitive
// returns a fixed-size 16-byte array (ULID, UUIDv4).
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

// New160 wraps a 20-byte identifier in an [ID] of size [Size160].
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

// New256 wraps a 32-byte identifier in an [ID] of size [Size256].
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

// FromBytes builds an [ID] from a byte slice, inferring the size
// from len(b). This is the construction path for callers whose
// identifier arrives from a wire, a database column, or a proof
// body rather than from a [Generator].
//
// Returns [ErrSize] unless len(b) is exactly [Size128], [Size160],
// or [Size256]. b is copied; the returned ID does not alias it.
//
// # Allocation contract
//
// Zero alloc.
func FromBytes(b []byte) (ID, error) {
	var size uint8
	switch len(b) {
	case Size128:
		size = Size128
	case Size160:
		size = Size160
	case Size256:
		size = Size256
	default:
		return Zero, ErrSize
	}

	var i ID
	copy(i.bytes[:], b)
	i.size = size

	return i, nil
}

// Size returns the number of meaningful bytes in i. Returns 0 for
// [Zero].
func (i ID) Size() int {
	return int(i.size)
}

// Bytes returns a read-only slice covering the meaningful prefix
// of i. The returned slice aliases i's storage; callers must
// treat it as immutable. The slice is invalidated by any
// modification to i, but [ID] is value-typed and therefore
// effectively immutable after construction.
func (i ID) Bytes() []byte {
	return i.bytes[:i.size]
}

// IsZero reports whether i is the zero [ID] (size 0, all bytes
// zero). The zero ID is the conventional sentinel for "no
// identifier."
func (i ID) IsZero() bool {
	return i == Zero
}

// Equal reports whether i and other have the same size and the
// same active bytes. Equivalent to `i == other`; the explicit
// method aids call sites that compare identifiers
// programmatically.
func (i ID) Equal(other ID) bool {
	return i == other
}

// Compare returns -1, 0, or +1 by lexicographic ordering of the
// active byte prefix. Bytewise compare; for ULID-shaped
// identifiers this matches chronological order, for UUIDv4 the
// order is meaningless (random bytes), and for KSUID it matches
// chronological order at second granularity.
func (i ID) Compare(other ID) int {
	return bytes.Compare(i.bytes[:i.size], other.bytes[:other.size])
}

// String returns the diagnostic encoding "id:<hex>" of the
// active prefix. The "id:" prefix is deliberate: it visually
// distinguishes the diagnostic encoding from every canonical
// algorithm encoding (ULID Crockford base32, UUIDv4 hyphenated
// hex, KSUID base62), so that a stray `fmt.Sprintf("%v", anID)`
// in logs is recognisable as the diagnostic form rather than
// silently passing for a canonical one.
//
// Consumers needing the producing generator's canonical
// encoding call the matching Format helper in that subpackage:
//
//   - [id/ulid.Format] for ULIDs
//   - [id/uuidv4.Format] for UUIDv4s
//   - [id/ksuid.Format] for KSUIDs
//
// Returns "id:" (with empty hex) for [id.Zero].
//
// # Allocation contract
//
// Allocates the result string.
func (i ID) String() string {
	return "id:" + hex.EncodeToString(i.bytes[:i.size])
}
