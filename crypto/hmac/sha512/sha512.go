// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package sha512

import (
	stdhmac "crypto/hmac"
	stdsha512 "crypto/sha512"
	"crypto/subtle"
	"hash"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/pool"
)

// Stable build-local IDs.
var (
	id384 = crypto.ID{
		'h', 'm', 'a', 'c', '-', 's', 'h', 'a', '3', '8', '4', '/', 'v', '1',
	}
	id512 = crypto.ID{
		'h', 'm', 'a', 'c', '-', 's', 'h', 'a', '5', '1', '2', '/', 'v', '1',
	}
)

// MAC384 implements [crypto.MAC] as HMAC-SHA-384 over a fixed
// key. The key is copied at construction; the source buffer may
// be reused or zeroed immediately. MAC384 is safe for concurrent
// use: the underlying [hash.Hash] instances are pooled in a
// per-MAC [pool.Pool] and shared across goroutines.
//
// # Allocation contract
//
// [MAC384.ID], [MAC384.Algorithm], [MAC384.Size], [MAC384.Sign],
// and [MAC384.Verify] are zero-alloc on the success path after
// pool warm-up. The first call after construction or after GC
// pool eviction allocates one underlying stdlib HMAC state.
// [MAC384.NewStream] is zero-alloc on the warm path; streams
// are drawn from a per-MAC stream pool and returned via
// [crypto.Stream.Close].
type MAC384 struct {
	pool       *pool.Pool[*entry384]
	streamPool *pool.Pool[*stream384]
	key        []byte
}

// entry384 is one pooled HMAC-SHA-384 instance with its output
// buffer co-located so [hash.Hash.Sum] does not force a
// per-call escape. See [crypto/hmac/sha256.macEntry] for the
// rationale.
type entry384 struct {
	h   hash.Hash
	out [crypto.DigestSize384]byte
}

// Compile-time interface check.
var _ crypto.MAC = (*MAC384)(nil)

// NewSHA384 returns an HMAC-SHA-384 [MAC] bound to a defensive
// copy of key.
func NewSHA384(key []byte) *MAC384 {
	keyCopy := make([]byte, len(key))
	copy(keyCopy, key)
	m := &MAC384{key: keyCopy}
	m.pool = pool.NewPool(func() *entry384 {
		return &entry384{h: stdhmac.New(stdsha512.New384, m.key)}
	})
	m.streamPool = pool.NewPool(func() *stream384 {
		return &stream384{h: stdhmac.New(stdsha512.New384, m.key), mac: m}
	})
	return m
}

// ID returns the stable build-local identifier.
func (*MAC384) ID() crypto.ID { return id384 }

// Algorithm returns [crypto.AlgHMACSHA384].
func (*MAC384) Algorithm() crypto.Algorithm { return crypto.AlgHMACSHA384 }

// Size returns [crypto.DigestSize384].
func (*MAC384) Size() int { return crypto.DigestSize384 }

// Sign returns HMAC-SHA-384(key, data).
func (m *MAC384) Sign(data []byte) crypto.Digest {
	e := m.pool.Get()
	e.h.Reset()
	// hash.Hash.Write never returns a non-nil error per the
	// stdlib contract; ignoring is safe.
	_, _ = e.h.Write(data)
	e.h.Sum(e.out[:0])
	digest := crypto.NewDigest384(e.out)
	m.pool.Put(e)
	return digest
}

