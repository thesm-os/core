// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package crypto

import (
	"bytes"
	"crypto/subtle"
	"encoding/hex"
)

// Digest size constants. Every [Hasher] in this module produces a
// digest of one of these sizes. The size of a stored digest is the
// length of its encoding, which [DigestFromBytes] reads. A caller that
// knows the producing [Algorithm] compares [Digest.Size] with the
// constant for that algorithm.
const (
	// DigestSize256 is the byte length of a 256-bit digest: SHA-256,
	// SHA3-256 and any other 256-bit hash.
	DigestSize256 = 32
	// DigestSize384 is the byte length of a 384-bit digest: SHA-384
	// and SHA3-384.
	DigestSize384 = 48
	// DigestSize512 is the byte length of a 512-bit digest: SHA-512
	// and SHA3-512.
	DigestSize512 = 64

	// MaxDigestSize is the upper bound on [Digest.Size] across
	// every algorithm a [Hasher] in this module may use. The
	// underlying byte array of [Digest] is sized to this constant
	// so a single [Digest] type covers the full set without
	// requiring per-algorithm specialisation.
	MaxDigestSize = DigestSize512
)

// Digest is a hash output of [DigestSize256], [DigestSize384] or
// [DigestSize512] bytes in one value type. [Digest.Size] returns the
// length of the active prefix, and [Digest.Bytes] returns the prefix.
//
// Digest is comparable, so it can be a map key or a field of a
// comparable struct, and == compares two digests. Its fields are
// unexported, so a stored Digest changes only when another Digest is
// assigned to it, as [Digest.UnmarshalBinary] does.
//
// # Allocation contract
//
// Construction, comparison and storage do not allocate.
// [Digest.String] allocates the returned string.
type Digest struct {
	bytes [MaxDigestSize]byte
	size  uint8
}

// NewDigest256 wraps a 32-byte hash output in a [Digest] of size
// [DigestSize256]. [Hasher] implementations whose primitive returns an
// array, such as [crypto/sha256.Sum256], use it.
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

// DigestFromBytes returns a [Digest] of b, with [Digest.Size] taken
// from len(b). A digest read from a wire, a database column or a proof
// body enters the type through it.
//
// Returns [ErrDigestSize] unless len(b) is [DigestSize256],
// [DigestSize384] or [DigestSize512]. Empty input is an error as well,
// so no input decodes to the zero [Digest], which has no wire form.
//
// The returned Digest is a copy of b and does not alias it.
//
// # Allocation contract
//
// Zero alloc.
func DigestFromBytes(b []byte) (Digest, error) {
	var size uint8
	switch len(b) {
	case DigestSize256:
		size = DigestSize256
	case DigestSize384:
		size = DigestSize384
	case DigestSize512:
		size = DigestSize512
	default:
		return Digest{}, ErrDigestSize
	}

	var d Digest
	copy(d.bytes[:], b)
	d.size = size

	return d, nil
}

// AppendBinary appends d's active bytes to dst with no length header.
// The length of the encoding is the size, so a container that stores
// the encoding also stores the width.
//
// Returns dst unchanged and [ErrDigestZero] for the zero [Digest],
// which has no wire form. Implements [encoding.BinaryAppender].
//
// # Allocation contract
//
// Zero alloc when dst has capacity for d.Size() more bytes.
func (d Digest) AppendBinary(dst []byte) ([]byte, error) {
	if d.IsZero() {
		return dst, ErrDigestZero
	}

	return append(dst, d.bytes[:d.size]...), nil
}

// MarshalBinary returns exactly d.Size() bytes, the encoding
// [Digest.AppendBinary] appends. [encoding/gob] refuses a struct with
// no exported fields, as a value and as a map key. It encodes a Digest
// through this method.
//
// Returns nil and [ErrDigestZero] for the zero [Digest].
//
// # Allocation contract
//
// One allocation for the returned slice.
//
// A call through the [encoding.BinaryMarshaler] interface costs a
// second allocation. Digest is a value type wider than a word, so
// converting it to an interface copies it to the heap.
// [encoding.BinaryUnmarshaler] does not add an allocation, because its
// receiver is a *Digest, which is pointer-shaped. Hot paths use
// [Digest.AppendBinary] and avoid both.
func (d Digest) MarshalBinary() ([]byte, error) {
	out, err := d.AppendBinary(make([]byte, 0, d.size))
	if err != nil {
		return nil, err
	}

	return out, nil
}

