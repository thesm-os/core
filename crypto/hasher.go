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

// String returns the hex-encoded ID. Allocates the result
// string; intended for diagnostic output, not the hot path. Hex
// is used (rather than ASCII trimming) so the string is
// well-defined regardless of the byte content.
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
//     path for leaf commitments.
//   - [Hasher.Combine] returns the [Digest] of left || right.
//     Hot path for chain extension and Merkle accumulator
//     construction. left and right must have [Digest.Size] equal
//     to this Hasher's output size; a mismatch panics with a
//     diagnostic message rather than silently producing a
//     truncated digest. See the package "Failure semantics"
//     section.
//   - [Hasher.NewStream] returns a fresh [Stream] for streaming
//     inputs that don't fit in memory or that compose multiple
//     fields without per-field concatenation.
//
// # Concurrency
//
// Implementations of [Hasher] must be safe for concurrent use;
// consumers share one Hasher across many goroutines. The
// returned [Stream] is NOT safe for concurrent use — each
// goroutine that streams should hold its own.
//
// # Allocation contract
//
// [Hasher.ID], [Hasher.Algorithm], [Hasher.Hash], and
// [Hasher.Combine] are zero-allocation on every implementation
// in this module. [Hasher.NewStream] allocates the underlying
// hash state once.
type Hasher interface {
	// ID returns the implementation's stable build-local
	// identifier.
	ID() ID

	// Algorithm returns the long-term cross-build algorithm
	// name (for example [AlgSHA256]). Persist it in artefacts
	// that may outlive the producing build.
	Algorithm() Algorithm

	// Hash returns the [Digest] of data. Hot path for leaf
	// commitments. Zero-allocation on every implementation in
	// this module.
	Hash(data []byte) Digest

	// Combine returns the [Digest] of left || right. Hot path
	// for chain extension and Merkle accumulator construction.
	// left and right must have [Digest.Size] equal to this
	// Hasher's output size; a mismatch panics with a diagnostic
	// message rather than silently producing a truncated digest.
	// See the package "Failure semantics" section.
	//
	//nolint:dupword // testkit directive: one builder per parameter, positional
	//testkit:sample SampleDigest SampleDigest
	Combine(left, right Digest) Digest

	// NewStream returns a fresh [Stream] for streaming inputs
	// that don't fit in memory or that compose multiple fields
	// without per-field concatenation. Allocates the underlying
	// hash state once; [Stream.Write] / [Stream.Sum] /
	// [Stream.Reset] are zero-allocation thereafter.
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
// streams should hold its own [Stream].
//
// # Allocation contract
//
// [Stream.Write], [Stream.Sum], [Stream.Reset], and
// [Stream.Close] are zero-allocation. The hash state and the
// output buffer for [Stream.Sum] are allocated once by
// [Hasher.NewStream] and reused thereafter; on impls that pool
// streams, [Stream.Close] returns the instance to the pool, and
// the next [Hasher.NewStream] call is zero-allocation when the
// pool is warm.
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

	// Close releases the Stream back to its [Hasher]'s pool (or
	// no-ops on impls that don't pool). The Stream MUST NOT be
	// used after Close — subsequent Write/Sum/Reset calls have
	// undefined behaviour.
	//
	// One-shot consumers ([HashDomain], [HashReader]) Close
	// after Sum to recycle the stream. Long-lived consumers —
	// per-message hot paths that construct a Stream once and
	// reuse it via [Stream.Reset] — never need to Close. The
	// [Hasher]'s pool tolerates streams permanently held outside
	// it (sync.Pool's factory creates a fresh instance on the
	// next [Hasher.NewStream] call).
	Close()
}
