// Copyright ThesmOS B.V. 2026
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
// [Hasher.CombineTagged] are zero-alloc; [Hasher.HashTagged] is
// zero-alloc on the warm path. [Hasher.NewStream] allocates the
// underlying [hash.Hash] once.
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

// sized reports whether d is exactly this hasher's output width.
//
// The zero [crypto.Digest] is not exempt. The tagged operations have
// no genesis sentinel to admit: a chain's first link is a unary role
// over one operand, so a zero operand reaching CombineTagged is a
// programmer error like any other wrong width.
func sized(d crypto.Digest) bool {
	return d.Size() == crypto.DigestSize256
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

// HashTagged returns SHA-256(r || data) for a unary role r.
//
// The role byte is what keeps a caller-supplied leaf from colliding
// with an interior node: without it a 64-byte payload hashes to
// exactly what Combine produces from two digests.
//
// Panics when r is binary (high bit set) — admitting one here would
// let a crafted payload share bytes with a CombineTagged result
// under the same role, which is the forgery the arity split exists
// to make unconstructible. Empty data is legal and yields
// SHA-256 of the single role byte.
//
// # Allocation contract
//
// Zero alloc on the warm path: the [crypto.Stream] is borrowed from
// the package pool and returned before HashTagged returns, and the
// role byte is sliced from static memory. A cold-path caller (first
// call after process start, or after GC pool eviction) pays the one
// Stream allocation [Hasher.NewStream] documents.
func (h Hasher) HashTagged(r crypto.Role, data []byte) crypto.Digest {
	if !r.IsUnary() {
		// Precondition violation; see crypto package "Failure
		// semantics" — programmer errors panic to surface silent
		// audit-chain corruption.
		panic(fmt.Sprintf( //nolint:forbidigo // a role or a digest of the wrong kind is a programmer error
			"crypto/sha256: HashTagged requires a unary role (high bit clear), got %#02x",
			byte(r),
		))
	}

	s := h.NewStream()
	_, _ = s.Write(rolePrefix(r))
	_, _ = s.Write(data)
	d := s.Sum()
	s.Close()

	return d
}

// CombineTagged returns SHA-256(r || left || right) for a binary
// role r.
//
// The 65-byte input costs no more than Combine's 64: SHA-256 appends
// a 0x80 byte and an eight-byte length, so both pad to 128 and
// compress twice. The role byte is free on this path.
//
// Panics when r is unary (high bit clear), when either operand is
// the zero [crypto.Digest] — with its own diagnostic, because a
// caller who reaches for the retired genesis sentinel needs to be
// told that rather than sent hunting for a width bug — and when
// either operand is any other wrong width.
//
// # Allocation contract
//
// Zero alloc on the success path — the 65-byte concat lives on the
// stack and [crypto/sha256.Sum256] does not escape its argument
// (concrete function, not the [hash.Hash] interface).
func (Hasher) CombineTagged(r crypto.Role, left, right crypto.Digest) crypto.Digest {
	if !r.IsBinary() {
		panic(fmt.Sprintf( //nolint:forbidigo // a role or a digest of the wrong kind is a programmer error
			"crypto/sha256: CombineTagged requires a binary role (high bit set), got %#02x",
			byte(r),
		))
	}
	if left.IsZero() || right.IsZero() {
		panic( //nolint:forbidigo // a role or a digest of the wrong kind is a programmer error
			"crypto/sha256: CombineTagged refuses the zero Digest; " +
				"the genesis sentinel is retired — a chain's first link is " +
				"a unary role over one operand",
		)
	}
	if !sized(left) || !sized(right) {
		panic(fmt.Sprintf( //nolint:forbidigo // a role or a digest of the wrong kind is a programmer error
			"crypto/sha256: CombineTagged requires %d-byte digests, got left=%d right=%d",
			crypto.DigestSize256, left.Size(), right.Size(),
		))
	}

	var buf [1 + 2*crypto.DigestSize256]byte
	buf[0] = byte(r)
	copy(buf[1:], left.Bytes())
	copy(buf[1+crypto.DigestSize256:], right.Bytes())

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
