// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package sha3

import (
	"crypto/sha3"
	"fmt"
	"hash"

	"go.thesmos.sh/core/crypto"
)

// Stable build-local IDs. The bytes spell out the SHA-3 variant
// name with a version suffix, padded to [crypto.IDSize] with
// zeros.
var (
	id256 = crypto.ID{'s', 'h', 'a', '3', '-', '2', '5', '6', '/', 'v', '1'}
	id384 = crypto.ID{'s', 'h', 'a', '3', '-', '3', '8', '4', '/', 'v', '1'}
	id512 = crypto.ID{'s', 'h', 'a', '3', '-', '5', '1', '2', '/', 'v', '1'}
)

// Hasher256 implements [crypto.Hasher] using SHA3-256.
type Hasher256 struct{}

// Compile-time interface check.
var _ crypto.Hasher = Hasher256{}

// New256 returns a SHA3-256 [crypto.Hasher].
func New256() Hasher256 { return Hasher256{} }

// ID returns the stable build-local identifier.
func (Hasher256) ID() crypto.ID { return id256 }

// Algorithm returns [crypto.AlgSHA3_256].
func (Hasher256) Algorithm() crypto.Algorithm { return crypto.AlgSHA3_256 }

// Hash returns SHA3-256(data).
//
// # Allocation contract
//
// Zero alloc.
func (Hasher256) Hash(data []byte) crypto.Digest {
	return crypto.NewDigest256(sha3.Sum256(data))
}

// Combine returns SHA3-256(left || right).
//
// Combine panics if either digest's [crypto.Digest.Size] differs
// from [crypto.DigestSize256] — a programmer error that would
// otherwise produce a silently-wrong digest. See the package doc
// "Failure semantics" section.
//
// # Allocation contract
//
// Zero alloc on the success path.
func (Hasher256) Combine(left, right crypto.Digest) crypto.Digest {
	if left.Size() != crypto.DigestSize256 || right.Size() != crypto.DigestSize256 {
		// Precondition violation; see crypto package "Failure
		// semantics" — programmer errors panic to surface
		// silent audit-chain corruption.
		panic(fmt.Sprintf( //nolint:forbidigo
			"crypto/sha3: SHA3-256 Combine requires %d-byte digests, got left=%d right=%d",
			crypto.DigestSize256, left.Size(), right.Size()))
	}
	var buf [2 * crypto.DigestSize256]byte
	copy(buf[:crypto.DigestSize256], left.Bytes())
	copy(buf[crypto.DigestSize256:], right.Bytes())
	return crypto.NewDigest256(sha3.Sum256(buf[:]))
}

// NewStream returns a fresh streaming SHA3-256 [crypto.Stream].
func (Hasher256) NewStream() crypto.Stream {
	return &stream256{h: sha3.New256()}
}

// Hasher384 implements [crypto.Hasher] using SHA3-384.
type Hasher384 struct{}

// Compile-time interface check.
var _ crypto.Hasher = Hasher384{}

// New384 returns a SHA3-384 [crypto.Hasher].
func New384() Hasher384 { return Hasher384{} }

// ID returns the stable build-local identifier.
func (Hasher384) ID() crypto.ID { return id384 }

// Algorithm returns [crypto.AlgSHA3_384].
func (Hasher384) Algorithm() crypto.Algorithm { return crypto.AlgSHA3_384 }

// Hash returns SHA3-384(data).
//
// # Allocation contract
//
// Zero alloc.
func (Hasher384) Hash(data []byte) crypto.Digest {
	return crypto.NewDigest384(sha3.Sum384(data))
}

// Combine returns SHA3-384(left || right).
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
			"crypto/sha3: SHA3-384 Combine requires %d-byte digests, got left=%d right=%d",
			crypto.DigestSize384, left.Size(), right.Size()))
	}
	var buf [2 * crypto.DigestSize384]byte
	copy(buf[:crypto.DigestSize384], left.Bytes())
	copy(buf[crypto.DigestSize384:], right.Bytes())
	return crypto.NewDigest384(sha3.Sum384(buf[:]))
}

// NewStream returns a fresh streaming SHA3-384 [crypto.Stream].
func (Hasher384) NewStream() crypto.Stream {
	return &stream384{h: sha3.New384()}
}

// Hasher512 implements [crypto.Hasher] using SHA3-512.
type Hasher512 struct{}

// Compile-time interface check.
var _ crypto.Hasher = Hasher512{}

// New512 returns a SHA3-512 [crypto.Hasher].
func New512() Hasher512 { return Hasher512{} }

// ID returns the stable build-local identifier.
func (Hasher512) ID() crypto.ID { return id512 }

