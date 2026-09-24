// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package crypto

import (
	"encoding/hex"
	"io"
)

// IDSize is the byte size of every [ID].
const IDSize = 16

// ID is a build-local identifier for a [Hasher] implementation.
// Persisted alongside [Digest] values so the same implementation
// that produced a digest can be re-selected when verifying. ID
// is short and fixed-size for fast equality and indexing; for
// cross-build / cross-language identification, use
// [Hasher.Algorithm] instead.
//
// Implementations encode their ID as a short printable ASCII tag
// with a version suffix (for example "sha256/v1") padded with
// zero bytes to [IDSize]. The only requirement beyond that
// convention is uniqueness across the implementations a
// deployment may select between.
//
// # Allocation contract
//
// Value type; pass by value. Zero alloc.
type ID [IDSize]byte

// String returns the hex-encoded ID. It allocates the result string
// and is meant for diagnostic output, not the hot path. Hex encoding
// keeps the string well-defined whatever the byte content.
func (id ID) String() string {
	return hex.EncodeToString(id[:])
}

// Hasher computes [Digest] values over byte streams and over
// pairs of digests, and constructs [Stream] state for
// arbitrary-length inputs.
//
// # Method semantics
//
//   - [Hasher.ID] returns the implementation's stable build-local
//     identifier.
//   - [Hasher.Algorithm] returns the long-term cross-build
//     algorithm name (for example "sha-256"). Persist it in
//     artefacts that may outlive the producing build.
//   - [Hasher.Hash] returns the [Digest] of the input bytes. Hot
//     path for content addressing, where the address must be the
//     digest OF the bytes and nothing else. Leaves of a tree or
//     chain use [Hasher.HashTagged] instead.
//   - [Hasher.HashTagged] returns the [Digest] of the input bytes
//     under a unary [Role]. Hot path for the leaves of a tree or
//     chain, where the payload is caller-supplied and could
//     otherwise be chosen to collide with an interior node.
//   - [Hasher.CombineTagged] returns the [Digest] of left || right
//     under a binary [Role]. Hot path for chain extension and
//     Merkle accumulator construction. Both operands must have
//     [Digest.Size] equal to this Hasher's output size and the zero
//     [Digest] is not admitted; a [Role] from the wrong arity half
//     panics, as does an operand of the wrong width.
//   - [Hasher.NewStream] returns a fresh [Stream] for streaming
//     inputs that don't fit in memory or that compose multiple
//     fields without per-field concatenation.
//
// # Domain separation
//
// There is no untagged two-operand combine. An unprefixed
// H(left || right) is indistinguishable from the hash of a
// caller-chosen payload of exactly two digest widths, which is how a
// fabricated entry verifies against a shortened authentication path.
// Its only correct uses are the tagged ones. Anything building a tree
// or a chain uses [Hasher.HashTagged] and [Hasher.CombineTagged]. A
// [Role] byte separates the two constructions, and the role's high bit
// keeps a leaf role and a node role from ever sharing one. See [Role].
//
// [Hasher.Hash] survives untagged because a content address must be
// the digest OF the bytes: prefixing it would make the address name
// something the bytes are not.
//
// # Concurrency
//
// Implementations of [Hasher] must be safe for concurrent use, and
// consumers share one Hasher across many goroutines. The returned
// [Stream] is NOT safe for concurrent use, and each goroutine that
// streams uses its own.
//
// # Allocation contract
//
// [Hasher.ID], [Hasher.Algorithm], [Hasher.Hash], [Hasher.HashTagged]
// and [Hasher.CombineTagged] do not allocate on any implementation in
// this module. HashTagged borrows a [Stream] from the pool that
// [HashDomain] also uses, so the first call after the pool is emptied
// allocates one. [Hasher.NewStream] allocates the underlying hash
// state once.
//
// These contracts count what an implementation allocates. A slice
// passed through this interface escapes to the heap at the call site,
// because the compiler cannot see the implementation. A caller that
// must not allocate passes data that is already on the heap, such as a
// reused buffer.
type Hasher interface {
	// ID returns the implementation's stable build-local
	// identifier.
	ID() ID

	// Algorithm returns the long-term cross-build algorithm
	// name (for example [AlgSHA256]). Persist it in artefacts
	// that may outlive the producing build.
	Algorithm() Algorithm

	// Hash returns the [Digest] of data. Hot path for content
	// addressing, where the address must be the digest OF the
	// bytes and nothing else. Zero-allocation on every
	// implementation in this module.
	//
	// A leaf of a tree or chain uses [Hasher.HashTagged]: an
	// unprefixed leaf hash can be made to equal an interior node.
	Hash(data []byte) Digest

	// HashTagged returns the [Digest] of data under r, a unary
	// [Role]. Hot path for the leaves of a tree or chain, where
	// data is caller-supplied and could otherwise be chosen to
	// collide with an interior node.
	//
	// Content addressing uses [Hasher.Hash] instead: a content
	// address is the digest OF the bytes, so prefixing it would
	// make the address name something the bytes are not.
	//
	// r with the high bit set panics. The values 0x80–0xFF are
	// binary roles, and admitting one here would let a crafted
	// payload share bytes with an interior node. Empty data is
	// legal and gives the digest of the role byte alone, which
	// collides with nothing shorter.
	//
	//testkit:sample SampleUnaryRole SampleBytes
	HashTagged(r Role, data []byte) Digest

	// CombineTagged returns the [Digest] of left || right under r,
	// a binary [Role]. Hot path for chain extension and Merkle
	// accumulator construction.
	//
	// r with the high bit clear panics. Both operands must have
	// [Digest.Size] equal to this Hasher's output size. A mismatch
	// panics, and the zero [Digest] is a mismatch with its own
	// diagnostic. A chain's first link is a unary role over one
	// operand, so there is no sentinel to admit here.
	//
	//nolint:dupword // testkit directive: one builder per parameter, positional
	//testkit:sample SampleBinaryRole SampleDigest SampleDigest
	CombineTagged(r Role, left, right Digest) Digest

	// NewStream returns a fresh [Stream] for inputs that do not fit
	// in memory, or that compose several fields without
	// concatenating them. It allocates the underlying hash state
	// once, and [Stream.Write], [Stream.Sum] and [Stream.Reset] do
	// not allocate after that.
	//
	//testkit:nondeterministic
	NewStream() Stream
}

