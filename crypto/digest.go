// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package crypto

import (
	"bytes"
	"crypto/subtle"
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
//
// [Hasher.Combine] accepts the zero Digest as either operand and
// zero-pads it to the hasher's width, so the genesis case needs no
// branch at the call site. Every other size mismatch panics. The
// zero Digest has no binary encoding: it is an in-memory sentinel,
// and absence on the wire is the containing format's job. See
// ADR-0007.
func (d Digest) IsZero() bool {
	return d == Digest{}
}

// Equal reports whether d and other have the same size and the
// same active bytes. Equivalent to `d == other` but the explicit
// method is clearer at call sites that compare digests
// programmatically.
//
// Equal is NOT constant-time. Use [Digest.ConstantTimeEqual] to
// compare a [MAC] or signature digest against a value supplied
// by an untrusted party — equality timing leaks the position of
// the first differing byte and converts into a forgery oracle.
func (d Digest) Equal(other Digest) bool {
	return d == other
}

// ConstantTimeEqual reports whether d and other have the same
// size and the same active bytes, in time independent of where
// the bytes first differ. Use this for [MAC] and signature
// comparisons against values supplied by untrusted parties;
// [Digest.Equal] (and `==`) leak the first-differing-byte
// position via timing and must not be used in that setting.
//
// Size is public information determined by the producing
// algorithm, so the size short-circuit is not itself a timing
// hazard.
func (d Digest) ConstantTimeEqual(other Digest) bool {
	if d.size != other.size {
		return false
	}
	return subtle.ConstantTimeCompare(d.bytes[:d.size], other.bytes[:other.size]) == 1
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
	// Encode into a stack-resident buffer sized for the largest
	// digest, then convert to string. One alloc total — the
	// string copy. [hex.EncodeToString] would do two (a make
	// for the hex bytes plus the string conversion).
	var buf [DigestSize512 * 2]byte
	hex.Encode(buf[:d.size*2], d.bytes[:d.size])
	return string(buf[:d.size*2])
}
