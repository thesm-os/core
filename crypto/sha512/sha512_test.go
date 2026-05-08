// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package sha512_test

import (
	"crypto/sha512"
	"hash"
	"testing"

	"go.thesmos.sh/testkit"
	"go.thesmos.sh/testkit/bench"

	"go.thesmos.sh/core/coretest/cryptotest"
	"go.thesmos.sh/core/crypto"
	cryptosha512 "go.thesmos.sh/core/crypto/sha512"
)

// Canonical build-local IDs for SHA-384 and SHA-512.
var (
	sha384ID = crypto.ID{'s', 'h', 'a', '3', '8', '4', '/', 'v', '1'}
	sha512ID = crypto.ID{'s', 'h', 'a', '5', '1', '2', '/', 'v', '1'}
)

func newSHA384() crypto.Hasher { return cryptosha512.New384() }
func newSHA512() crypto.Hasher { return cryptosha512.New512() }

var sha384Spec = cryptotest.StdlibHasherSpec{
	Algorithm: crypto.AlgSHA384,
	ID:        sha384ID,
	Sum:       func(d []byte) []byte { h := sha512.Sum384(d); return h[:] },
	NewHash:   func() hash.Hash { return sha512.New384() },
}

var sha512Spec = cryptotest.StdlibHasherSpec{
	Algorithm: crypto.AlgSHA512,
	ID:        sha512ID,
	Sum:       func(d []byte) []byte { h := sha512.Sum512(d); return h[:] },
	NewHash:   func() hash.Hash { return sha512.New() },
}

// --- testkit-driven contract layer (SHA-384) ---

func TestSHA384HasherContract(t *testing.T) {
	t.Parallel()
	cryptotest.AssertHasherContract(t, newSHA384,
		append(cryptotest.HasherContractAssertions(),
			cryptotest.HasherIDAssertion(sha384ID),
			cryptotest.HasherAlgorithmAssertion(crypto.AlgSHA384),
			cryptotest.HasherCrossStdlibAssertion(sha384Spec.Sum),
			cryptotest.HasherCombinePanicsOnSizeMismatch(
				crypto.DigestSize384,
				crypto.NewDigest256([crypto.DigestSize256]byte{}),
			),
		)...,
	)
}

func TestSHA384HasherModel(t *testing.T) {
	t.Parallel()
	cryptotest.HasherModelTest(t, newSHA384,
		cryptotest.HasherModelReference(func() crypto.Hasher {
			return cryptotest.NewStdlibHasherStub(t, sha384Spec)
		}),
		cryptotest.HasherModelExtraActions(
			cryptotest.HasherHashAction(),
			cryptotest.HasherCombineAction(),
		),
	)
}

func FuzzSHA384HasherModel(f *testing.F) {
	cryptotest.HasherModelFuzz(f, newSHA384,
		cryptotest.HasherModelReference(func() crypto.Hasher {
			return cryptotest.NewStdlibHasherStub(f, sha384Spec)
		}),
		cryptotest.HasherModelExtraActions(
			cryptotest.HasherHashAction(),
			cryptotest.HasherCombineAction(),
		),
	)
}

func BenchmarkSHA384Hasher(b *testing.B) {
	cryptotest.BenchmarkHasherContract(b, newSHA384,
		cryptotest.HasherBenchOnAlgorithm(bench.PureAllocsWithin[crypto.Hasher, crypto.Algorithm](0)),
		cryptotest.HasherBenchOnID(bench.PureAllocsWithin[crypto.Hasher, crypto.ID](0)),
		cryptotest.HasherBenchOnHash(bench.PureAllocsWithin[crypto.Hasher, crypto.Digest](0)),
		cryptotest.HasherBenchOnCombine(bench.PureAllocsWithin[crypto.Hasher, crypto.Digest](0)),
	)
}

// --- testkit-driven contract layer (SHA-512) ---

func TestSHA512HasherContract(t *testing.T) {
	t.Parallel()
	cryptotest.AssertHasherContract(t, newSHA512,
		append(cryptotest.HasherContractAssertions(),
			cryptotest.HasherIDAssertion(sha512ID),
			cryptotest.HasherAlgorithmAssertion(crypto.AlgSHA512),
			cryptotest.HasherCrossStdlibAssertion(sha512Spec.Sum),
			cryptotest.HasherCombinePanicsOnSizeMismatch(
				crypto.DigestSize512,
				crypto.NewDigest256([crypto.DigestSize256]byte{}),
			),
		)...,
	)
}

