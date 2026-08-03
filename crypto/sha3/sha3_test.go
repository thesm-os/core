// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package sha3_test

import (
	"crypto/sha3"
	"hash"
	"testing"

	"go.thesmos.sh/testkit"
	"go.thesmos.sh/testkit/bench"

	"go.thesmos.sh/core/coretest/cryptotest"
	"go.thesmos.sh/core/crypto"
	cryptosha3 "go.thesmos.sh/core/crypto/sha3"
)

// Canonical build-local IDs for SHA3-256, SHA3-384, SHA3-512.
var (
	sha3_256ID = crypto.ID{'s', 'h', 'a', '3', '-', '2', '5', '6', '/', 'v', '1'}
	sha3_384ID = crypto.ID{'s', 'h', 'a', '3', '-', '3', '8', '4', '/', 'v', '1'}
	sha3_512ID = crypto.ID{'s', 'h', 'a', '3', '-', '5', '1', '2', '/', 'v', '1'}
)

func newSHA3_256() crypto.Hasher { return cryptosha3.New256() }
func newSHA3_384() crypto.Hasher { return cryptosha3.New384() }
func newSHA3_512() crypto.Hasher { return cryptosha3.New512() }

var sha3_256Spec = cryptotest.StdlibHasherSpec{
	Algorithm: crypto.AlgSHA3_256,
	ID:        sha3_256ID,
	Sum:       func(d []byte) []byte { h := sha3.Sum256(d); return h[:] },
	NewHash:   func() hash.Hash { return sha3.New256() },
}

var sha3_384Spec = cryptotest.StdlibHasherSpec{
	Algorithm: crypto.AlgSHA3_384,
	ID:        sha3_384ID,
	Sum:       func(d []byte) []byte { h := sha3.Sum384(d); return h[:] },
	NewHash:   func() hash.Hash { return sha3.New384() },
}

var sha3_512Spec = cryptotest.StdlibHasherSpec{
	Algorithm: crypto.AlgSHA3_512,
	ID:        sha3_512ID,
	Sum:       func(d []byte) []byte { h := sha3.Sum512(d); return h[:] },
	NewHash:   func() hash.Hash { return sha3.New512() },
}

// --- testkit-driven contract layer (SHA3-256) ---

func TestSHA3_256HasherContract(t *testing.T) {
	t.Parallel()
	cryptotest.AssertHasherContract(t, newSHA3_256,
		append(cryptotest.HasherContractAssertions(),
			cryptotest.HasherIDAssertion(sha3_256ID),
			cryptotest.HasherAlgorithmAssertion(crypto.AlgSHA3_256),
			cryptotest.HasherCrossStdlibAssertion(sha3_256Spec.Sum),
			cryptotest.HasherCombinePanicsOnSizeMismatch(
				crypto.DigestSize256,
				crypto.NewDigest384([crypto.DigestSize384]byte{}),
			),
			cryptotest.HasherCombineAdmitsZeroDigest(
				crypto.NewDigest256([crypto.DigestSize256]byte{}),
			),
		)...,
	)
}

func TestSHA3_256HasherModel(t *testing.T) {
	t.Parallel()
	cryptotest.HasherModelTest(t, newSHA3_256,
		cryptotest.HasherModelReference(func() crypto.Hasher {
			return cryptotest.NewStdlibHasherStub(t, sha3_256Spec)
		}),
		cryptotest.HasherModelExtraActions(
			cryptotest.HasherHashAction(),
			cryptotest.HasherCombineAction(),
		),
	)
}

func FuzzSHA3_256HasherModel(f *testing.F) {
	cryptotest.HasherModelFuzz(f, newSHA3_256,
		cryptotest.HasherModelReference(func() crypto.Hasher {
			return cryptotest.NewStdlibHasherStub(f, sha3_256Spec)
		}),
		cryptotest.HasherModelExtraActions(
			cryptotest.HasherHashAction(),
			cryptotest.HasherCombineAction(),
		),
	)
}

