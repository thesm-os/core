// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package sha256

import (
	"crypto/sha256"
	"fmt"
	"hash"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/pool"
)

// streamPool holds reusable SHA-256 stream instances. Hasher is
// stateless (zero-value struct), so a single package-level pool
// is shared across every [New] / zero-value caller.
var streamPool = pool.NewPool(func() *stream {
	return &stream{h: sha256.New()}
})

// id is this implementation's stable build-local identifier. The
// bytes spell out "sha256/v1" left-aligned with zero padding to
// [crypto.IDSize].
var id = crypto.ID{'s', 'h', 'a', '2', '5', '6', '/', 'v', '1'}

// Hasher implements [crypto.Hasher] using [crypto/sha256].
//
// The zero value is usable. Stateless and safe for concurrent
// use; the per-call hash state for [Hasher.NewStream] lives on
// the returned [crypto.Stream], which is single-goroutine.
//
// # Allocation contract
//
// [Hasher.ID], [Hasher.Algorithm], [Hasher.Hash], and
// [Hasher.Combine] are zero-alloc. [Hasher.NewStream] allocates
// the underlying [hash.Hash] once.
type Hasher struct{}

// Compile-time interface check.
var _ crypto.Hasher = Hasher{}

// New returns a [Hasher]. Equivalent to the zero-value [Hasher];
// offered as a constructor for use sites that prefer one.
func New() Hasher { return Hasher{} }

// ID returns the stable build-local identifier.
func (Hasher) ID() crypto.ID { return id }

// Algorithm returns [crypto.AlgSHA256].
func (Hasher) Algorithm() crypto.Algorithm { return crypto.AlgSHA256 }

// Hash returns SHA-256(data).
//
// # Allocation contract
//
// Zero alloc.
func (Hasher) Hash(data []byte) crypto.Digest {
	return crypto.NewDigest256(sha256.Sum256(data))
}

// Combine returns SHA-256(left || right). The 64-byte
// concatenation is exactly one SHA-256 block, so the hash
// function processes the input in a single compress invocation
// with no padding.
//
// Combine panics if either digest's [crypto.Digest.Size] differs
// from [crypto.DigestSize256] — a programmer error that would
// otherwise produce a silently-wrong digest. See the package doc
// "Failure semantics" section.
//
// # Allocation contract
//
// Zero alloc on the success path — the 64-byte concat lives on
// the stack and [crypto/sha256.Sum256] does not escape its
// argument (concrete function, not the [hash.Hash] interface).
func (Hasher) Combine(left, right crypto.Digest) crypto.Digest {
	if left.Size() != crypto.DigestSize256 || right.Size() != crypto.DigestSize256 {
		// Precondition violation; see crypto package "Failure
		// semantics" — programmer errors panic to surface
		// silent audit-chain corruption.
		panic(fmt.Sprintf( //nolint:forbidigo
			"crypto/sha256: Combine requires %d-byte digests, got left=%d right=%d",
			crypto.DigestSize256, left.Size(), right.Size(),
		))
	}
	var buf [64]byte
	copy(buf[:32], left.Bytes())
	copy(buf[32:], right.Bytes())
	return crypto.NewDigest256(sha256.Sum256(buf[:]))
}

// NewStream returns a [crypto.Stream] backed by
// [crypto/sha256.New]. Streams are drawn from a package-level
// pool; [Stream.Close] returns the instance for reuse — see the
// [crypto.Stream] documentation. Zero-allocation on the warm
// path.
func (Hasher) NewStream() crypto.Stream {
	s := streamPool.Get()
	s.h.Reset()
	return s
}

// stream wraps a stdlib [hash.Hash] to satisfy [crypto.Stream].
//
// The output buffer lives on the receiver — heap-allocated once
// by [Hasher.NewStream] — so [stream.Sum] passes a slice over
// already-heap memory to [hash.Hash.Sum] without forcing another
// allocation through the interface boundary's escape analysis.
type stream struct {
	h   hash.Hash
	out [crypto.DigestSize256]byte
}

// Compile-time interface check.
var _ crypto.Stream = (*stream)(nil)

// Write feeds p into the underlying hash.Hash. The stdlib
// hash.Hash contract guarantees no error path, so Write
// always reports (len(p), nil).
func (s *stream) Write(p []byte) (int, error) {
	// hash.Hash.Write never returns a non-nil error per the
	// stdlib contract; we propagate the same guarantee.
	n, _ := s.h.Write(p)
	return n, nil
}

// Sum returns the SHA-256 of every byte written so far. The
// output buffer lives on the receiver — already heap-allocated
// by [Hasher.NewStream] — so Sum reuses owned memory and
// does not escape an additional slice through the
// hash.Hash interface boundary. State is preserved; further
// [stream.Write] calls extend the same hash.
func (s *stream) Sum() crypto.Digest {
	s.h.Sum(s.out[:0])
	return crypto.NewDigest256(s.out)
}

// Reset clears the underlying hash.Hash state so the
// stream can be reused for a fresh digest. The receiver's
// output buffer is reused as-is; no allocation.
func (s *stream) Reset() {
	s.h.Reset()
}

// Close returns the stream to the package-level pool so the next
// [Hasher.NewStream] caller can reuse it. The stream MUST NOT be
// used after Close.
func (s *stream) Close() {
	streamPool.Put(s)
}