// Stream is an in-progress hash computation. Bytes are appended
// via the embedded [io.Writer]; [Stream.Sum] finalises and
// returns a snapshot [Digest] without resetting state, matching
// stdlib [hash.Hash] semantics. [Stream.Reset] clears the state
// for reuse.
//
// A long-lived consumer constructs one Stream per goroutine via
// [Hasher.NewStream], then calls [Stream.Reset] between hashes
// to amortise the hash-state allocation:
//
//	s := h.NewStream()
//	for entry := range entries {
//	    s.Reset()
//	    s.Write(domain)
//	    s.Write(entry)
//	    digests[i] = s.Sum()
//	}
//
// # Concurrency
//
// Streams are NOT safe for concurrent use. Each goroutine that
// streams uses its own [Stream].
//
// # Allocation contract
//
// [Stream.Write], [Stream.Sum], [Stream.Reset] and [Stream.Close] do
// not allocate. [Hasher.NewStream] allocates the hash state and the
// output buffer for [Stream.Sum] once. On implementations that pool
// streams, [Stream.Close] returns the instance to the pool, and the
// next [Hasher.NewStream] does not allocate while the pool is warm. A
// slice passed to [Stream.Write] through this interface escapes to the
// heap at the call site, as it does for [Hasher].
type Stream interface {
	io.Writer

	// Sum finalises the in-progress hash and returns the
	// resulting Digest. Sum does not reset state; subsequent
	// Write calls extend the same hash. Call Reset to start a
	// new hash.
	Sum() Digest

	// Reset clears the in-progress hash state. After Reset, the
	// Stream is equivalent to a freshly-returned
	// [Hasher.NewStream] result and can be reused.
	Reset()

	// Close returns the Stream to its [Hasher]'s pool, and does
	// nothing on implementations that do not pool. The Stream MUST
	// NOT be used after Close, and later Write, Sum and Reset calls
	// have undefined behaviour.
	//
	// One-shot consumers, such as [HashDomain] and [HashReader],
	// Close after Sum to recycle the stream. A long-lived consumer
	// that builds a Stream once and reuses it with [Stream.Reset]
	// does not need to Close. The pool tolerates streams that are
	// never returned, because sync.Pool's factory creates a fresh
	// instance on the next [Hasher.NewStream].
	Close()
}
