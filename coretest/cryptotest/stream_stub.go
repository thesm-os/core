// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package cryptotest

import (
	"hash"
	"testing"

	"go.thesmos.sh/core/crypto"
)

// stdlibStream is a [crypto.Stream] over a [hash.Hash] of the standard
// library. [NewStdlibStreamStub] passes it to [StreamStubDelegateTo], so the
// generated [StreamStub] records the calls and injects faults on top of it.
type stdlibStream struct {
	h hash.Hash
}

// Write adds p to the hash. The Write of a [hash.Hash] never returns an
// error, so Write returns nil.
func (s *stdlibStream) Write(p []byte) (int, error) {
	n, _ := s.h.Write(p)

	return n, nil
}

// Sum returns the digest of the bytes that Write added since the last Reset.
func (s *stdlibStream) Sum() crypto.Digest {
	return digestFromBytes(s.h.Sum(nil))
}

// Reset drops the bytes that Write added.
func (s *stdlibStream) Reset() { s.h.Reset() }

// Close does nothing, because the stream is not pooled.
func (*stdlibStream) Close() {}

// NewStdlibStreamStub returns a [StreamStub] that delegates to a
// [stdlibStream] over h. The stub records the calls and injects faults, and
// h computes the digests.
func NewStdlibStreamStub(tb testing.TB, h hash.Hash) *StreamStub {
	return NewStreamStub(tb, StreamStubDelegateTo(&stdlibStream{h: h}))
}
