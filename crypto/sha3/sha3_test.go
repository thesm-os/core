// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package sha3_test

import (
	"crypto/sha3"
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
	cryptosha3 "go.thesmos.sh/core/crypto/sha3"
)

// The unary role of the HashTagged cases and the binary role of the
// CombineTagged cases.
const (
	entryLeaf crypto.Role = 0x01
	batchNode crypto.Role = 0x84
)

// The goroutines of a concurrent case, and the digests that each
// computes.
const (
	goroutines = 8
	rounds     = 20
)

// Canonical build-local IDs for SHA3-256, SHA3-384, SHA3-512.
var (
	sha3_256ID = crypto.ID{'s', 'h', 'a', '3', '-', '2', '5', '6', '/', 'v', '1'}
	sha3_384ID = crypto.ID{'s', 'h', 'a', '3', '-', '3', '8', '4', '/', 'v', '1'}
	sha3_512ID = crypto.ID{'s', 'h', 'a', '3', '-', '5', '1', '2', '/', 'v', '1'}
)

// sha3_256Spec describes the stdlib SHA3-256 reference of the model.
var sha3_256Spec = cryptotest.StdlibHasherSpec{
	Algorithm: crypto.AlgSHA3_256,
	ID:        sha3_256ID,
	Sum:       func(d []byte) []byte { h := sha3.Sum256(d); return h[:] },
	NewHash:   func() hash.Hash { return sha3.New256() },
}

// sha3_384Spec describes the stdlib SHA3-384 reference of the model.
var sha3_384Spec = cryptotest.StdlibHasherSpec{
	Algorithm: crypto.AlgSHA3_384,
	ID:        sha3_384ID,
	Sum:       func(d []byte) []byte { h := sha3.Sum384(d); return h[:] },
	NewHash:   func() hash.Hash { return sha3.New384() },
}

// sha3_512Spec describes the stdlib SHA3-512 reference of the model.
var sha3_512Spec = cryptotest.StdlibHasherSpec{
	Algorithm: crypto.AlgSHA3_512,
	ID:        sha3_512ID,
	Sum:       func(d []byte) []byte { h := sha3.Sum512(d); return h[:] },
	NewHash:   func() hash.Hash { return sha3.New512() },
}

// hashers are the three Hashers of the package, with their FIPS 202
// vectors of the empty message and of "abc".
var hashers = []struct {
	name string
	h    crypto.Hasher
	fips [2]string
}{
	{
		name: "Hasher256",
		h:    cryptosha3.New256(),
		fips: [2]string{
			"a7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a",
			"3a985da74fe225b2045c172d6bd390bd855f086e3e9d525b46bfe24511431532",
		},
	},
	{
		name: "Hasher384",
		h:    cryptosha3.New384(),
		fips: [2]string{
			"0c63a75b845e4f7d01107d852e4c2485c51a50aaaa94fc61995e71bbee983a2ac3713831264adb47fb6bd1e058d5f004",
			"ec01498288516fc926459f58e2c6ad8df9b473cb0fc08c2596da7cf0e49be4b298d88cea927ac7f539f1edf228376d25",
		},
	},
	{
		name: "Hasher512",
		h:    cryptosha3.New512(),
		fips: [2]string{
			"a69f73cca23a9ac5c8b567dc185a756e97c982164fe25859e0d1dcc1475c80a6" +
				"15b2123af1f5f94c11e3e9402c3ac558f500199d95b6d3e301758586281dcd26",
			"b751850b1a57168a5693cd924b6b096e08f621827444f70d884f5d0240d2712e" +
				"10e116e9192af3c91a7ec57647e3934057340b4cf408d5a56592f8274eec53f0",
		},
	},
}

// TestSHA3_256HasherContract runs the contract suite of crypto.Hasher on
// SHA3-256 with its ID, its Algorithm and the digests of the standard
// library.
func TestSHA3_256HasherContract(t *testing.T) {
	t.Parallel()
	cryptotest.AssertHasherContract(t, func() crypto.Hasher { return cryptosha3.New256() },
		append(cryptotest.HasherContractAssertions(),
			cryptotest.HasherIDAssertion(sha3_256ID),
			cryptotest.HasherAlgorithmAssertion(crypto.AlgSHA3_256),
			cryptotest.HasherCrossStdlibAssertion(sha3_256Spec.Sum),
		)...,
	)
}

// TestSHA3_256HasherModel checks SHA3-256 against the stdlib reference
// on every Hash and tagged call.
func TestSHA3_256HasherModel(t *testing.T) {
	t.Parallel()
	cryptotest.HasherModelTest(t, func() crypto.Hasher { return cryptosha3.New256() },
		cryptotest.HasherModelReference(func() crypto.Hasher {
			return cryptotest.NewStdlibHasherStub(t, sha3_256Spec)
		}),
		cryptotest.HasherModelExtraActions(
			cryptotest.HasherHashAction(),
			cryptotest.HasherHashTaggedAction(),
			cryptotest.HasherCombineTaggedAction(),
		),
	)
}

