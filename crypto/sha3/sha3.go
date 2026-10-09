// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package sha3

import (
	"crypto/sha3"
	"fmt"
	"hash"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/pool"
)

// Package-level stream pools — one per algorithm. Each Hasher
// type is stateless, so a single pool per algorithm is shared
// across all callers.
var (
	stream256Pool = pool.NewPool(func() *stream256 {
		return &stream256{h: sha3.New256()}
	})
	stream384Pool = pool.NewPool(func() *stream384 {
		return &stream384{h: sha3.New384()}
	})
	stream512Pool = pool.NewPool(func() *stream512 {
		return &stream512{h: sha3.New512()}
	})
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

// HashTagged returns SHA3-256(r || data) for a unary role r.
//
// The role byte is what keeps a caller-supplied leaf from colliding
// with an interior node: without it a payload of two digest widths
// hashes to exactly what Combine produces from two digests.
//
// Panics when r is binary (high bit set). Empty data is legal and
// yields SHA3-256 of the single role byte.
//
// # Allocation contract
//
// Zero alloc on the warm path: the [crypto.Stream] is borrowed from
// the package pool and returned before HashTagged returns, and the
// role byte is sliced from static memory.
func (h Hasher256) HashTagged(r crypto.Role, data []byte) crypto.Digest {
	requireUnary(r, "SHA3-256")

	s := h.NewStream()
	_, _ = s.Write(rolePrefix(r))
	_, _ = s.Write(data)
	d := s.Sum()
	s.Close()

	return d
}

// CombineTagged returns SHA3-256(r || left || right) for a binary
// role r.
//
// The role byte is free here: SHA-3 absorbs at a 136-byte rate, so
// the 64-byte and 65-byte inputs both take one permutation.
//
// Panics when r is unary (high bit clear), when either operand is
// the zero [crypto.Digest], or when either is any other wrong width.
//
// # Allocation contract
//
// Zero alloc on the success path.
func (Hasher256) CombineTagged(r crypto.Role, left, right crypto.Digest) crypto.Digest {
	requireBinaryOperands(r, left, right, crypto.DigestSize256, "SHA3-256")

	var buf [1 + 2*crypto.DigestSize256]byte
	buf[0] = byte(r)
	copy(buf[1:], left.Bytes())
	copy(buf[1+crypto.DigestSize256:], right.Bytes())

	return crypto.NewDigest256(sha3.Sum256(buf[:]))
}

// NewStream returns a streaming SHA3-256 [crypto.Stream] drawn
// from a package-level pool; [Stream.Close] returns the
// instance for reuse. Zero-allocation on the warm path.
func (Hasher256) NewStream() crypto.Stream {
	s := stream256Pool.Get()
	s.h.Reset()
	return s
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

// HashTagged returns SHA3-384(r || data) for a unary role r.
//
// The role byte is what keeps a caller-supplied leaf from colliding
// with an interior node. Panics when r is binary (high bit set);
// empty data is legal.
//
// # Allocation contract
//
// Zero alloc on the warm path.
func (h Hasher384) HashTagged(r crypto.Role, data []byte) crypto.Digest {
	requireUnary(r, "SHA3-384")

	s := h.NewStream()
	_, _ = s.Write(rolePrefix(r))
	_, _ = s.Write(data)
	d := s.Sum()
	s.Close()

	return d
}

// CombineTagged returns SHA3-384(r || left || right) for a binary
// role r.
//
// The role byte is free here: SHA-3 absorbs at a 104-byte rate for
// this width, so the 96-byte and 97-byte inputs both take one
// permutation.
//
// Panics when r is unary (high bit clear), when either operand is
// the zero [crypto.Digest], or when either is any other wrong width.
//
// # Allocation contract
//
// Zero alloc on the success path.
func (Hasher384) CombineTagged(r crypto.Role, left, right crypto.Digest) crypto.Digest {
	requireBinaryOperands(r, left, right, crypto.DigestSize384, "SHA3-384")

	var buf [1 + 2*crypto.DigestSize384]byte
	buf[0] = byte(r)
	copy(buf[1:], left.Bytes())
	copy(buf[1+crypto.DigestSize384:], right.Bytes())

	return crypto.NewDigest384(sha3.Sum384(buf[:]))
}

// NewStream returns a streaming SHA3-384 [crypto.Stream] drawn
// from a package-level pool; [Stream.Close] returns the
// instance for reuse. Zero-allocation on the warm path.
func (Hasher384) NewStream() crypto.Stream {
	s := stream384Pool.Get()
	s.h.Reset()
	return s
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

// HashTagged returns SHA3-512(r || data) for a unary role r.
//
// The role byte is what keeps a caller-supplied leaf from colliding
// with an interior node. Panics when r is binary (high bit set);
// empty data is legal.
//
// # Allocation contract
//
// Zero alloc on the warm path.
func (h Hasher512) HashTagged(r crypto.Role, data []byte) crypto.Digest {
	requireUnary(r, "SHA3-512")

	s := h.NewStream()
	_, _ = s.Write(rolePrefix(r))
	_, _ = s.Write(data)
	d := s.Sum()
	s.Close()

	return d
}

// CombineTagged returns SHA3-512(r || left || right) for a binary
// role r.
//
// The role byte is free here: SHA-3 absorbs at a 72-byte rate for
// this width, so the 128-byte and 129-byte inputs both take two
// permutations.
//
// Panics when r is unary (high bit clear), when either operand is
// the zero [crypto.Digest], or when either is any other wrong width.
//
// # Allocation contract
//
// Zero alloc on the success path.
func (Hasher512) CombineTagged(r crypto.Role, left, right crypto.Digest) crypto.Digest {
	requireBinaryOperands(r, left, right, crypto.DigestSize512, "SHA3-512")

	var buf [1 + 2*crypto.DigestSize512]byte
	buf[0] = byte(r)
	copy(buf[1:], left.Bytes())
	copy(buf[1+crypto.DigestSize512:], right.Bytes())

	return crypto.NewDigest512(sha3.Sum512(buf[:]))
}

// NewStream returns a streaming SHA3-512 [crypto.Stream] drawn
// from a package-level pool; [Stream.Close] returns the
// instance for reuse. Zero-allocation on the warm path.
func (Hasher512) NewStream() crypto.Stream {
	s := stream512Pool.Get()
	s.h.Reset()
	return s
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

// Close returns the stream to the package-level pool. The
// stream MUST NOT be used after Close.
func (s *stream256) Close() { stream256Pool.Put(s) }

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

// Close returns the stream to the package-level pool. The
// stream MUST NOT be used after Close.
func (s *stream384) Close() { stream384Pool.Put(s) }

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

// Close returns the stream to the package-level pool. The
// stream MUST NOT be used after Close.
func (s *stream512) Close() { stream512Pool.Put(s) }

// sized reports whether d is exactly the given digest width.
//
// Unlike combinable it refuses the zero [crypto.Digest]. The tagged
// operations have no genesis sentinel to admit: a chain's first link
// is a unary role over one operand, so a zero operand reaching
// CombineTagged is a programmer error like any other wrong width.
func sized(d crypto.Digest, size int) bool {
	return d.Size() == size
}

// roleBytes is the identity table of every one-byte [crypto.Role]
// value, so HashTagged can hand the role to a [crypto.Stream] without
// allocating.
//
// A local array sliced into Stream.Write would escape — the same
// interface boundary that makes [crypto.HashDomain] draw its length
// buffer from a pool. A slice of this table is static memory
// instead: nothing escapes, no pool, no atomics, and the content of
// a role byte never varies the way a length prefix does.
//
// Written once at initialisation and never mutated.
var roleBytes = func() [256]byte {
	var t [256]byte
	for i := range t {
		t[i] = byte(i)
	}

	return t
}()

// rolePrefix returns the one-byte prefix for r, backed by static
// memory so it does not escape through [crypto.Stream.Write].
//
// The int conversion is load-bearing: r+1 evaluated in Role's own
// type wraps to zero at 0xFF and would slice backwards.
func rolePrefix(r crypto.Role) []byte {
	i := int(r)

	return roleBytes[i : i+1]
}

// requireUnary panics unless r is a unary role. alg names the
// algorithm so a mixed-width package's diagnostic says which hasher
// rejected the call.
func requireUnary(r crypto.Role, alg string) {
	if !r.IsUnary() {
		// Precondition violation; see crypto package "Failure
		// semantics" — programmer errors panic to surface silent
		// audit-chain corruption.
		panic(fmt.Sprintf( //nolint:forbidigo // a role or a digest of the wrong kind is a programmer error
			"crypto/sha3: %s HashTagged requires a unary role (high bit clear), got %#02x",
			alg, byte(r),
		))
	}
}

// requireBinaryOperands panics unless r is a binary role and both
// operands are exactly size bytes.
//
// The zero [crypto.Digest] is rejected with its own diagnostic ahead
// of the width check: a caller who reaches for the retired genesis
// sentinel needs to be told that, not sent hunting for a width bug.
func requireBinaryOperands(r crypto.Role, left, right crypto.Digest, size int, alg string) {
	if !r.IsBinary() {
		panic(fmt.Sprintf( //nolint:forbidigo // a role or a digest of the wrong kind is a programmer error
			"crypto/sha3: %s CombineTagged requires a binary role (high bit set), got %#02x",
			alg, byte(r),
		))
	}
	if left.IsZero() || right.IsZero() {
		panic(fmt.Sprintf( //nolint:forbidigo // a role or a digest of the wrong kind is a programmer error
			"crypto/sha3: %s CombineTagged refuses the zero Digest; the genesis sentinel "+
				"is retired — a chain's first link is a unary role over one operand",
			alg,
		))
	}
	if !sized(left, size) || !sized(right, size) {
		panic(fmt.Sprintf( //nolint:forbidigo // a role or a digest of the wrong kind is a programmer error
			"crypto/sha3: %s CombineTagged requires %d-byte digests, got left=%d right=%d",
			alg, size, left.Size(), right.Size(),
		))
	}
}
