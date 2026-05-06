// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package crypto

import (
	"bytes"
	"encoding/hex"
)

// Digest size constants. Every [Hasher] in this module produces a
// digest of one of these sizes; consumers reading a stored
// [Digest] determine the active size from the producing
// [Hasher]'s [ID] or [Algorithm].
const (
	// DigestSize256 is the byte length of a 256-bit digest —
	// SHA-256, SHA3-256, and any other 256-bit hash carried
	// through this seam.
	DigestSize256 = 32
	// DigestSize384 is the byte length of a 384-bit digest —
	// SHA-384 and SHA3-384.
	DigestSize384 = 48
	// DigestSize512 is the byte length of a 512-bit digest —
	// SHA-512 and SHA3-512.
	DigestSize512 = 64

	// MaxDigestSize is the upper bound on [Digest.Size] across
	// every algorithm a [Hasher] in this module may use. The
	// underlying byte array of [Digest] is sized to this constant
	// so a single [Digest] type covers the full set without
	// requiring per-algorithm specialisation.
	MaxDigestSize = DigestSize512
)

// Digest is a fixed-max-size hash output covering 256-, 384-, and
// 512-bit digests in a single value type. The [Digest.Size]
// method reports the active prefix length; [Digest.Bytes] returns
// a slice over that prefix.
//
// Digest is comparable (`==` works) so it can be a map key, a
// struct field participating in equality, or compared in tests
// without `bytes.Equal`. Pass-by-value; value-typed mutations do
// not alias previously-stored digests — important for
// audit-chain integrity where a stored digest must stay frozen.
//
// # Allocation contract
//
// Construction, comparison, and storage are zero-alloc.
// [Digest.String] allocates the returned hex string.
type Digest struct {
	bytes [MaxDigestSize]byte
	size  uint8
}

// NewDigest256 wraps a 32-byte hash output in a [Digest] of size
// [DigestSize256]. Used by [Hasher] implementations whose
// underlying primitive returns a fixed-size array (for example
// [crypto/sha256.Sum256]).
//
// # Allocation contract
//
// Zero alloc.
func NewDigest256(b [DigestSize256]byte) Digest {
	var d Digest
	copy(d.bytes[:], b[:])
	d.size = DigestSize256
	return d
}

// NewDigest384 wraps a 48-byte hash output in a [Digest] of size
// [DigestSize384].
//
// # Allocation contract
//
// Zero alloc.
func NewDigest384(b [DigestSize384]byte) Digest {
	var d Digest
	copy(d.bytes[:], b[:])
	d.size = DigestSize384
	return d
}

// NewDigest512 wraps a 64-byte hash output in a [Digest] of size
// [DigestSize512].
//
// # Allocation contract
//
// Zero alloc.
func NewDigest512(b [DigestSize512]byte) Digest {
	var d Digest
	copy(d.bytes[:], b[:])
	d.size = DigestSize512
	return d
}

// Size returns the number of meaningful bytes in d.
func (d Digest) Size() int {
	return int(d.size)
}

// Bytes returns a read-only slice covering the meaningful prefix
// of d. The returned slice aliases d's storage; callers must
// treat it as immutable. The slice is invalidated by any
// modification to d, but Digest is value-typed and therefore
// effectively immutable after construction.
func (d Digest) Bytes() []byte {
	return d.bytes[:d.size]
}

// IsZero reports whether d is the zero [Digest] (size 0, all
// bytes zero). The zero Digest is the conventional sentinel for
// "no digest computed" — for example, the predecessor anchor of
// the genesis entry in a hash chain.
func (d Digest) IsZero() bool {
	return d == Digest{}
}

// Equal reports whether d and other have the same size and the
// same active bytes. Equivalent to `d == other` but the explicit
// method is clearer at call sites that compare digests
// programmatically.
func (d Digest) Equal(other Digest) bool {
	return d == other
}

// Compare returns -1, 0, or +1 by lexicographic ordering of the
// active byte prefix. Size is part of the ordering implicitly:
// when two digests share a common prefix, [bytes.Compare] orders
// the shorter as less than the longer. Useful when digests are
// stored in sorted indexes (Merkle accumulators, sorted-set
// caches).
func (d Digest) Compare(other Digest) int {
	return bytes.Compare(d.bytes[:d.size], other.bytes[:other.size])
}

// String returns the hex-encoded active prefix. Allocates the
// result string; intended for diagnostic output, not the hot
// path.
func (d Digest) String() string {
	return hex.EncodeToString(d.bytes[:d.size])
}
