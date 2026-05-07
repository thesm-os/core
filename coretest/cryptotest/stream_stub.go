// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package cryptotest

import (
	"hash"
	"testing"

	"go.thesmos.sh/core/crypto"
)

// stdlibStream is a [crypto.Stream] companion backed by a stdlib
// [hash.Hash]. Used as the inner delegate for
// [NewStdlibStreamStub] — the generated [StreamStub] wraps it
// via [StreamStubDelegateTo] to keep the recorder / fault-
// injector layer on top.
type stdlibStream struct {
	h hash.Hash
}

func (s *stdlibStream) Write(p []byte) (int, error) { return s.h.Write(p) }

func (s *stdlibStream) Sum() crypto.Digest {
	return digestFromBytes(s.h.Sum(nil))
}

func (s *stdlibStream) Reset() { s.h.Reset() }

func (*stdlibStream) Close() {}

// NewStdlibStreamStub returns a [StreamStub] delegating to a
// stdlib-backed companion built around h. The stub layer adds
// recording / fault-injection hooks; the inner [hash.Hash]
// supplies real behaviour.
func NewStdlibStreamStub(tb testing.TB, h hash.Hash) *StreamStub {
	return NewStreamStub(tb, StreamStubDelegateTo(&stdlibStream{h: h}))
}