// TestSHA3_384HasherContract runs the contract suite of crypto.Hasher on
// SHA3-384 with its ID, its Algorithm and the digests of the standard
// library.
func TestSHA3_384HasherContract(t *testing.T) {
	t.Parallel()
	cryptotest.AssertHasherContract(t, func() crypto.Hasher { return cryptosha3.New384() },
		append(cryptotest.HasherContractAssertions(),
			cryptotest.HasherIDAssertion(sha3_384ID),
			cryptotest.HasherAlgorithmAssertion(crypto.AlgSHA3_384),
			cryptotest.HasherCrossStdlibAssertion(sha3_384Spec.Sum),
		)...,
	)
}

// TestSHA3_384HasherModel checks SHA3-384 against the stdlib reference
// on every Hash and tagged call.
func TestSHA3_384HasherModel(t *testing.T) {
	t.Parallel()
	cryptotest.HasherModelTest(t, func() crypto.Hasher { return cryptosha3.New384() },
		cryptotest.HasherModelReference(func() crypto.Hasher {
			return cryptotest.NewStdlibHasherStub(t, sha3_384Spec)
		}),
		cryptotest.HasherModelExtraActions(
			cryptotest.HasherHashAction(),
			cryptotest.HasherHashTaggedAction(),
			cryptotest.HasherCombineTaggedAction(),
		),
	)
}

// TestSHA3_512HasherContract runs the contract suite of crypto.Hasher on
// SHA3-512 with its ID, its Algorithm and the digests of the standard
// library.
func TestSHA3_512HasherContract(t *testing.T) {
	t.Parallel()
	cryptotest.AssertHasherContract(t, func() crypto.Hasher { return cryptosha3.New512() },
		append(cryptotest.HasherContractAssertions(),
			cryptotest.HasherIDAssertion(sha3_512ID),
			cryptotest.HasherAlgorithmAssertion(crypto.AlgSHA3_512),
			cryptotest.HasherCrossStdlibAssertion(sha3_512Spec.Sum),
		)...,
	)
}

// TestSHA3_512HasherModel checks SHA3-512 against the stdlib reference
// on every Hash and tagged call.
func TestSHA3_512HasherModel(t *testing.T) {
	t.Parallel()
	cryptotest.HasherModelTest(t, func() crypto.Hasher { return cryptosha3.New512() },
		cryptotest.HasherModelReference(func() crypto.Hasher {
			return cryptotest.NewStdlibHasherStub(t, sha3_512Spec)
		}),
		cryptotest.HasherModelExtraActions(
			cryptotest.HasherHashAction(),
			cryptotest.HasherHashTaggedAction(),
			cryptotest.HasherCombineTaggedAction(),
		),
	)
}

// TestSHA3 pins the FIPS 202 digests of the three Hashers, the digests of
// concurrent tagged calls, the message of a zero operand and the count of
// a Write.
func TestSHA3(t *testing.T) {
	t.Parallel()

	for _, hasher := range hashers {
		t.Run(hasher.name, func(t *testing.T) {
			t.Parallel()
			h := hasher.h

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

				// The FIPS 202 vectors are the algorithm of record.
				vectors := []struct {
					name string
					give []byte
					want string
				}{
					{name: "returns the FIPS 202 digest of the empty message", give: []byte{}, want: hasher.fips[0]},
					{name: `returns the FIPS 202 digest of "abc"`, give: []byte("abc"), want: hasher.fips[1]},
				}
				for _, tt := range vectors {
					t.Run(tt.name, func(t *testing.T) {
						t.Parallel()
						assert.Equal(t, hex.EncodeToString(h.Hash(tt.give).Bytes()), tt.want,
							"Hash must match the FIPS vector")
					})
				}
			})

			t.Run("HashTagged", func(t *testing.T) {
				t.Parallel()

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

				// The width check rejects a zero operand too, so only the
				// message tells the two refusals apart.
				operand := h.Hash([]byte("operand"))
				zeros := []struct {
					name        string
					left, right crypto.Digest
				}{
					{name: "panics with the message of the zero Digest for a zero left operand", right: operand},
					{name: "panics with the message of the zero Digest for a zero right operand", left: operand},
				}
				for _, tt := range zeros {
					t.Run(tt.name, func(t *testing.T) {
						t.Parallel()
						recovered := assert.Panics(t, func() { h.CombineTagged(batchNode, tt.left, tt.right) },
							"CombineTagged must refuse a zero operand")
						assert.Contains(t, recovered, "refuses the zero Digest", "the panic must name the zero Digest")
					})
				}
			})
		})
	}
}

// TestSHA3Allocs checks the allocation contract of every SHA-3 Hasher
// through the crypto.Hasher and crypto.Stream interfaces, with data on
// the heap, as a caller that must not allocate passes it. MaxAllocs
// counts the allocations of the whole process, so the test does not run
// in parallel.
func TestSHA3Allocs(t *testing.T) {
	data := make([]byte, 1024)

	for _, hasher := range hashers {
		t.Run(hasher.name, func(t *testing.T) {
			for name, fn := range cryptotest.HasherZeroAllocCases(hasher.h, data) {
				t.Run(name, func(t *testing.T) {
					expect.MaxAllocs(t, fn, 0, name+" must not allocate on the warm path")
				})
			}
		})
	}
}

