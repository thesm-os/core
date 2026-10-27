// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package cryptotest

import (
	"hash"

	"go.dokimi.dev/assert"

	"go.thesmos.sh/core/crypto"
)

// stdlibStream is a [crypto.Stream] over a [hash.Hash] of the standard library.
// [NewStdlibStreamStub] wraps it in a [StreamStub], and the sound MAC of the
// planted defects of the suite of MAC returns it from NewStream.
type stdlibStream struct {
	h hash.Hash
}

// Write adds p to the hash. The Write of a [hash.Hash] never returns an error,
// so Write returns nil.
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

// NewStdlibStreamStub returns a [StreamStub] that delegates each call to a
// stream over h. The double records the calls, and h computes the digests.
func NewStdlibStreamStub(tb assert.TB, h hash.Hash) *StreamStub {
	return NewStreamStub(tb, StreamStubDelegateTo(&stdlibStream{h: h}))
}
