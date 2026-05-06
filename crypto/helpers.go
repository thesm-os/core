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
// Allocates one [Stream] per call. Consumers on a hot path that
// hash many values with the same domain should construct a
// [Stream] once and reuse it via [Stream.Reset].
func HashDomain(h Hasher, domain []byte, parts ...[]byte) Digest {
	s := h.NewStream()
	_, _ = s.Write(domain)
	for _, p := range parts {
		_, _ = s.Write(p)
	}
	return s.Sum()
}

// HashReader streams r through h and returns the digest of all
// bytes read. Useful for hashing inputs that don't fit in memory
// — large agentic contexts, file uploads, model artefacts.
//
// HashReader returns the underlying [io.Reader] error wrapped
// with package context if the reader fails; the partial digest
// is discarded. Read-loop allocations come from the [io.Reader]
// implementation; [HashReader] itself allocates one [Stream]
// plus the [io.Copy] internal buffer.
func HashReader(h Hasher, r io.Reader) (Digest, error) {
	s := h.NewStream()
	_, err := io.Copy(s, r)
	if err != nil {
		return Digest{}, fmt.Errorf("crypto: hash reader: %w", err)
	}
	return s.Sum(), nil
}
