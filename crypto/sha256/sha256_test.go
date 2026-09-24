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

// sha256ID is the canonical build-local identifier for the SHA-256
// [crypto.Hasher]: "sha256/v1", left-aligned and padded with zeros to
// [crypto.IDSize].
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
		)...,
	)
}

// TestSHA256HasherModel drives random byte sequences through
// both the SUT and a stdlib-backed reference, asserting byte-
// exact equivalence on every Hash and tagged call. Failures
// shrink to the minimal divergent input via rapid.
func TestSHA256HasherModel(t *testing.T) {
	t.Parallel()
	cryptotest.HasherModelTest(t, newHasher,
		cryptotest.HasherModelReference(func() crypto.Hasher {
			return cryptotest.NewStdlibHasherStub(t, stdlibSpec)
		}),
		cryptotest.HasherModelExtraActions(
			cryptotest.HasherHashAction(),
			cryptotest.HasherHashTaggedAction(),
			cryptotest.HasherCombineTaggedAction(),
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
			cryptotest.HasherHashTaggedAction(),
			cryptotest.HasherCombineTaggedAction(),
		),
	)
}

// BenchmarkSHA256Hasher runs the standard Hasher bench contract: a
// hot-path measurement for every method, and PureAllocsWithin(0)
// gates for the documented zero-alloc paths (Hash, the tagged pair,
// Algorithm and ID).
func BenchmarkSHA256Hasher(b *testing.B) {
	cryptotest.BenchmarkHasherContract(b, newHasher,
		cryptotest.HasherBenchOnAlgorithm(bench.PureAllocsWithin[crypto.Hasher, crypto.Algorithm](0)),
		cryptotest.HasherBenchOnID(bench.PureAllocsWithin[crypto.Hasher, crypto.ID](0)),
		cryptotest.HasherBenchOnHash(bench.PureAllocsWithin[crypto.Hasher, crypto.Digest](0)),
		cryptotest.HasherBenchOnHashTagged(bench.PureAllocsWithin[crypto.Hasher, crypto.Digest](0)),
		cryptotest.HasherBenchOnCombineTagged(bench.PureAllocsWithin[crypto.Hasher, crypto.Digest](0)),
	)
}

// --- SHA-256-specific tests ---

// TestSHA256FIPSVectors checks the implementation against the FIPS
// 180-4 known-answer vectors. The contract suite's
// HasherCrossStdlibAssertion checks byte equivalence with the stdlib
// across a sweep of inputs. The FIPS vectors are the algorithm of
// record, so a failure here means the implementation and the stdlib
// have both departed from the standard.
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