// UnmarshalBinary accepts exactly the inputs [DigestFromBytes] accepts
// and returns the same error, so the two decode paths agree on every
// input. On an error d is unchanged. Implements
// [encoding.BinaryUnmarshaler].
//
// # Allocation contract
//
// Zero alloc. It decodes into the receiver.
func (d *Digest) UnmarshalBinary(data []byte) error {
	parsed, err := DigestFromBytes(data)
	if err != nil {
		return err
	}
	*d = parsed

	return nil
}

// Size returns the length of d's active prefix: 32, 48 or 64, or 0 for
// the zero [Digest].
func (d Digest) Size() int {
	return int(d.size)
}

// Bytes returns the active prefix of d. Bytes has a value receiver, so
// the slice refers to a copy of d, and a write through the slice does
// not change d.
func (d Digest) Bytes() []byte {
	return d.bytes[:d.size]
}

// IsZero reports whether d is the zero [Digest], the uninitialised
// value, which is valid nowhere. [Hasher.CombineTagged] refuses it as an
// operand, with a panic message of its own that is distinct from the
// one for a wrong width.
//
// IsZero compares the size only. [NewDigest256], [NewDigest384],
// [NewDigest512] and [DigestFromBytes] are the only code that sets a
// Digest's size. Each sets it to 32, 48 or 64 together with the bytes,
// and [Digest.UnmarshalBinary] assigns the result of DigestFromBytes.
// No Digest other than the zero value has size 0.
//
// The zero Digest has no binary encoding. [Digest.AppendBinary] and
// [Digest.MarshalBinary] return [ErrDigestZero] for it, and every
// decode path rejects empty input, so a truncated read or an absent
// field cannot decode to a digest. Encoding absence is the containing
// format's job, as it is for any other optional field.
//
// # Allocation contract
//
// Zero alloc.
func (d Digest) IsZero() bool {
	return d.size == 0
}

// Equal reports whether d and other have the same size and the same
// active bytes. It returns the same result as d == other.
//
// The duration of Equal depends on the position of the first differing
// byte. An attacker who submits guesses can use that timing to recover
// a MAC one byte at a time. Compare a [MAC] or a signature digest from
// an untrusted party with [Digest.ConstantTimeEqual].
func (d Digest) Equal(other Digest) bool {
	return d == other
}

// ConstantTimeEqual reports whether d and other have the same size and
// the same active bytes, in time independent of where the bytes first
// differ. Use it to compare a [MAC] or a signature digest against a
// value from an untrusted party. [Digest.Equal] and == leak the
// position of the first differing byte through their duration.
//
// A size mismatch returns false at once. The producing algorithm
// determines the size, so the size is public and the early return does
// not leak a secret.
func (d Digest) ConstantTimeEqual(other Digest) bool {
	if d.size != other.size {
		return false
	}
	return subtle.ConstantTimeCompare(d.bytes[:d.size], other.bytes[:other.size]) == 1
}

// Compare returns -1, 0 or +1 by the lexicographic order of the active
// prefixes. When one prefix is a prefix of the other, [bytes.Compare]
// orders the shorter first. Use it to keep digests in a sorted index,
// such as a Merkle accumulator.
func (d Digest) Compare(other Digest) int {
	return bytes.Compare(d.bytes[:d.size], other.bytes[:other.size])
}

// String returns the active prefix in hexadecimal, for diagnostic
// output, and the empty string for the zero [Digest]. It allocates the
// returned string.
func (d Digest) String() string {
	// The hex digits go into a stack buffer sized for the largest
	// digest, so the conversion to string is the only allocation.
	var buf [DigestSize512 * 2]byte
	hex.Encode(buf[:d.size*2], d.bytes[:d.size])
	return string(buf[:d.size*2])
}
