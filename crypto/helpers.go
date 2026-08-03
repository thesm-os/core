// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package crypto

import (
	"encoding/binary"
	"fmt"
	"io"

	"go.thesmos.sh/core/pool"
)

// lenBufPool holds the 8-byte scratch [HashDomain] encodes each
// part's length prefix into. See the comment at its use site for why
// it is pooled rather than stack-allocated.
var lenBufPool = pool.NewPool(func() *[8]byte { return new([8]byte) })

// HashDomain computes h's digest over domain followed by each part,
// with every part length-prefixed:
//
//	h.Hash(domain || len(parts[0]) || parts[0] || ... || len(parts[n-1]) || parts[n-1])
//
// Lengths are big-endian uint64. The prefix is what makes distinct
// part sequences distinct: without it HashDomain(h, d, "ab", "c")
// and HashDomain(h, d, "a", "bc") produce the same digest, and
// whoever can influence two adjacent parts can move the boundary
// between them while preserving the digest. domain itself carries no
// prefix — nothing precedes it, and the framed parts that follow
// cannot be confused with it.
//
// The prefix is 64 bits rather than 32 so that every possible part
// length is representable. A 32-bit prefix would need a failure mode
// above 4 GiB, and neither available answer is acceptable: truncating
// the length silently reintroduces the ambiguity the prefix exists to
// remove, and failing turns a total function into a fallible one. The
// extra four bytes per part buy a function that cannot fail.
//
// Different domain bytes guarantee non-colliding digests for
// otherwise-identical inputs — the standard remedy against
// cross-protocol collision attacks. Caller chooses the domain
// bytes; this package ships no domain constants.
//
// Callers building records with mixed fixed- and variable-width
// fields, or with a layout that will evolve, want [Framer] instead —
// it adds a versioned domain and skips the prefix on fields whose
// width is a protocol constant.
//
// # Allocation contract
//
// Zero-allocation on the warm path: the [Stream] is borrowed
// from the [Hasher]'s pool and returned via [Stream.Close]
// before HashDomain returns. Cold-path callers (first call after
// process start, or after GC pool eviction) pay one Stream
// allocation. Consumers hashing many values with the same
// domain may still prefer to construct a [Stream] once and
// reuse it via [Stream.Reset] to save the per-call Get/Put
// overhead.
func HashDomain(h Hasher, domain []byte, parts ...[]byte) Digest {
	s := h.NewStream()
	_, _ = s.Write(domain)

	// Drawn from a pool rather than declared on the stack: it is
	// passed to Stream.Write, an interface method the compiler
	// cannot see through, so a local array would escape and cost one
	// allocation per call. *[8]byte is pointer-shaped, so Put does
	// not box it.
	lenBuf := lenBufPool.Get()
	for _, p := range parts {
		binary.BigEndian.PutUint64(lenBuf[:], uint64(len(p)))
		_, _ = s.Write(lenBuf[:])
		_, _ = s.Write(p)
	}
	lenBufPool.Put(lenBuf)

	d := s.Sum()
	s.Close()

	return d
}

// HashReader streams r through h and returns the digest of all
// bytes read. Useful for hashing inputs that don't fit in memory
// — large agentic contexts, file uploads, model artefacts.
//
// HashReader returns the underlying [io.Reader] error wrapped
// with package context if the reader fails; the partial digest
// is discarded. Read-loop allocations come from the [io.Reader]
// implementation; [HashReader] itself is zero-allocation on the
// warm path (Stream borrowed from the Hasher's pool, returned
// before HashReader returns). The [io.Copy] internal buffer is
// elided when the [io.Reader] implements [io.WriterTo] (as
// [bytes.Reader] does).
func HashReader(h Hasher, r io.Reader) (Digest, error) {
	s := h.NewStream()
	if _, err := io.Copy(s, r); err != nil {
		s.Close()
		return Digest{}, fmt.Errorf("crypto: hash reader: %w", err)
	}
	d := s.Sum()
	s.Close()
	return d, nil
}