func BenchmarkSHA3_256Hasher(b *testing.B) {
	cryptotest.BenchmarkHasherContract(b, newSHA3_256,
		cryptotest.HasherBenchOnAlgorithm(bench.PureAllocsWithin[crypto.Hasher, crypto.Algorithm](0)),
		cryptotest.HasherBenchOnID(bench.PureAllocsWithin[crypto.Hasher, crypto.ID](0)),
		cryptotest.HasherBenchOnHash(bench.PureAllocsWithin[crypto.Hasher, crypto.Digest](0)),
		cryptotest.HasherBenchOnCombine(bench.PureAllocsWithin[crypto.Hasher, crypto.Digest](0)),
	)
}

// --- testkit-driven contract layer (SHA3-384) ---

func TestSHA3_384HasherContract(t *testing.T) {
	t.Parallel()
	cryptotest.AssertHasherContract(t, newSHA3_384,
		append(cryptotest.HasherContractAssertions(),
			cryptotest.HasherIDAssertion(sha3_384ID),
			cryptotest.HasherAlgorithmAssertion(crypto.AlgSHA3_384),
			cryptotest.HasherCrossStdlibAssertion(sha3_384Spec.Sum),
			cryptotest.HasherCombinePanicsOnSizeMismatch(
				crypto.DigestSize384,
				crypto.NewDigest256([crypto.DigestSize256]byte{}),
			),
			cryptotest.HasherCombineAdmitsZeroDigest(
				crypto.NewDigest384([crypto.DigestSize384]byte{}),
			),
		)...,
	)
}

func TestSHA3_384HasherModel(t *testing.T) {
	t.Parallel()
	cryptotest.HasherModelTest(t, newSHA3_384,
		cryptotest.HasherModelReference(func() crypto.Hasher {
			return cryptotest.NewStdlibHasherStub(t, sha3_384Spec)
		}),
		cryptotest.HasherModelExtraActions(
			cryptotest.HasherHashAction(),
			cryptotest.HasherCombineAction(),
		),
	)
}

func FuzzSHA3_384HasherModel(f *testing.F) {
	cryptotest.HasherModelFuzz(f, newSHA3_384,
		cryptotest.HasherModelReference(func() crypto.Hasher {
			return cryptotest.NewStdlibHasherStub(f, sha3_384Spec)
		}),
		cryptotest.HasherModelExtraActions(
			cryptotest.HasherHashAction(),
			cryptotest.HasherCombineAction(),
		),
	)
}

func BenchmarkSHA3_384Hasher(b *testing.B) {
	cryptotest.BenchmarkHasherContract(b, newSHA3_384,
		cryptotest.HasherBenchOnAlgorithm(bench.PureAllocsWithin[crypto.Hasher, crypto.Algorithm](0)),
		cryptotest.HasherBenchOnID(bench.PureAllocsWithin[crypto.Hasher, crypto.ID](0)),
		cryptotest.HasherBenchOnHash(bench.PureAllocsWithin[crypto.Hasher, crypto.Digest](0)),
		cryptotest.HasherBenchOnCombine(bench.PureAllocsWithin[crypto.Hasher, crypto.Digest](0)),
	)
}

// --- testkit-driven contract layer (SHA3-512) ---

func TestSHA3_512HasherContract(t *testing.T) {
	t.Parallel()
	cryptotest.AssertHasherContract(t, newSHA3_512,
		append(cryptotest.HasherContractAssertions(),
			cryptotest.HasherIDAssertion(sha3_512ID),
			cryptotest.HasherAlgorithmAssertion(crypto.AlgSHA3_512),
			cryptotest.HasherCrossStdlibAssertion(sha3_512Spec.Sum),
			cryptotest.HasherCombinePanicsOnSizeMismatch(
				crypto.DigestSize512,
				crypto.NewDigest256([crypto.DigestSize256]byte{}),
			),
			cryptotest.HasherCombineAdmitsZeroDigest(
				crypto.NewDigest512([crypto.DigestSize512]byte{}),
			),
		)...,
	)
}

