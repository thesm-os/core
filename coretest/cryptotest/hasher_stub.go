// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package cryptotest

import (
	"fmt"
	"testing"

	"go.thesmos.sh/core/crypto"
)

// samplePayload is the payload that [SampleBytes] returns to every
// caller. No caller writes to it.
var samplePayload = []byte("sample payload")

// stdlibHasher is a [crypto.Hasher] companion backed by stdlib.
// Used as the inner delegate for [NewStdlibHasherStub] — the
// generated [HasherStub] wraps it via [HasherStubDelegateTo] to
// keep the recorder / fault-injector layer on top.
type stdlibHasher struct {
	spec StdlibHasherSpec
	tb   testing.TB
}

func (h *stdlibHasher) Algorithm() crypto.Algorithm { return h.spec.Algorithm }

func (h *stdlibHasher) ID() crypto.ID { return h.spec.ID }

func (h *stdlibHasher) Hash(data []byte) crypto.Digest {
	return digestFromBytes(h.spec.Sum(data))
}

func (h *stdlibHasher) HashTagged(r crypto.Role, data []byte) crypto.Digest {
	if !r.IsUnary() {
		panic(fmt.Sprintf( //nolint:forbidigo
			"cryptotest: HashTagged requires a unary role (high bit clear), got %#02x", byte(r),
		))
	}
	tagged := make([]byte, 0, 1+len(data))
	tagged = append(tagged, byte(r))
	tagged = append(tagged, data...)

	return digestFromBytes(h.spec.Sum(tagged))
}

func (h *stdlibHasher) CombineTagged(r crypto.Role, left, right crypto.Digest) crypto.Digest {
	if !r.IsBinary() {
		panic(fmt.Sprintf( //nolint:forbidigo
			"cryptotest: CombineTagged requires a binary role (high bit set), got %#02x", byte(r),
		))
	}
	if left.IsZero() || right.IsZero() {
		panic( //nolint:forbidigo
			"cryptotest: CombineTagged refuses the zero Digest; the genesis sentinel is retired",
		)
	}
	concat := make([]byte, 0, 1+left.Size()+right.Size())
	concat = append(concat, byte(r))
	concat = append(concat, left.Bytes()...)
	concat = append(concat, right.Bytes()...)

	return digestFromBytes(h.spec.Sum(concat))
}

func (h *stdlibHasher) NewStream() crypto.Stream {
	return NewStdlibStreamStub(h.tb, h.spec.NewHash())
}

// NewStdlibHasherStub returns a [HasherStub] delegating to a
// stdlib-backed companion described by spec. The stub layer adds
// recording / fault-injection hooks; the inner companion
// supplies real behaviour from the Go stdlib.
func NewStdlibHasherStub(tb testing.TB, spec StdlibHasherSpec) *HasherStub {
	return NewHasherStub(tb,
		HasherStubDelegateTo(&stdlibHasher{spec: spec, tb: tb}),
	)
}

// SampleDigest returns a [crypto.Digest] of the natural size for
// h — the digest of empty input. Used as the SUT-aware sample
// builder for testkit's `//testkit:sample` directive: the same
// function works for every Hasher implementation regardless of
// digest size (sha256 → 32 B, sha384 → 48 B, sha512 / sha3-512
// → 64 B).
func SampleDigest(h crypto.Hasher) crypto.Digest {
	return h.Hash(nil)
}

// SampleUnaryRole returns a [crypto.Role] from the unary half of the
// space — one [crypto.Hasher.HashTagged] accepts and
// [crypto.Hasher.CombineTagged] refuses. The SUT-aware sample builder
// for testkit's `//testkit:sample` directive, which cannot synthesize
// a uint8 that satisfies the arity precondition.
func SampleUnaryRole(crypto.Hasher) crypto.Role { return 0x01 }

// SampleBinaryRole returns a [crypto.Role] from the binary half of
// the space — one [crypto.Hasher.CombineTagged] accepts and
// [crypto.Hasher.HashTagged] refuses.
func SampleBinaryRole(crypto.Hasher) crypto.Role { return 0x81 }

// SampleBytes returns a non-empty payload for the generated stub and
// benchmark surfaces. Content is irrelevant; only that it is stable
// across calls, so a generated determinism assertion compares like
// with like. It returns the one slice of samplePayload, so a generated
// benchmark that passes it to a method whose argument escapes, as
// [crypto.Hasher.HashTagged] passes data to [crypto.Stream.Write],
// measures no allocation of its own. The caller must not modify it.
func SampleBytes(crypto.Hasher) []byte { return samplePayload }

// digestFromBytes wraps a byte slice from a stdlib hash output
// in a [crypto.Digest] of the matching size. Bridges the stdlib
// `[]byte`-returning helpers (`sha256.Sum256` etc.) into the
// fixed-size [crypto.Digest] shape the companion requires.
//
// Panics on unexpected output lengths — callers must match the
// byte slice to the algorithm's natural digest size (32 / 48 /
// 64 bytes).
func digestFromBytes(b []byte) crypto.Digest {
	switch len(b) {
	case crypto.DigestSize256:
		var d [crypto.DigestSize256]byte
		copy(d[:], b)
		return crypto.NewDigest256(d)
	case crypto.DigestSize384:
		var d [crypto.DigestSize384]byte
		copy(d[:], b)
		return crypto.NewDigest384(d)
	case crypto.DigestSize512:
		var d [crypto.DigestSize512]byte
		copy(d[:], b)
		return crypto.NewDigest512(d)
	default:
		panic(fmt.Sprintf("cryptotest: unsupported digest size %d", len(b)))
	}
}