// Verify reports whether expected is HMAC-SHA-384(key, data).
// The comparison is constant-time over the active byte prefix;
// size mismatch short-circuits to false.
func (m *MAC384) Verify(data, expected []byte) bool {
	if len(expected) != crypto.DigestSize384 {
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

// NewStream returns a streaming [crypto.Stream] backed by
// HMAC-SHA-384 over the instance's key, drawn from a per-MAC
// pool. Zero-allocation on the warm path; [crypto.Stream.Close]
// returns the instance for reuse.
func (m *MAC384) NewStream() crypto.Stream {
	s := m.streamPool.Get()
	s.h.Reset()
	return s
}

// MAC512 implements [crypto.MAC] as HMAC-SHA-512 over a fixed
// key. Allocation contract and concurrency mirror [MAC384].
type MAC512 struct {
	pool       *pool.Pool[*entry512]
	streamPool *pool.Pool[*stream512]
	key        []byte
}

// entry512 is one pooled HMAC-SHA-512 instance.
type entry512 struct {
	h   hash.Hash
	out [crypto.DigestSize512]byte
}

// Compile-time interface check.
var _ crypto.MAC = (*MAC512)(nil)

// NewSHA512 returns an HMAC-SHA-512 [MAC] bound to a defensive
// copy of key.
func NewSHA512(key []byte) *MAC512 {
	keyCopy := make([]byte, len(key))
	copy(keyCopy, key)
	m := &MAC512{key: keyCopy}
	m.pool = pool.NewPool(func() *entry512 {
		return &entry512{h: stdhmac.New(stdsha512.New, m.key)}
	})
	m.streamPool = pool.NewPool(func() *stream512 {
		return &stream512{h: stdhmac.New(stdsha512.New, m.key), mac: m}
	})
	return m
}

// ID returns the stable build-local identifier.
func (*MAC512) ID() crypto.ID { return id512 }

// Algorithm returns [crypto.AlgHMACSHA512].
func (*MAC512) Algorithm() crypto.Algorithm { return crypto.AlgHMACSHA512 }

// Size returns [crypto.DigestSize512].
func (*MAC512) Size() int { return crypto.DigestSize512 }

// Sign returns HMAC-SHA-512(key, data).
func (m *MAC512) Sign(data []byte) crypto.Digest {
	e := m.pool.Get()
	e.h.Reset()
	// hash.Hash.Write never returns a non-nil error per the
	// stdlib contract; ignoring is safe.
	_, _ = e.h.Write(data)
	e.h.Sum(e.out[:0])
	digest := crypto.NewDigest512(e.out)
	m.pool.Put(e)
	return digest
}

// Verify reports whether expected is HMAC-SHA-512(key, data).
// The comparison is constant-time over the active byte prefix;
// size mismatch short-circuits to false.
func (m *MAC512) Verify(data, expected []byte) bool {
	if len(expected) != crypto.DigestSize512 {
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

// NewStream returns a streaming [crypto.Stream] backed by
// HMAC-SHA-512 over the instance's key, drawn from a per-MAC
// pool. Zero-allocation on the warm path; [crypto.Stream.Close]
// returns the instance for reuse.
func (m *MAC512) NewStream() crypto.Stream {
	s := m.streamPool.Get()
	s.h.Reset()
	return s
}

// stream384 wraps a stdlib HMAC-SHA-384-keyed [hash.Hash] for
// [crypto.Stream]. The output buffer lives on the receiver so
// [Stream.Sum] reuses already-owned heap memory; the back-
// reference to the parent [MAC384] lets [Close] return the
// instance to its origin pool.
type stream384 struct {
	h   hash.Hash
	mac *MAC384
	out [crypto.DigestSize384]byte
}

var _ crypto.Stream = (*stream384)(nil)

func (s *stream384) Write(p []byte) (int, error) {
	// hash.Hash.Write never returns a non-nil error per the
	// stdlib contract; we propagate the same guarantee.
	n, _ := s.h.Write(p)
	return n, nil
}

func (s *stream384) Sum() crypto.Digest {
	s.h.Sum(s.out[:0])
	return crypto.NewDigest384(s.out)
}

// Reset clears the in-progress HMAC state. The key remains
// loaded — [hash.Hash.Reset] on an HMAC instance returns to the
// post-construction state, not to an unkeyed state — so the
// stream can be reused for a fresh MAC under the same key.
func (s *stream384) Reset() { s.h.Reset() }

// Close returns the stream to the parent [MAC384]'s stream
// pool. The stream MUST NOT be used after Close.
func (s *stream384) Close() { s.mac.streamPool.Put(s) }

// stream512 wraps a stdlib HMAC-SHA-512-keyed [hash.Hash] for
// [crypto.Stream].
type stream512 struct {
	h   hash.Hash
	mac *MAC512
	out [crypto.DigestSize512]byte
}

var _ crypto.Stream = (*stream512)(nil)

func (s *stream512) Write(p []byte) (int, error) {
	// hash.Hash.Write never returns a non-nil error per the
	// stdlib contract; we propagate the same guarantee.
	n, _ := s.h.Write(p)
	return n, nil
}

func (s *stream512) Sum() crypto.Digest {
	s.h.Sum(s.out[:0])
	return crypto.NewDigest512(s.out)
}

// Reset clears the in-progress HMAC state. The key remains
// loaded — [hash.Hash.Reset] on an HMAC instance returns to the
// post-construction state, not to an unkeyed state — so the
// stream can be reused for a fresh MAC under the same key.
func (s *stream512) Reset() { s.h.Reset() }

// Close returns the stream to the parent [MAC512]'s stream
// pool. The stream MUST NOT be used after Close.
func (s *stream512) Close() { s.mac.streamPool.Put(s) }