func TestSHA3_512HasherModel(t *testing.T) {
	t.Parallel()
	cryptotest.HasherModelTest(t, newSHA3_512,
		cryptotest.HasherModelReference(func() crypto.Hasher {
			return cryptotest.NewStdlibHasherStub(t, sha3_512Spec)
		}),
		cryptotest.HasherModelExtraActions(
			cryptotest.HasherHashAction(),
			cryptotest.HasherCombineAction(),
		),
	)
}

func FuzzSHA3_512HasherModel(f *testing.F) {
	cryptotest.HasherModelFuzz(f, newSHA3_512,
		cryptotest.HasherModelReference(func() crypto.Hasher {
			return cryptotest.NewStdlibHasherStub(f, sha3_512Spec)
		}),
		cryptotest.HasherModelExtraActions(
			cryptotest.HasherHashAction(),
			cryptotest.HasherCombineAction(),
		),
	)
}

func BenchmarkSHA3_512Hasher(b *testing.B) {
	cryptotest.BenchmarkHasherContract(b, newSHA3_512,
		cryptotest.HasherBenchOnAlgorithm(bench.PureAllocsWithin[crypto.Hasher, crypto.Algorithm](0)),
		cryptotest.HasherBenchOnID(bench.PureAllocsWithin[crypto.Hasher, crypto.ID](0)),
		cryptotest.HasherBenchOnHash(bench.PureAllocsWithin[crypto.Hasher, crypto.Digest](0)),
		cryptotest.HasherBenchOnCombine(bench.PureAllocsWithin[crypto.Hasher, crypto.Digest](0)),
	)
}

// --- impl-specific FIPS 202 / NIST CAVP known-answer vectors ---

const (
	emptyName = "empty"
	abcName   = `"abc"`
)

func TestSHA3_256FIPSVectors(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		wantHex string
		input   []byte
	}{
		emptyName: {
			input:   []byte{},
			wantHex: "a7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a",
		},
		abcName: {
			input:   []byte("abc"),
			wantHex: "3a985da74fe225b2045c172d6bd390bd855f086e3e9d525b46bfe24511431532",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := cryptosha3.New256().Hash(tc.input)
			want := testkit.MustDecodeHex(t, tc.wantHex)
			testkit.Equal(t, got.Bytes(), want, "Hash output must byte-match FIPS vector")
		})
	}
}

func TestSHA3_384FIPSVectors(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		wantHex string
		input   []byte
	}{
		emptyName: {
			input: []byte{},
			wantHex: "0c63a75b845e4f7d01107d852e4c2485c51a50aaaa94fc61995e71bbee983a2a" +
				"c3713831264adb47fb6bd1e058d5f004",
		},
		abcName: {
			input: []byte("abc"),
			wantHex: "ec01498288516fc926459f58e2c6ad8df9b473cb0fc08c2596da7cf0e49be4b2" +
				"98d88cea927ac7f539f1edf228376d25",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := cryptosha3.New384().Hash(tc.input)
			want := testkit.MustDecodeHex(t, tc.wantHex)
			testkit.Equal(t, got.Bytes(), want, "Hash output must byte-match FIPS vector")
		})
	}
}

func TestSHA3_512FIPSVectors(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		wantHex string
		input   []byte
	}{
		emptyName: {
			input: []byte{},
			wantHex: "a69f73cca23a9ac5c8b567dc185a756e97c982164fe25859e0d1dcc1475c80a6" +
				"15b2123af1f5f94c11e3e9402c3ac558f500199d95b6d3e301758586281dcd26",
		},
		abcName: {
			input: []byte("abc"),
			wantHex: "b751850b1a57168a5693cd924b6b096e08f621827444f70d884f5d0240d2712e" +
				"10e116e9192af3c91a7ec57647e3934057340b4cf408d5a56592f8274eec53f0",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := cryptosha3.New512().Hash(tc.input)
			want := testkit.MustDecodeHex(t, tc.wantHex)
			testkit.Equal(t, got.Bytes(), want, "Hash output must byte-match FIPS vector")
		})
	}
}
