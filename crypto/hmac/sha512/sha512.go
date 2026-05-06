// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package sha512

import (
	stdhmac "crypto/hmac"
	stdsha512 "crypto/sha512"
	"crypto/subtle"
	"hash"

	"go.thesmos.sh/core/crypto"
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
// use.
//
// # Allocation contract
//
// [MAC384.ID], [MAC384.Algorithm], and [MAC384.Size] are
// zero-alloc. [MAC384.Sign] and [MAC384.Verify] each allocate
// the underlying HMAC state once per call. [MAC384.NewStream]
// allocates the wrapper plus the underlying stdlib HMAC state;
// [crypto.Stream] methods are zero-alloc thereafter.
type MAC384 struct {
	key []byte
}

// Compile-time interface check.
var _ crypto.MAC = (*MAC384)(nil)

// NewSHA384 returns an HMAC-SHA-384 [MAC] bound to a defensive
// copy of key.
func NewSHA384(key []byte) *MAC384 {
	keyCopy := make([]byte, len(key))
	copy(keyCopy, key)
	return &MAC384{key: keyCopy}
}

// ID returns the stable build-local identifier.
func (*MAC384) ID() crypto.ID { return id384 }

// Algorithm returns [crypto.AlgHMACSHA384].
func (*MAC384) Algorithm() crypto.Algorithm { return crypto.AlgHMACSHA384 }

// Size returns [crypto.DigestSize384].
func (*MAC384) Size() int { return crypto.DigestSize384 }

// Sign returns HMAC-SHA-384(key, data).
func (m *MAC384) Sign(data []byte) crypto.Digest {
	h := stdhmac.New(stdsha512.New384, m.key)
	// hash.Hash.Write never returns a non-nil error per the
	// stdlib contract; ignoring is safe.
	_, _ = h.Write(data)
	var out [crypto.DigestSize384]byte
	h.Sum(out[:0])
	return crypto.NewDigest384(out)
}

// Verify reports whether expected is HMAC-SHA-384(key, data).
// The comparison is constant-time over the active byte prefix;
// size mismatch short-circuits to false.
func (m *MAC384) Verify(data, expected []byte) bool {
	if len(expected) != crypto.DigestSize384 {
		return false
	}
	h := stdhmac.New(stdsha512.New384, m.key)
	// hash.Hash.Write never returns a non-nil error per the
	// stdlib contract; ignoring is safe.
	_, _ = h.Write(data)
	var got [crypto.DigestSize384]byte
	h.Sum(got[:0])
	return subtle.ConstantTimeCompare(got[:], expected) == 1
}

// NewStream returns a fresh streaming [crypto.Stream] backed by
// HMAC-SHA-384 over the instance's key.
func (m *MAC384) NewStream() crypto.Stream {
	return &stream384{h: stdhmac.New(stdsha512.New384, m.key)}
}

// MAC512 implements [crypto.MAC] as HMAC-SHA-512 over a fixed
// key.
type MAC512 struct {
	key []byte
}

// Compile-time interface check.
var _ crypto.MAC = (*MAC512)(nil)

// NewSHA512 returns an HMAC-SHA-512 [MAC] bound to a defensive
// copy of key.
func NewSHA512(key []byte) *MAC512 {
	keyCopy := make([]byte, len(key))
	copy(keyCopy, key)
	return &MAC512{key: keyCopy}
}

// ID returns the stable build-local identifier.
func (*MAC512) ID() crypto.ID { return id512 }

// Algorithm returns [crypto.AlgHMACSHA512].
func (*MAC512) Algorithm() crypto.Algorithm { return crypto.AlgHMACSHA512 }

// Size returns [crypto.DigestSize512].
func (*MAC512) Size() int { return crypto.DigestSize512 }

// Sign returns HMAC-SHA-512(key, data).
func (m *MAC512) Sign(data []byte) crypto.Digest {
	h := stdhmac.New(stdsha512.New, m.key)
	// hash.Hash.Write never returns a non-nil error per the
	// stdlib contract; ignoring is safe.
	_, _ = h.Write(data)
	var out [crypto.DigestSize512]byte
	h.Sum(out[:0])
	return crypto.NewDigest512(out)
}

// Verify reports whether expected is HMAC-SHA-512(key, data).
// The comparison is constant-time over the active byte prefix;
// size mismatch short-circuits to false.
func (m *MAC512) Verify(data, expected []byte) bool {
	if len(expected) != crypto.DigestSize512 {
		return false
	}
	h := stdhmac.New(stdsha512.New, m.key)
	// hash.Hash.Write never returns a non-nil error per the
	// stdlib contract; ignoring is safe.
	_, _ = h.Write(data)
	var got [crypto.DigestSize512]byte
	h.Sum(got[:0])
	return subtle.ConstantTimeCompare(got[:], expected) == 1
}

// NewStream returns a fresh streaming [crypto.Stream] backed by
// HMAC-SHA-512 over the instance's key.
func (m *MAC512) NewStream() crypto.Stream {
	return &stream512{h: stdhmac.New(stdsha512.New, m.key)}
}

// stream384 wraps a stdlib HMAC-SHA-384-keyed [hash.Hash] for
// [crypto.Stream]. The output buffer lives on the receiver so
// [Stream.Sum] reuses already-owned heap memory.
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

// stream512 wraps a stdlib HMAC-SHA-512-keyed [hash.Hash] for
// [crypto.Stream].
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