// BenchmarkSHA3_256Hasher runs the standard Hasher bench contract on
// SHA3-256.
func BenchmarkSHA3_256Hasher(b *testing.B) {
	cryptotest.BenchmarkHasherContract(b, func() crypto.Hasher { return cryptosha3.New256() },
		cryptotest.HasherBenchOnAlgorithm(bench.PureAllocsWithin[crypto.Hasher, crypto.Algorithm](0)),
		cryptotest.HasherBenchOnID(bench.PureAllocsWithin[crypto.Hasher, crypto.ID](0)),
		cryptotest.HasherBenchOnHash(bench.PureAllocsWithin[crypto.Hasher, crypto.Digest](0)),
		cryptotest.HasherBenchOnHashTagged(bench.PureAllocsWithin[crypto.Hasher, crypto.Digest](0)),
		cryptotest.HasherBenchOnCombineTagged(bench.PureAllocsWithin[crypto.Hasher, crypto.Digest](0)),
	)
}

// BenchmarkSHA3_384Hasher runs the standard Hasher bench contract on
// SHA3-384.
func BenchmarkSHA3_384Hasher(b *testing.B) {
	cryptotest.BenchmarkHasherContract(b, func() crypto.Hasher { return cryptosha3.New384() },
		cryptotest.HasherBenchOnAlgorithm(bench.PureAllocsWithin[crypto.Hasher, crypto.Algorithm](0)),
		cryptotest.HasherBenchOnID(bench.PureAllocsWithin[crypto.Hasher, crypto.ID](0)),
		cryptotest.HasherBenchOnHash(bench.PureAllocsWithin[crypto.Hasher, crypto.Digest](0)),
		cryptotest.HasherBenchOnHashTagged(bench.PureAllocsWithin[crypto.Hasher, crypto.Digest](0)),
		cryptotest.HasherBenchOnCombineTagged(bench.PureAllocsWithin[crypto.Hasher, crypto.Digest](0)),
	)
}

// BenchmarkSHA3_512Hasher runs the standard Hasher bench contract on
// SHA3-512.
func BenchmarkSHA3_512Hasher(b *testing.B) {
	cryptotest.BenchmarkHasherContract(b, func() crypto.Hasher { return cryptosha3.New512() },
		cryptotest.HasherBenchOnAlgorithm(bench.PureAllocsWithin[crypto.Hasher, crypto.Algorithm](0)),
		cryptotest.HasherBenchOnID(bench.PureAllocsWithin[crypto.Hasher, crypto.ID](0)),
		cryptotest.HasherBenchOnHash(bench.PureAllocsWithin[crypto.Hasher, crypto.Digest](0)),
		cryptotest.HasherBenchOnHashTagged(bench.PureAllocsWithin[crypto.Hasher, crypto.Digest](0)),
		cryptotest.HasherBenchOnCombineTagged(bench.PureAllocsWithin[crypto.Hasher, crypto.Digest](0)),
	)
}

// FuzzSHA3_256HasherModel is the coverage-guided fuzz wrapper around the
// model property of SHA3-256.
func FuzzSHA3_256HasherModel(f *testing.F) {
	cryptotest.HasherModelFuzz(f, func() crypto.Hasher { return cryptosha3.New256() },
		cryptotest.HasherModelReference(func() crypto.Hasher {
			return cryptotest.NewStdlibHasherStub(f, sha3_256Spec)
		}),
		cryptotest.HasherModelExtraActions(
			cryptotest.HasherHashAction(),
			cryptotest.HasherHashTaggedAction(),
			cryptotest.HasherCombineTaggedAction(),
		),
	)
}

// FuzzSHA3_384HasherModel is the coverage-guided fuzz wrapper around the
// model property of SHA3-384.
func FuzzSHA3_384HasherModel(f *testing.F) {
	cryptotest.HasherModelFuzz(f, func() crypto.Hasher { return cryptosha3.New384() },
		cryptotest.HasherModelReference(func() crypto.Hasher {
			return cryptotest.NewStdlibHasherStub(f, sha3_384Spec)
		}),
		cryptotest.HasherModelExtraActions(
			cryptotest.HasherHashAction(),
			cryptotest.HasherHashTaggedAction(),
			cryptotest.HasherCombineTaggedAction(),
		),
	)
}

// FuzzSHA3_512HasherModel is the coverage-guided fuzz wrapper around the
// model property of SHA3-512.
func FuzzSHA3_512HasherModel(f *testing.F) {
	cryptotest.HasherModelFuzz(f, func() crypto.Hasher { return cryptosha3.New512() },
		cryptotest.HasherModelReference(func() crypto.Hasher {
			return cryptotest.NewStdlibHasherStub(f, sha3_512Spec)
		}),
		cryptotest.HasherModelExtraActions(
			cryptotest.HasherHashAction(),
			cryptotest.HasherHashTaggedAction(),
			cryptotest.HasherCombineTaggedAction(),
		),
	)
}
