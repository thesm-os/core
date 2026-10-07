// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package sha256_test

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"slices"
	"strconv"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"
	"go.dokimi.dev/assert/prop"
	"go.thesmos.sh/testkit/bench"

	"go.thesmos.sh/core/coretest/cryptotest"
	"go.thesmos.sh/core/crypto"
	cryptosha256 "go.thesmos.sh/core/crypto/sha256"
)

// The example roles of the golden vectors. Core ships no roles, and
// nothing here binds a protocol to a registry.
const (
	entryLeaf    crypto.Role = 0x01
	chainGenesis crypto.Role = 0x02
	chainLink    crypto.Role = 0x83
	batchNode    crypto.Role = 0x84
	mmrNode      crypto.Role = 0x85
)

// The goroutines of a concurrent case, and the digests that each
// computes.
const (
	goroutines = 8
	rounds     = 20
)

// sha256ID is the canonical build-local identifier for the SHA-256
// [crypto.Hasher]: "sha256/v1", left-aligned and padded with zeros to
// [crypto.IDSize].
var sha256ID = crypto.ID{'s', 'h', 'a', '2', '5', '6', '/', 'v', '1'}

// stdlibSpec describes the stdlib SHA-256 reference, used to
// build the model's reference factory.
var stdlibSpec = cryptotest.StdlibHasherSpec{
	Algorithm: crypto.AlgSHA256,
	ID:        sha256ID,
	Sum:       func(d []byte) []byte { h := sha256.Sum256(d); return h[:] },
	NewHash:   func() hash.Hash { return sha256.New() },
}

// The payloads of the leaves of the golden vectors.
var (
	payload0 = []byte(`{"act":"infer","id":1}`)
	payload1 = []byte(`{"act":"infer","id":2}`)
	payload2 = []byte(`{"act":"score","id":3}`)
)

// TestSHA256HasherContract runs the contract suite of crypto.Hasher with
// the ID, the Algorithm and the digests of the standard library.
func TestSHA256HasherContract(t *testing.T) {
	t.Parallel()
	cryptotest.AssertHasherContract(t, func() crypto.Hasher { return cryptosha256.New() },
		append(cryptotest.HasherContractAssertions(),
			cryptotest.HasherIDAssertion(sha256ID),
			cryptotest.HasherAlgorithmAssertion(crypto.AlgSHA256),
			cryptotest.HasherCrossStdlibAssertion(stdlibSpec.Sum),
		)...,
	)
}

