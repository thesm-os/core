// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package sha256_test

import (
	"crypto/sha256"
	"hash"
	"testing"

	"go.thesmos.sh/testkit"
	"go.thesmos.sh/testkit/bench"

	"go.thesmos.sh/core/coretest/cryptotest"
	"go.thesmos.sh/core/crypto"
	cryptosha256 "go.thesmos.sh/core/crypto/sha256"
)

// sha256ID is the canonical build-local identifier for the
// SHA-256 [crypto.Hasher] — "sha256/v1" left-aligned with zero
// padding to [crypto.IDSize].
var sha256ID = crypto.ID{'s', 'h', 'a', '2', '5', '6', '/', 'v', '1'}

// newHasher is the SUT factory shared by every testkit-driven
// entry point: contract suite, model, fuzz target, bench.
func newHasher() crypto.Hasher { return cryptosha256.New() }

// stdlibSpec describes the stdlib SHA-256 reference, used to
// build the model's reference factory.
var stdlibSpec = cryptotest.StdlibHasherSpec{
	Algorithm: crypto.AlgSHA256,
	ID:        sha256ID,
	Sum:       func(d []byte) []byte { h := sha256.Sum256(d); return h[:] },
	NewHash:   func() hash.Hash { return sha256.New() },
}

// --- testkit-driven contract layer ---

func TestSHA256HasherContract(t *testing.T) {
	t.Parallel()
	cryptotest.AssertHasherContract(t, newHasher,
		append(cryptotest.HasherContractAssertions(),
			cryptotest.HasherIDAssertion(sha256ID),
			cryptotest.HasherAlgorithmAssertion(crypto.AlgSHA256),
			cryptotest.HasherCrossStdlibAssertion(stdlibSpec.Sum),
			cryptotest.HasherCombinePanicsOnSizeMismatch(
				crypto.DigestSize256,
				crypto.NewDigest384([crypto.DigestSize384]byte{}),
			),
		)...,
	)
}

// TestSHA256HasherModel drives random byte sequences through
// both the SUT and a stdlib-backed reference, asserting byte-
// exact equivalence on every Hash and Combine call. Failures
// shrink to the minimal divergent input via rapid.
func TestSHA256HasherModel(t *testing.T) {
	t.Parallel()
	cryptotest.HasherModelTest(t, newHasher,
		cryptotest.HasherModelReference(func() crypto.Hasher {
			return cryptotest.NewStdlibHasherStub(t, stdlibSpec)
		}),
		cryptotest.HasherModelExtraActions(
			cryptotest.HasherHashAction(),
			cryptotest.HasherCombineAction(),
		),
	)
}

// FuzzSHA256HasherModel is the coverage-guided fuzz wrapper
// around the model property — same actions as
// [TestSHA256HasherModel], driven by `go test -fuzz`.
func FuzzSHA256HasherModel(f *testing.F) {
	cryptotest.HasherModelFuzz(f, newHasher,
		cryptotest.HasherModelReference(func() crypto.Hasher {
			return cryptotest.NewStdlibHasherStub(f, stdlibSpec)
		}),
		cryptotest.HasherModelExtraActions(
			cryptotest.HasherHashAction(),
			cryptotest.HasherCombineAction(),
		),
	)
}

// BenchmarkSHA256Hasher runs the standard Hasher bench contract
// — auto hot-path measurement for every method plus
// PureAllocsWithin(0) gates for the documented zero-alloc paths
// (Hash, Combine, Algorithm, ID).
func BenchmarkSHA256Hasher(b *testing.B) {
	cryptotest.BenchmarkHasherContract(b, newHasher,
		cryptotest.HasherBenchOnAlgorithm(bench.PureAllocsWithin[crypto.Hasher, crypto.Algorithm](0)),
		cryptotest.HasherBenchOnID(bench.PureAllocsWithin[crypto.Hasher, crypto.ID](0)),
		cryptotest.HasherBenchOnHash(bench.PureAllocsWithin[crypto.Hasher, crypto.Digest](0)),
		cryptotest.HasherBenchOnCombine(bench.PureAllocsWithin[crypto.Hasher, crypto.Digest](0)),
	)
}

// --- SHA-256-specific tests ---

// TestSHA256FIPSVectors locks the impl against the FIPS 180-4
// known-answer vectors. The contract suite's
// HasherCrossStdlibAssertion covers byte-equivalence with stdlib
// across a sweep of inputs, but the FIPS vectors are the
// algorithm-of-record reference: failure here means our impl
// AND stdlib have both drifted from the spec.
func TestSHA256FIPSVectors(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		wantHex string
		input   []byte
	}{
		"empty": {
			input:   []byte{},
			wantHex: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
		`"abc"`: {
			input:   []byte("abc"),
			wantHex: "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
		},
		"FIPS 180-4 §B.2 two-block": {
			input:   []byte("abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq"),
			wantHex: "248d6a61d20638b8e5c026930c3e6039a33ce45964ff2167f6ecedd419db06c1",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := cryptosha256.New().Hash(tc.input)
			want := testkit.MustDecodeHex(t, tc.wantHex)
			testkit.Equal(t, got.Bytes(), want, "Hash output must byte-match FIPS vector")
		})
	}
}

// TestZeroValueHasher locks the documented "zero value is
// usable" property of [cryptosha256.Hasher] — it's a struct{}
// type, so the zero value behaves identically to the
// constructor's return.
func TestZeroValueHasher(t *testing.T) {
	t.Parallel()
	var z cryptosha256.Hasher
	testkit.Equal(t, z.ID(), cryptosha256.New().ID(),
		"zero-value Hasher must report the same ID as a constructed one")
	testkit.Equal(t, z.Algorithm(), cryptosha256.New().Algorithm(),
		"zero-value Hasher must report the same Algorithm as a constructed one")
}
