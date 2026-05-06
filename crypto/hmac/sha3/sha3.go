// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package sha3

import (
	stdhmac "crypto/hmac"
	stdsha3 "crypto/sha3"
	"crypto/subtle"
	"hash"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/pool"
)

// Stable build-local IDs.
var (
	id256 = crypto.ID{
		'h', 'm', 'a', 'c', '-', 's', 'h', 'a', '3', '-', '2', '5', '6', '/', 'v', '1',
	}
	id384 = crypto.ID{
		'h', 'm', 'a', 'c', '-', 's', 'h', 'a', '3', '-', '3', '8', '4', '/', 'v', '1',
	}
	id512 = crypto.ID{
		'h', 'm', 'a', 'c', '-', 's', 'h', 'a', '3', '-', '5', '1', '2', '/', 'v', '1',
	}
)

// The stdlib's [crypto/sha3] constructors return *sha3.SHA3, not
// hash.Hash; [crypto/hmac.New] expects `func() hash.Hash`.
// Wrap each constructor at package scope so the function value
// is allocated once at init.
var (
	new256 = func() hash.Hash { return stdsha3.New256() }
	new384 = func() hash.Hash { return stdsha3.New384() }
	new512 = func() hash.Hash { return stdsha3.New512() }
)

// MAC256 implements [crypto.MAC] as HMAC-SHA3-256 over a fixed
// key. Allocation contract and concurrency mirror
// [go.thesmos.sh/core/crypto/hmac/sha256.MAC].
type MAC256 struct {
	key  []byte
	pool *pool.Pool[*entry256]
}

// entry256 is one pooled HMAC-SHA3-256 instance.
type entry256 struct {
	h   hash.Hash
	out [crypto.DigestSize256]byte
}

var _ crypto.MAC = (*MAC256)(nil)

// NewSHA3_256 returns an HMAC-SHA3-256 [MAC] bound to a
// defensive copy of key.
func NewSHA3_256(key []byte) *MAC256 {
	keyCopy := make([]byte, len(key))
	copy(keyCopy, key)
	m := &MAC256{key: keyCopy}
	m.pool = pool.NewPool(func() *entry256 {
		return &entry256{h: stdhmac.New(new256, m.key)}
	})
	return m
}

// ID returns the stable build-local identifier.
func (*MAC256) ID() crypto.ID { return id256 }

// Algorithm returns [crypto.AlgHMACSHA3_256].
func (*MAC256) Algorithm() crypto.Algorithm { return crypto.AlgHMACSHA3_256 }

// Size returns [crypto.DigestSize256].
func (*MAC256) Size() int { return crypto.DigestSize256 }

// Sign returns HMAC-SHA3-256(key, data).
func (m *MAC256) Sign(data []byte) crypto.Digest {
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

// Verify reports whether expected is HMAC-SHA3-256(key, data).
// The comparison is constant-time over the active byte prefix;
// size mismatch short-circuits to false.
func (m *MAC256) Verify(data, expected []byte) bool {
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

// NewStream returns a fresh streaming HMAC-SHA3-256
// [crypto.Stream].
func (m *MAC256) NewStream() crypto.Stream {
	return &stream256{h: stdhmac.New(new256, m.key)}
}

// MAC384 implements [crypto.MAC] as HMAC-SHA3-384 over a fixed
// key.
type MAC384 struct {
	key  []byte
	pool *pool.Pool[*entry384]
}

// entry384 is one pooled HMAC-SHA3-384 instance.
type entry384 struct {
	h   hash.Hash
	out [crypto.DigestSize384]byte
}

var _ crypto.MAC = (*MAC384)(nil)

// NewSHA3_384 returns an HMAC-SHA3-384 [MAC] bound to a
// defensive copy of key.
func NewSHA3_384(key []byte) *MAC384 {
	keyCopy := make([]byte, len(key))
	copy(keyCopy, key)
	m := &MAC384{key: keyCopy}
	m.pool = pool.NewPool(func() *entry384 {
		return &entry384{h: stdhmac.New(new384, m.key)}
	})
	return m
}

// ID returns the stable build-local identifier.
func (*MAC384) ID() crypto.ID { return id384 }

// Algorithm returns [crypto.AlgHMACSHA3_384].
func (*MAC384) Algorithm() crypto.Algorithm { return crypto.AlgHMACSHA3_384 }

// Size returns [crypto.DigestSize384].
func (*MAC384) Size() int { return crypto.DigestSize384 }

// Sign returns HMAC-SHA3-384(key, data).
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

// Verify reports whether expected is HMAC-SHA3-384(key, data).
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

// NewStream returns a fresh streaming HMAC-SHA3-384
// [crypto.Stream].
func (m *MAC384) NewStream() crypto.Stream {
	return &stream384{h: stdhmac.New(new384, m.key)}
}

// MAC512 implements [crypto.MAC] as HMAC-SHA3-512 over a fixed
// key.
type MAC512 struct {
	key  []byte
	pool *pool.Pool[*entry512]
}

// entry512 is one pooled HMAC-SHA3-512 instance.
type entry512 struct {
	h   hash.Hash
	out [crypto.DigestSize512]byte
}

var _ crypto.MAC = (*MAC512)(nil)

// NewSHA3_512 returns an HMAC-SHA3-512 [MAC] bound to a
// defensive copy of key.
func NewSHA3_512(key []byte) *MAC512 {
	keyCopy := make([]byte, len(key))
	copy(keyCopy, key)
	m := &MAC512{key: keyCopy}
	m.pool = pool.NewPool(func() *entry512 {
		return &entry512{h: stdhmac.New(new512, m.key)}
	})
	return m
}

// ID returns the stable build-local identifier.
func (*MAC512) ID() crypto.ID { return id512 }

// Algorithm returns [crypto.AlgHMACSHA3_512].
func (*MAC512) Algorithm() crypto.Algorithm { return crypto.AlgHMACSHA3_512 }

// Size returns [crypto.DigestSize512].
func (*MAC512) Size() int { return crypto.DigestSize512 }

// Sign returns HMAC-SHA3-512(key, data).
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

// Verify reports whether expected is HMAC-SHA3-512(key, data).
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

// NewStream returns a fresh streaming HMAC-SHA3-512
// [crypto.Stream].
func (m *MAC512) NewStream() crypto.Stream {
	return &stream512{h: stdhmac.New(new512, m.key)}
}

// stream256 / stream384 / stream512 wrap stdlib HMAC-keyed
// [hash.Hash] states for [crypto.Stream]. The output buffer
// lives on the receiver so [Stream.Sum] reuses already-owned
// heap memory.
type stream256 struct {
	h   hash.Hash
	out [crypto.DigestSize256]byte
}

var _ crypto.Stream = (*stream256)(nil)

func (s *stream256) Write(p []byte) (int, error) {
	// hash.Hash.Write never returns a non-nil error per the
	// stdlib contract; we propagate the same guarantee.
	n, _ := s.h.Write(p)
	return n, nil
}

func (s *stream256) Sum() crypto.Digest {
	s.h.Sum(s.out[:0])
	return crypto.NewDigest256(s.out)
}

// Reset clears the in-progress HMAC state. The key remains
// loaded — [hash.Hash.Reset] on an HMAC instance returns to the
// post-construction state, not to an unkeyed state — so the
// stream can be reused for a fresh MAC under the same key.
func (s *stream256) Reset() { s.h.Reset() }

type stream384 struct {
	h   hash.Hash
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

type stream512 struct {
	h   hash.Hash
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
