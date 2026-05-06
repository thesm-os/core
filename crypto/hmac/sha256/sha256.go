// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package sha256

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"hash"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/pool"
)

// id is this implementation's stable build-local identifier. The
// bytes spell out "hmac-sha256/v1" left-aligned with zero padding
// to [crypto.IDSize].
var id = crypto.ID{'h', 'm', 'a', 'c', '-', 's', 'h', 'a', '2', '5', '6', '/', 'v', '1'}

// MAC implements [crypto.MAC] as HMAC-SHA-256 over a fixed key.
//
// The key is copied at construction; the source buffer may be
// reused or zeroed immediately. MAC is safe for concurrent use:
// the underlying [hash.Hash] instances are pooled in a per-MAC
// [pool.Pool] and shared across goroutines via Get / Put.
//
// # Allocation contract
//
// [MAC.ID], [MAC.Algorithm], [MAC.Size], [MAC.Sign], and
// [MAC.Verify] are zero-allocation on the success path after
// the first warm-up call. The pool caches pre-keyed
// [hash.Hash] instances; [hash.Hash.Reset] preserves the HMAC
// key state per stdlib documented behaviour. The first call
// after construction or after GC pool eviction allocates one
// underlying stdlib HMAC state via [hmac.New].
//
// [MAC.NewStream] allocates the wrapper plus the underlying
// stdlib HMAC state; [crypto.Stream] methods are zero-alloc
// thereafter.
type MAC struct {
	key  []byte
	pool *pool.Pool[*macEntry]
}

// macEntry is one pooled HMAC instance. Holding the output
// buffer on the entry alongside the hash keeps both heap-
// resident across Get / Put — passing [macEntry.out][:0]
// through [hash.Hash.Sum]'s interface boundary therefore does
// not force a per-call escape of a stack-local buffer.
type macEntry struct {
	h   hash.Hash
	out [crypto.DigestSize256]byte
}

// Compile-time interface check.
var _ crypto.MAC = (*MAC)(nil)

// New returns a MAC bound to a defensive copy of key.
//
// HMAC accepts any key length: keys longer than the hash block
// size are pre-hashed, and keys shorter are zero-padded, both
// per RFC 2104. Cryptographic guidance recommends ≥ 32 bytes
// (the SHA-256 output size) of high-entropy material; enforcing
// a minimum is a higher-layer policy concern.
func New(key []byte) *MAC {
	keyCopy := make([]byte, len(key))
	copy(keyCopy, key)
	m := &MAC{key: keyCopy}
	m.pool = pool.NewPool(func() *macEntry {
		return &macEntry{h: hmac.New(sha256.New, m.key)}
	})
	return m
}

// ID returns the stable build-local identifier.
func (*MAC) ID() crypto.ID { return id }

// Algorithm returns [crypto.AlgHMACSHA256].
func (*MAC) Algorithm() crypto.Algorithm { return crypto.AlgHMACSHA256 }

// Size returns [crypto.DigestSize256].
func (*MAC) Size() int { return crypto.DigestSize256 }

// Sign returns HMAC-SHA-256(key, data).
//
// # Allocation contract
//
// Zero-allocation steady state. The first call after
// construction (or after GC pool eviction) allocates one
// underlying stdlib HMAC state.
func (m *MAC) Sign(data []byte) crypto.Digest {
	e := m.pool.Get()
	e.h.Reset()
	// hash.Hash.Write never returns a non-nil error per the
	// stdlib contract; ignoring is safe.
	_, _ = e.h.Write(data)
	e.h.Sum(e.out[:0])
	digest := crypto.NewDigest256(e.out)
	m.pool.Put(e)
	return digest
}

// Verify reports whether expected is HMAC-SHA-256(key, data).
//
// The comparison is performed in constant time over the active
// byte prefix; size mismatch short-circuits to false. Size is
// public information determined by the algorithm, so the early
// return is not a timing hazard.
//
// # Allocation contract
//
// Zero-allocation steady state, same as [MAC.Sign].
func (m *MAC) Verify(data, expected []byte) bool {
	if len(expected) != crypto.DigestSize256 {
		return false
	}
	e := m.pool.Get()
	e.h.Reset()
	// hash.Hash.Write never returns a non-nil error per the
	// stdlib contract; ignoring is safe.
	_, _ = e.h.Write(data)
	e.h.Sum(e.out[:0])
	ok := subtle.ConstantTimeCompare(e.out[:], expected) == 1
	m.pool.Put(e)
	return ok
}

// NewStream returns a fresh streaming [crypto.Stream] backed by
// HMAC-SHA-256 over the instance's key.
//
// Verification of a streamed payload: write the bytes, finalise
// with [crypto.Stream.Sum], and compare the resulting [crypto.Digest]
// against the expected value with [crypto.Digest.ConstantTimeEqual].
// Plain `==` and [crypto.Digest.Equal] are NOT constant-time and
// must not be used.
func (m *MAC) NewStream() crypto.Stream {
	return &stream{h: hmac.New(sha256.New, m.key)}
}

// stream wraps a stdlib HMAC-keyed [hash.Hash] to satisfy
// [crypto.Stream]. The output buffer lives on the receiver —
// heap-allocated once by [MAC.NewStream] — so [stream.Sum]
// reuses already-owned heap memory rather than escaping a
// stack-local through the [hash.Hash] interface boundary.
type stream struct {
	h   hash.Hash
	out [crypto.DigestSize256]byte
}

// Compile-time interface check.
var _ crypto.Stream = (*stream)(nil)

// Write feeds p into the underlying HMAC state. The stdlib
// [hash.Hash] contract guarantees no error path, so Write
// always reports (len(p), nil).
func (s *stream) Write(p []byte) (int, error) {
	// hash.Hash.Write never returns a non-nil error per the
	// stdlib contract; we propagate the same guarantee.
	n, _ := s.h.Write(p)
	return n, nil
}

// Sum returns HMAC-SHA-256 of every byte written so far. State
// is preserved; further [stream.Write] calls extend the same
// computation. Call [stream.Reset] to start a new MAC over the
// same key.
func (s *stream) Sum() crypto.Digest {
	s.h.Sum(s.out[:0])
	return crypto.NewDigest256(s.out)
}

// Reset clears the in-progress HMAC state. The key remains
// loaded — [hash.Hash.Reset] on an HMAC instance returns to the
// post-construction state, not to an unkeyed state — so the
// stream can be reused for a fresh MAC under the same key.
func (s *stream) Reset() {
	s.h.Reset()
}