func TestSHA512HasherModel(t *testing.T) {
	t.Parallel()
	cryptotest.HasherModelTest(t, newSHA512,
		cryptotest.HasherModelReference(func() crypto.Hasher {
			return cryptotest.NewStdlibHasherStub(t, sha512Spec)
		}),
		cryptotest.HasherModelExtraActions(
			cryptotest.HasherHashAction(),
			cryptotest.HasherCombineAction(),
		),
	)
}

func FuzzSHA512HasherModel(f *testing.F) {
	cryptotest.HasherModelFuzz(f, newSHA512,
		cryptotest.HasherModelReference(func() crypto.Hasher {
			return cryptotest.NewStdlibHasherStub(f, sha512Spec)
		}),
		cryptotest.HasherModelExtraActions(
			cryptotest.HasherHashAction(),
			cryptotest.HasherCombineAction(),
		),
	)
}

func BenchmarkSHA512Hasher(b *testing.B) {
	cryptotest.BenchmarkHasherContract(b, newSHA512,
		cryptotest.HasherBenchOnAlgorithm(bench.PureAllocsWithin[crypto.Hasher, crypto.Algorithm](0)),
		cryptotest.HasherBenchOnID(bench.PureAllocsWithin[crypto.Hasher, crypto.ID](0)),
		cryptotest.HasherBenchOnHash(bench.PureAllocsWithin[crypto.Hasher, crypto.Digest](0)),
		cryptotest.HasherBenchOnCombine(bench.PureAllocsWithin[crypto.Hasher, crypto.Digest](0)),
	)
}

// --- impl-specific FIPS known-answer vectors ---

// TestSHA384FIPSVectors locks the SHA-384 impl against FIPS
// 180-4 §C.1 / §C.2 vectors.
func TestSHA384FIPSVectors(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		wantHex string
		input   []byte
	}{
		"empty": {
			input: []byte{},
			wantHex: "38b060a751ac96384cd9327eb1b1e36a21fdb71114be07434c0cc7bf63f6e1da" +
				"274edebfe76f65fbd51ad2f14898b95b",
		},
		`"abc"`: {
			input: []byte("abc"),
			wantHex: "cb00753f45a35e8bb5a03d699ac65007272c32ab0eded1631a8b605a43ff5bed" +
				"8086072ba1e7cc2358baeca134c825a7",
		},
		"FIPS 180-4 §C.2 two-block": {
			input: []byte("abcdefghbcdefghicdefghijdefghijkefghijklfghijklmghijklmnhijklmno" +
				"ijklmnopjklmnopqklmnopqrlmnopqrsmnopqrstnopqrstu"),
			wantHex: "09330c33f71147e83d192fc782cd1b4753111b173b3b05d22fa08086e3b0f712" +
				"fcc7c71a557e2db966c3e9fa91746039",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := cryptosha512.New384().Hash(tc.input)
			want := testkit.MustDecodeHex(t, tc.wantHex)
			testkit.Equal(t, got.Bytes(), want, "Hash output must byte-match FIPS vector")
		})
	}
}

// TestSHA512FIPSVectors locks the SHA-512 impl against FIPS
// 180-4 §C.3 / §C.4 vectors.
func TestSHA512FIPSVectors(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		wantHex string
		input   []byte
	}{
		"empty": {
			input: []byte{},
			wantHex: "cf83e1357eefb8bdf1542850d66d8007d620e4050b5715dc83f4a921d36ce9ce" +
				"47d0d13c5d85f2b0ff8318d2877eec2f63b931bd47417a81a538327af927da3e",
		},
		`"abc"`: {
			input: []byte("abc"),
			wantHex: "ddaf35a193617abacc417349ae20413112e6fa4e89a97ea20a9eeee64b55d39a" +
				"2192992a274fc1a836ba3c23a3feebbd454d4423643ce80e2a9ac94fa54ca49f",
		},
		"FIPS 180-4 §C.4 two-block": {
			input: []byte("abcdefghbcdefghicdefghijdefghijkefghijklfghijklmghijklmnhijklmno" +
				"ijklmnopjklmnopqklmnopqrlmnopqrsmnopqrstnopqrstu"),
			wantHex: "8e959b75dae313da8cf4f72814fc143f8f7779c6eb9f7fa17299aeadb6889018" +
				"501d289e4900f7e4331b99dec4b5433ac7d329eeb6dd26545e96e55b874be909",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := cryptosha512.New512().Hash(tc.input)
			want := testkit.MustDecodeHex(t, tc.wantHex)
			testkit.Equal(t, got.Bytes(), want, "Hash output must byte-match FIPS vector")
		})
	}
}
