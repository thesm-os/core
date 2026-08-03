// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package sha512

import (
	"crypto/sha512"
	"fmt"
	"hash"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/pool"
)

// Package-level stream pools. Each Hasher type is stateless, so a
// single pool per algorithm is shared across all instances.
var (
	stream384Pool = pool.NewPool(func() *stream384 {
		return &stream384{h: sha512.New384()}
	})
	stream512Pool = pool.NewPool(func() *stream512 {
		return &stream512{h: sha512.New()}
	})
)

// Stable build-local IDs.
var (
	id384 = crypto.ID{'s', 'h', 'a', '3', '8', '4', '/', 'v', '1'}
	id512 = crypto.ID{'s', 'h', 'a', '5', '1', '2', '/', 'v', '1'}
)

// Hasher384 implements [crypto.Hasher] using SHA-384 from
// [crypto/sha512].
//
// The zero value is usable. Stateless and safe for concurrent
// use.
type Hasher384 struct{}

// Compile-time interface check.
var _ crypto.Hasher = Hasher384{}

// New384 returns a SHA-384 [crypto.Hasher].
func New384() Hasher384 { return Hasher384{} }

// ID returns the stable build-local identifier.
func (Hasher384) ID() crypto.ID { return id384 }

// Algorithm returns [crypto.AlgSHA384].
func (Hasher384) Algorithm() crypto.Algorithm { return crypto.AlgSHA384 }

// Hash returns SHA-384(data).
//
// # Allocation contract
//
// Zero alloc.
func (Hasher384) Hash(data []byte) crypto.Digest {
	return crypto.NewDigest384(sha512.Sum384(data))
}

// Combine returns SHA-384(left || right). The 96-byte input fits
// in one SHA-512 block (1024 bits).
//
// Combine panics if either digest's [crypto.Digest.Size] differs
// from [crypto.DigestSize384] — a programmer error that would
// otherwise produce a silently-wrong digest. See the package doc
// "Failure semantics" section.
//
// # Allocation contract
//
// Zero alloc on the success path.
func (Hasher384) Combine(left, right crypto.Digest) crypto.Digest {
	if left.Size() != crypto.DigestSize384 || right.Size() != crypto.DigestSize384 {
		// Precondition violation; see crypto package "Failure
		// semantics" — programmer errors panic to surface
		// silent audit-chain corruption.
		panic(fmt.Sprintf( //nolint:forbidigo
			"crypto/sha512: SHA-384 Combine requires %d-byte digests, got left=%d right=%d",
			crypto.DigestSize384, left.Size(), right.Size(),
		))
	}
	var buf [2 * crypto.DigestSize384]byte
	copy(buf[:crypto.DigestSize384], left.Bytes())
	copy(buf[crypto.DigestSize384:], right.Bytes())
	return crypto.NewDigest384(sha512.Sum384(buf[:]))
}

// NewStream returns a streaming SHA-384 [crypto.Stream] drawn
// from a package-level pool; [Stream.Close] returns the
// instance for reuse. Zero-allocation on the warm path.
func (Hasher384) NewStream() crypto.Stream {
	s := stream384Pool.Get()
	s.h.Reset()
	return s
}

// Hasher512 implements [crypto.Hasher] using SHA-512 from
// [crypto/sha512].
type Hasher512 struct{}

// Compile-time interface check.
var _ crypto.Hasher = Hasher512{}

// New512 returns a SHA-512 [crypto.Hasher].
func New512() Hasher512 { return Hasher512{} }

// ID returns the stable build-local identifier.
func (Hasher512) ID() crypto.ID { return id512 }

// Algorithm returns [crypto.AlgSHA512].
func (Hasher512) Algorithm() crypto.Algorithm { return crypto.AlgSHA512 }

// Hash returns SHA-512(data).
//
// # Allocation contract
//
// Zero alloc.
func (Hasher512) Hash(data []byte) crypto.Digest {
	return crypto.NewDigest512(sha512.Sum512(data))
}