// Algorithm returns [crypto.AlgSHA3_512].
func (Hasher512) Algorithm() crypto.Algorithm { return crypto.AlgSHA3_512 }

// Hash returns SHA3-512(data).
//
// # Allocation contract
//
// Zero alloc.
func (Hasher512) Hash(data []byte) crypto.Digest {
	return crypto.NewDigest512(sha3.Sum512(data))
}

// Combine returns SHA3-512(left || right).
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
			"crypto/sha3: SHA3-512 Combine requires %d-byte digests, got left=%d right=%d",
			crypto.DigestSize512, left.Size(), right.Size()))
	}
	var buf [2 * crypto.DigestSize512]byte
	copy(buf[:crypto.DigestSize512], left.Bytes())
	copy(buf[crypto.DigestSize512:], right.Bytes())
	return crypto.NewDigest512(sha3.Sum512(buf[:]))
}

// NewStream returns a fresh streaming SHA3-512 [crypto.Stream].
func (Hasher512) NewStream() crypto.Stream {
	return &stream512{h: sha3.New512()}
}

// stream256 / stream384 / stream512 wrap stdlib SHA-3 hash.Hash
// state for [crypto.Stream].

// Each stream stores its output buffer on the receiver so
// [Stream.Sum] can pass an already-heap-allocated slice through
// the [hash.Hash] interface boundary without forcing another
// allocation.

type stream256 struct {
	h   hash.Hash
	out [crypto.DigestSize256]byte
}

// Compile-time interface check.
var _ crypto.Stream = (*stream256)(nil)

// Write feeds p into the underlying SHA3-256 hash.Hash.
// The stdlib contract guarantees no error path, so Write
// always reports (len(p), nil).
func (s *stream256) Write(p []byte) (int, error) {
	// hash.Hash.Write never returns a non-nil error per the
	// stdlib contract; we propagate the same guarantee.
	n, _ := s.h.Write(p)
	return n, nil
}

// Sum returns the SHA3-256 of every byte written so far.
// The receiver-owned output buffer keeps Sum zero-alloc
// through the hash.Hash interface boundary. State is
// preserved; further writes extend the same hash.
func (s *stream256) Sum() crypto.Digest {
	s.h.Sum(s.out[:0])
	return crypto.NewDigest256(s.out)
}

// Reset clears the underlying SHA3-256 sponge state so the
// stream can be reused for a fresh digest.
func (s *stream256) Reset() { s.h.Reset() }

type stream384 struct {
	h   hash.Hash
	out [crypto.DigestSize384]byte
}

// Compile-time interface check.
var _ crypto.Stream = (*stream384)(nil)

// Write feeds p into the underlying SHA3-384 hash.Hash.
// The stdlib contract guarantees no error path, so Write
// always reports (len(p), nil).
func (s *stream384) Write(p []byte) (int, error) {
	// hash.Hash.Write never returns a non-nil error per the
	// stdlib contract; we propagate the same guarantee.
	n, _ := s.h.Write(p)
	return n, nil
}

// Sum returns the SHA3-384 of every byte written so far.
// The receiver-owned output buffer keeps Sum zero-alloc
// through the hash.Hash interface boundary. State is
// preserved; further writes extend the same hash.
func (s *stream384) Sum() crypto.Digest {
	s.h.Sum(s.out[:0])
	return crypto.NewDigest384(s.out)
}

// Reset clears the underlying SHA3-384 sponge state so the
// stream can be reused for a fresh digest.
func (s *stream384) Reset() { s.h.Reset() }

type stream512 struct {
	h   hash.Hash
	out [crypto.DigestSize512]byte
}

// Compile-time interface check.
var _ crypto.Stream = (*stream512)(nil)

// Write feeds p into the underlying SHA3-512 hash.Hash.
// The stdlib contract guarantees no error path, so Write
// always reports (len(p), nil).
func (s *stream512) Write(p []byte) (int, error) {
	// hash.Hash.Write never returns a non-nil error per the
	// stdlib contract; we propagate the same guarantee.
	n, _ := s.h.Write(p)
	return n, nil
}

// Sum returns the SHA3-512 of every byte written so far.
// The receiver-owned output buffer keeps Sum zero-alloc
// through the hash.Hash interface boundary. State is
// preserved; further writes extend the same hash.
func (s *stream512) Sum() crypto.Digest {
	s.h.Sum(s.out[:0])
	return crypto.NewDigest512(s.out)
}

// Reset clears the underlying SHA3-512 sponge state so the
// stream can be reused for a fresh digest.
func (s *stream512) Reset() { s.h.Reset() }