// TestTaggedGoldenVectors pins the tagged byte layout. The generic
// conformance suite checks the shape for every implementation: one
// role byte, then the operands, with no framing. These vectors pin the
// resulting SHA-256 digests, so a verifier written against them in
// another language agrees with this one.
//
// The role values are examples. Core ships none, and nothing here
// binds any protocol to a registry. The crafted-leaf case is the
// forgery the roles prevent: the attacker's payload is exactly the two
// sibling digests, and its digest must differ from their interior
// node's.
func TestTaggedGoldenVectors(t *testing.T) {
	t.Parallel()

	const (
		entryLeaf    crypto.Role = 0x01
		chainGenesis crypto.Role = 0x02
		chainLink    crypto.Role = 0x83
		batchNode    crypto.Role = 0x84
		mmrNode      crypto.Role = 0x85
	)

	payload0 := []byte(`{"act":"infer","id":1}`)
	payload1 := []byte(`{"act":"infer","id":2}`)
	payload2 := []byte(`{"act":"score","id":3}`)

	t.Run("leaves, a chain and a batch tree match their recorded digests", func(t *testing.T) {
		t.Parallel()

		h := newHasher()
		leaf0 := h.HashTagged(entryLeaf, payload0)
		leaf1 := h.HashTagged(entryLeaf, payload1)
		leaf2 := h.HashTagged(entryLeaf, payload2)

		e1 := h.HashTagged(chainGenesis, leaf0.Bytes())
		e2 := h.CombineTagged(chainLink, e1, leaf1)
		e3 := h.CombineTagged(chainLink, e2, leaf2)

		n01 := h.CombineTagged(batchNode, leaf0, leaf1)
		root := h.CombineTagged(batchNode, n01, leaf2)

		cases := map[string]struct {
			got     crypto.Digest
			wantHex string
		}{
			"leaf0":     {leaf0, "9978971ab897cd3656fdde798a8984a606b37e51f2153f5ebeae2b6a06550cfd"},
			"leaf1":     {leaf1, "67a0f291cd3e01dbddfaacf8ba3e0af33627dd94cc4d7ad3d9deefe302faafcd"},
			"leaf2":     {leaf2, "921fd00458c68f7c46437576db9f96878f388c9eea233858cafa0a18733c77f8"},
			"E1":        {e1, "34b6c48bb25089e98c8ac2b7a67ea4c35dbf1cf2dff29526af55c46cd3d7dfb5"},
			"E2":        {e2, "ba248ec5db725a8eff0a0068c4225132425bd983e154b158f7ce5bcb0bc60df6"},
			"E3":        {e3, "cde1a7dcd5630788b710cd741398fa14bff17083a79ed9e895880b949d2ce296"},
			"n01":       {n01, "cf9b6328ccb2bd4156e7f22b4417d426fa7a0bce96e74690ecbb9ff95b723715"},
			"BatchRoot": {root, "fc2f792f6acc7aa9f5ebbc7de9ffa490ff08ef33cbffefb108da46b501d71fba"},
		}
		for name, tc := range cases {
			testkit.Equal(t, tc.got.Bytes(), testkit.MustDecodeHex(t, tc.wantHex),
				name+" must match its recorded digest")
		}
	})

	t.Run("an empty leaf is the role byte alone", func(t *testing.T) {
		t.Parallel()

		got := newHasher().HashTagged(entryLeaf, nil)
		testkit.Equal(t, got.Bytes(),
			testkit.MustDecodeHex(t, "4bf5122f344554c53bde2ebb8cd2b7e3d1600ad631c385a5d7cce23c7785459a"),
			"HashTagged over empty data must be SHA-256 of the single role byte")
	})

	t.Run("a crafted leaf and the interior node it targets differ", func(t *testing.T) {
		t.Parallel()

		h := newHasher()
		leaf0 := h.HashTagged(entryLeaf, payload0)
		leaf1 := h.HashTagged(entryLeaf, payload1)
		crafted := append(append([]byte{}, leaf0.Bytes()...), leaf1.Bytes()...)

		testkit.Equal(t, h.HashTagged(entryLeaf, crafted).Bytes(),
			testkit.MustDecodeHex(t, "97055cac3af04d642568f4619517572949240caec5256838a62cf9c166b19c8e"),
			"the crafted leaf must match its recorded digest")
		testkit.Equal(t, h.CombineTagged(batchNode, leaf0, leaf1).Bytes(),
			testkit.MustDecodeHex(t, "cf9b6328ccb2bd4156e7f22b4417d426fa7a0bce96e74690ecbb9ff95b723715"),
			"the interior node must match its recorded digest")
	})

	t.Run("one operand pair under three binary roles gives three digests", func(t *testing.T) {
		t.Parallel()

		h := newHasher()
		leaf0 := h.HashTagged(entryLeaf, payload0)
		leaf1 := h.HashTagged(entryLeaf, payload1)

		cases := map[string]struct {
			role    crypto.Role
			wantHex string
		}{
			"chain-link": {chainLink, "2127809bd7be825e"},
			"batch-node": {batchNode, "cf9b6328ccb2bd41"},
			"mmr-node":   {mmrNode, "6eebccae2a270500"},
		}
		for name, tc := range cases {
			got := h.CombineTagged(tc.role, leaf0, leaf1)
			testkit.Equal(t, got.Bytes()[:8], testkit.MustDecodeHex(t, tc.wantHex),
				name+" must match its recorded digest prefix")
		}
	})
}

// TestZeroValueHasher checks the documented property that the zero
// value of [cryptosha256.Hasher] is usable. The type is struct{}, so
// the zero value behaves as the constructor's result does.
func TestZeroValueHasher(t *testing.T) {
	t.Parallel()

	t.Run("reports the constructor's ID and Algorithm", func(t *testing.T) {
		t.Parallel()
		var z cryptosha256.Hasher
		testkit.Equal(t, z.ID(), cryptosha256.New().ID(),
			"zero-value Hasher must report the same ID as a constructed one")
		testkit.Equal(t, z.Algorithm(), cryptosha256.New().Algorithm(),
			"zero-value Hasher must report the same Algorithm as a constructed one")
	})
}

// TestZeroAlloc enforces the allocation contract through the
// crypto.Hasher and crypto.Stream interfaces, with data on the heap,
// as a caller that must not allocate passes it. testing.AllocsPerRun
// reads a process-global malloc counter, so this test does not call
// t.Parallel.
//
//nolint:paralleltest // see comment above
func TestZeroAlloc(t *testing.T) {
	for name, fn := range cryptotest.HasherZeroAllocCases(newHasher(), make([]byte, 1024)) {
		t.Run(name, func(t *testing.T) {
			testkit.Equal(t, testing.AllocsPerRun(100, fn), float64(0), name+" must not allocate on the warm path")
		})
	}
}