// TestSHA256HasherModel drives random byte sequences through
// both the SUT and a stdlib-backed reference, asserting byte-
// exact equivalence on every Hash and tagged call.
func TestSHA256HasherModel(t *testing.T) {
	t.Parallel()
	cryptotest.HasherModelTest(t, func() crypto.Hasher { return cryptosha256.New() },
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

// TestHasher pins the digests of the algorithm of record and of the
// tagged layout, so a verifier written against them in another language
// agrees with this one.
func TestHasher(t *testing.T) {
	t.Parallel()

	h := cryptosha256.New()
	leaf0 := h.HashTagged(entryLeaf, payload0)
	leaf1 := h.HashTagged(entryLeaf, payload1)
	leaf2 := h.HashTagged(entryLeaf, payload2)
	e1 := h.HashTagged(chainGenesis, leaf0.Bytes())

	t.Run("New", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the zero Hasher", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, cryptosha256.New(), cryptosha256.Hasher{}, "the zero Hasher must be usable as New")
		})
	})

	t.Run("NewStream", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a Stream whose Write reports every byte of p", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, func(p []byte) int {
				n, _ := h.NewStream().Write(p)

				return n
			}, func(p []byte) int { return len(p) }, "Write must report the length of p",
				prop.Using(prop.Bytes(prop.MaxSize(256))))
		})
	})

	t.Run("Hash", func(t *testing.T) {
		t.Parallel()

		// The FIPS 180-4 vectors are the algorithm of record, so a
		// failure here means that the implementation and the standard
		// library have both departed from the standard.
		tests := []struct {
			name string
			give []byte
			want string
		}{
			{
				name: "returns the FIPS 180-4 digest of the empty message",
				give: []byte{},
				want: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			},
			{
				name: `returns the FIPS 180-4 digest of "abc"`,
				give: []byte("abc"),
				want: "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
			},
			{
				name: "returns the FIPS 180-4 digest of the message of two blocks",
				give: []byte("abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq"),
				want: "248d6a61d20638b8e5c026930c3e6039a33ce45964ff2167f6ecedd419db06c1",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, hex.EncodeToString(h.Hash(tt.give).Bytes()), tt.want,
					"Hash must match the FIPS vector")
			})
		}
	})

	t.Run("HashTagged", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the recorded digests of leaves and of a chain genesis", func(t *testing.T) {
			t.Parallel()
			for _, tt := range []struct {
				name string
				got  crypto.Digest
				want string
			}{
				{"leaf0", leaf0, "9978971ab897cd3656fdde798a8984a606b37e51f2153f5ebeae2b6a06550cfd"},
				{"leaf1", leaf1, "67a0f291cd3e01dbddfaacf8ba3e0af33627dd94cc4d7ad3d9deefe302faafcd"},
				{"leaf2", leaf2, "921fd00458c68f7c46437576db9f96878f388c9eea233858cafa0a18733c77f8"},
				{"E1", e1, "34b6c48bb25089e98c8ac2b7a67ea4c35dbf1cf2dff29526af55c46cd3d7dfb5"},
			} {
				expect.Equal(t, hex.EncodeToString(tt.got.Bytes()), tt.want, tt.name+" must match its recorded digest")
			}
		})

		t.Run("returns the digest of the role byte alone for empty data", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, hex.EncodeToString(h.HashTagged(entryLeaf, nil).Bytes()),
				"4bf5122f344554c53bde2ebb8cd2b7e3d1600ad631c385a5d7cce23c7785459a",
				"HashTagged over empty data must be SHA-256 of the single role byte")
		})

		// The attacker's payload is exactly the two sibling digests, and
		// its digest must differ from the interior node of the siblings.
		t.Run("returns the recorded digest of a leaf crafted from two siblings", func(t *testing.T) {
			t.Parallel()
			crafted := append(append([]byte{}, leaf0.Bytes()...), leaf1.Bytes()...)
			assert.Equal(t, hex.EncodeToString(h.HashTagged(entryLeaf, crafted).Bytes()),
				"97055cac3af04d642568f4619517572949240caec5256838a62cf9c166b19c8e",
				"the crafted leaf must match its recorded digest")
		})

		t.Run("returns the digest of one call to goroutines that hash at once", func(t *testing.T) {
			t.Parallel()
			outcomes := history.Concurrently(goroutines, 10*time.Second, func(client int) (any, error) {
				data := []byte("payload " + strconv.Itoa(client))
				digests := make([]crypto.Digest, 0, rounds)
				for range rounds {
					digests = append(digests, h.HashTagged(entryLeaf, data))
				}

				return digests, nil
			})
			for _, o := range outcomes {
				assert.True(t, o.Finished, "every goroutine must finish")
				digests, _ := o.Output.([]crypto.Digest)
				want := h.HashTagged(entryLeaf, []byte("payload "+strconv.Itoa(o.Client)))
				assert.Equal(t, digests, slices.Repeat([]crypto.Digest{want}, rounds),
					"every digest must equal the digest of one call")
			}
		})
	})

	t.Run("CombineTagged", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the recorded digests of a chain and a batch tree", func(t *testing.T) {
			t.Parallel()
			e2 := h.CombineTagged(chainLink, e1, leaf1)
			n01 := h.CombineTagged(batchNode, leaf0, leaf1)
			for _, tt := range []struct {
				name string
				got  crypto.Digest
				want string
			}{
				{"E2", e2, "ba248ec5db725a8eff0a0068c4225132425bd983e154b158f7ce5bcb0bc60df6"},
				{"E3", h.CombineTagged(chainLink, e2, leaf2), "cde1a7dcd5630788b710cd741398fa14bff17083a79ed9e895880b949d2ce296"},
				{"n01", n01, "cf9b6328ccb2bd4156e7f22b4417d426fa7a0bce96e74690ecbb9ff95b723715"},
				{"BatchRoot", h.CombineTagged(batchNode, n01, leaf2), "fc2f792f6acc7aa9f5ebbc7de9ffa490ff08ef33cbffefb108da46b501d71fba"},
			} {
				expect.Equal(t, hex.EncodeToString(tt.got.Bytes()), tt.want, tt.name+" must match its recorded digest")
			}
		})

		// The width check rejects a zero operand too, so only the
		// message tells the two refusals apart.
		zeros := []struct {
			name        string
			left, right crypto.Digest
		}{
			{name: "panics with the message of the zero Digest for a zero left operand", right: leaf1},
			{name: "panics with the message of the zero Digest for a zero right operand", left: leaf0},
		}
		for _, tt := range zeros {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				recovered := assert.Panics(t, func() { h.CombineTagged(batchNode, tt.left, tt.right) },
					"CombineTagged must refuse a zero operand")
				assert.Contains(t, recovered, "refuses the zero Digest", "the panic must name the zero Digest")
			})
		}

		t.Run("returns another digest of one operand pair under each binary role", func(t *testing.T) {
			t.Parallel()
			for _, tt := range []struct {
				name string
				role crypto.Role
				want string
			}{
				{"chain-link", chainLink, "2127809bd7be825e"},
				{"batch-node", batchNode, "cf9b6328ccb2bd41"},
				{"mmr-node", mmrNode, "6eebccae2a270500"},
			} {
				got := h.CombineTagged(tt.role, leaf0, leaf1)
				expect.Equal(t, hex.EncodeToString(got.Bytes()[:8]), tt.want,
					tt.name+" must match its recorded digest prefix")
			}
		})
	})
}

