// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package crypto

import (
	"fmt"
	"io"
)

// HashDomain computes h's digest over the concatenation of
// domain and parts:
//
//	h.Hash(domain || parts[0] || parts[1] || ... || parts[n-1])
//
// Different domain bytes guarantee non-colliding digests for
// otherwise-identical inputs — the standard remedy against
// cross-protocol collision attacks. Caller chooses the domain
// bytes; this package ships no domain constants.
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
	for _, p := range parts {
		_, _ = s.Write(p)
	}
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