// Combine returns SHA-512(left || right). The 128-byte input is
// exactly one SHA-512 block.
//
// Combine panics if either digest's [crypto.Digest.Size] differs
// from [crypto.DigestSize512] — a programmer error that would
// otherwise produce a silently-wrong digest. See the package doc
// "Failure semantics" section.
//
// # Allocation contract
//
// Zero alloc on the success path.
func (Hasher512) Combine(left, right crypto.Digest) crypto.Digest {
	if left.Size() != crypto.DigestSize512 || right.Size() != crypto.DigestSize512 {
		// Precondition violation; see crypto package "Failure
		// semantics" — programmer errors panic to surface
		// silent audit-chain corruption.
		panic(fmt.Sprintf( //nolint:forbidigo
			"crypto/sha512: SHA-512 Combine requires %d-byte digests, got left=%d right=%d",
			crypto.DigestSize512, left.Size(), right.Size(),
		))
	}
	var buf [2 * crypto.DigestSize512]byte
	copy(buf[:crypto.DigestSize512], left.Bytes())
	copy(buf[crypto.DigestSize512:], right.Bytes())
	return crypto.NewDigest512(sha512.Sum512(buf[:]))
}

// NewStream returns a streaming SHA-512 [crypto.Stream] drawn
// from a package-level pool; [Stream.Close] returns the
// instance for reuse. Zero-allocation on the warm path.
func (Hasher512) NewStream() crypto.Stream {
	s := stream512Pool.Get()
	s.h.Reset()
	return s
}

// stream384 wraps a stdlib SHA-384 [hash.Hash] for [crypto.Stream].
type stream384 struct {
	h   hash.Hash
	out [crypto.DigestSize384]byte
}

// Compile-time interface check.
var _ crypto.Stream = (*stream384)(nil)

// Write feeds p into the underlying SHA-384 hash.Hash.
// The stdlib contract guarantees no error path, so Write
// always reports (len(p), nil).
func (s *stream384) Write(p []byte) (int, error) {
	// hash.Hash.Write never returns a non-nil error per the
	// stdlib contract; we propagate the same guarantee.
	n, _ := s.h.Write(p)
	return n, nil
}

// Sum returns the SHA-384 of every byte written so far.
// The receiver-owned output buffer keeps Sum zero-alloc
// through the hash.Hash interface boundary. State is
// preserved; further writes extend the same hash.
func (s *stream384) Sum() crypto.Digest {
	s.h.Sum(s.out[:0])
	return crypto.NewDigest384(s.out)
}

// Reset clears the underlying SHA-384 state so the stream
// can be reused for a fresh digest.
func (s *stream384) Reset() { s.h.Reset() }

// Close returns the stream to the package-level pool. The
// stream MUST NOT be used after Close.
func (s *stream384) Close() { stream384Pool.Put(s) }

// stream512 wraps a stdlib SHA-512 [hash.Hash] for [crypto.Stream].
type stream512 struct {
	h   hash.Hash
	out [crypto.DigestSize512]byte
}

// Compile-time interface check.
var _ crypto.Stream = (*stream512)(nil)

// Write feeds p into the underlying SHA-512 hash.Hash.
// The stdlib contract guarantees no error path, so Write
// always reports (len(p), nil).
func (s *stream512) Write(p []byte) (int, error) {
	// hash.Hash.Write never returns a non-nil error per the
	// stdlib contract; we propagate the same guarantee.
	n, _ := s.h.Write(p)
	return n, nil
}

// Sum returns the SHA-512 of every byte written so far.
// The receiver-owned output buffer keeps Sum zero-alloc
// through the hash.Hash interface boundary. State is
// preserved; further writes extend the same hash.
func (s *stream512) Sum() crypto.Digest {
	s.h.Sum(s.out[:0])
	return crypto.NewDigest512(s.out)
}

// Reset clears the underlying SHA-512 state so the stream
// can be reused for a fresh digest.
func (s *stream512) Reset() { s.h.Reset() }

// Close returns the stream to the package-level pool. The
// stream MUST NOT be used after Close.
func (s *stream512) Close() { stream512Pool.Put(s) }