// TestHasherAllocs checks the allocation contract through the
// crypto.Hasher and crypto.Stream interfaces, with data on the heap, as a
// caller that must not allocate passes it. MaxAllocs counts the
// allocations of the whole process, so the test does not run in
// parallel.
//
//nolint:paralleltest // see above
func TestHasherAllocs(t *testing.T) {
	for name, fn := range cryptotest.HasherZeroAllocCases(cryptosha256.New(), make([]byte, 1024)) {
		t.Run(name, func(t *testing.T) {
			expect.MaxAllocs(t, fn, 0, name+" must not allocate on the warm path")
		})
	}
}

// BenchmarkSHA256Hasher runs the standard Hasher bench contract: a
// hot-path measurement for every method, and PureAllocsWithin(0)
// gates for the documented zero-alloc paths (Hash, the tagged pair,
// Algorithm and ID).
func BenchmarkSHA256Hasher(b *testing.B) {
	cryptotest.BenchmarkHasherContract(b, func() crypto.Hasher { return cryptosha256.New() },
		cryptotest.HasherBenchOnAlgorithm(bench.PureAllocsWithin[crypto.Hasher, crypto.Algorithm](0)),
		cryptotest.HasherBenchOnID(bench.PureAllocsWithin[crypto.Hasher, crypto.ID](0)),
		cryptotest.HasherBenchOnHash(bench.PureAllocsWithin[crypto.Hasher, crypto.Digest](0)),
		cryptotest.HasherBenchOnHashTagged(bench.PureAllocsWithin[crypto.Hasher, crypto.Digest](0)),
		cryptotest.HasherBenchOnCombineTagged(bench.PureAllocsWithin[crypto.Hasher, crypto.Digest](0)),
	)
}

// FuzzSHA256HasherModel is the coverage-guided fuzz wrapper around the
// model property, with the actions of TestSHA256HasherModel.
func FuzzSHA256HasherModel(f *testing.F) {
	cryptotest.HasherModelFuzz(f, func() crypto.Hasher { return cryptosha256.New() },
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
