// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package sha512_test

import (
	"crypto/sha512"
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
	cryptosha512 "go.thesmos.sh/core/crypto/sha512"
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

// Canonical build-local IDs for SHA-384 and SHA-512.
var (
	sha384ID = crypto.ID{'s', 'h', 'a', '3', '8', '4', '/', 'v', '1'}
	sha512ID = crypto.ID{'s', 'h', 'a', '5', '1', '2', '/', 'v', '1'}
)

// sha384Spec describes the stdlib SHA-384 reference of the model.
var sha384Spec = cryptotest.StdlibHasherSpec{
	Algorithm: crypto.AlgSHA384,
	ID:        sha384ID,
	Sum:       func(d []byte) []byte { h := sha512.Sum384(d); return h[:] },
	NewHash:   func() hash.Hash { return sha512.New384() },
}

// sha512Spec describes the stdlib SHA-512 reference of the model.
var sha512Spec = cryptotest.StdlibHasherSpec{
	Algorithm: crypto.AlgSHA512,
	ID:        sha512ID,
	Sum:       func(d []byte) []byte { h := sha512.Sum512(d); return h[:] },
	NewHash:   func() hash.Hash { return sha512.New() },
}

// hashers are the two Hashers of the package, with their FIPS 180-4
// vectors of the empty message, of "abc" and of the message of two
// blocks.
var hashers = []struct {
	name string
	h    crypto.Hasher
	fips [3]string
}{
	{
		name: "Hasher384",
		h:    cryptosha512.New384(),
		fips: [3]string{
			"38b060a751ac96384cd9327eb1b1e36a21fdb71114be07434c0cc7bf63f6e1da274edebfe76f65fbd51ad2f14898b95b",
			"cb00753f45a35e8bb5a03d699ac65007272c32ab0eded1631a8b605a43ff5bed8086072ba1e7cc2358baeca134c825a7",
			"09330c33f71147e83d192fc782cd1b4753111b173b3b05d22fa08086e3b0f712fcc7c71a557e2db966c3e9fa91746039",
		},
	},
	{
		name: "Hasher512",
		h:    cryptosha512.New512(),
		fips: [3]string{
			"cf83e1357eefb8bdf1542850d66d8007d620e4050b5715dc83f4a921d36ce9ce" +
				"47d0d13c5d85f2b0ff8318d2877eec2f63b931bd47417a81a538327af927da3e",
			"ddaf35a193617abacc417349ae20413112e6fa4e89a97ea20a9eeee64b55d39a" +
				"2192992a274fc1a836ba3c23a3feebbd454d4423643ce80e2a9ac94fa54ca49f",
			"8e959b75dae313da8cf4f72814fc143f8f7779c6eb9f7fa17299aeadb6889018" +
				"501d289e4900f7e4331b99dec4b5433ac7d329eeb6dd26545e96e55b874be909",
		},
	},
}

// TestSHA384HasherContract runs the contract suite of crypto.Hasher on
// SHA-384 with its ID, its Algorithm and the digests of the standard
// library.
func TestSHA384HasherContract(t *testing.T) {
	t.Parallel()
	cryptotest.AssertHasherContract(t, func() crypto.Hasher { return cryptosha512.New384() },
		append(cryptotest.HasherContractAssertions(),
			cryptotest.HasherIDAssertion(sha384ID),
			cryptotest.HasherAlgorithmAssertion(crypto.AlgSHA384),
			cryptotest.HasherCrossStdlibAssertion(sha384Spec.Sum),
		)...,
	)
}

// TestSHA384HasherModel checks SHA-384 against the stdlib reference on
// every Hash and tagged call.
func TestSHA384HasherModel(t *testing.T) {
	t.Parallel()
	cryptotest.HasherModelTest(t, func() crypto.Hasher { return cryptosha512.New384() },
		cryptotest.HasherModelReference(func() crypto.Hasher {
			return cryptotest.NewStdlibHasherStub(t, sha384Spec)
		}),
		cryptotest.HasherModelExtraActions(
			cryptotest.HasherHashAction(),
			cryptotest.HasherHashTaggedAction(),
			cryptotest.HasherCombineTaggedAction(),
		),
	)
}

// TestSHA512HasherContract runs the contract suite of crypto.Hasher on
// SHA-512 with its ID, its Algorithm and the digests of the standard
// library.
func TestSHA512HasherContract(t *testing.T) {
	t.Parallel()
	cryptotest.AssertHasherContract(t, func() crypto.Hasher { return cryptosha512.New512() },
		append(cryptotest.HasherContractAssertions(),
			cryptotest.HasherIDAssertion(sha512ID),
			cryptotest.HasherAlgorithmAssertion(crypto.AlgSHA512),
			cryptotest.HasherCrossStdlibAssertion(sha512Spec.Sum),
		)...,
	)
}

// TestSHA512HasherModel checks SHA-512 against the stdlib reference on
// every Hash and tagged call.
func TestSHA512HasherModel(t *testing.T) {
	t.Parallel()
	cryptotest.HasherModelTest(t, func() crypto.Hasher { return cryptosha512.New512() },
		cryptotest.HasherModelReference(func() crypto.Hasher {
			return cryptotest.NewStdlibHasherStub(t, sha512Spec)
		}),
		cryptotest.HasherModelExtraActions(
			cryptotest.HasherHashAction(),
			cryptotest.HasherHashTaggedAction(),
			cryptotest.HasherCombineTaggedAction(),
		),
	)
}

// TestSHA512 pins the FIPS 180-4 digests of both Hashers, the digests of
// concurrent tagged calls, the message of a zero operand and the count of
// a Write.
func TestSHA512(t *testing.T) {
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

				// The FIPS 180-4 vectors are the algorithm of record.
				vectors := []struct {
					name string
					give []byte
					want string
				}{
					{name: "returns the FIPS 180-4 digest of the empty message", give: []byte{}, want: hasher.fips[0]},
					{name: `returns the FIPS 180-4 digest of "abc"`, give: []byte("abc"), want: hasher.fips[1]},
					{
						name: "returns the FIPS 180-4 digest of the message of two blocks",
						give: []byte("abcdefghbcdefghicdefghijdefghijkefghijklfghijklmghijklmnhijklmno" +
							"ijklmnopjklmnopqklmnopqrlmnopqrsmnopqrstnopqrstu"),
						want: hasher.fips[2],
					},
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

// TestSHA512Allocs checks the allocation contract of SHA-384 and SHA-512
// through the crypto.Hasher and crypto.Stream interfaces, with data on
// the heap, as a caller that must not allocate passes it. MaxAllocs
// counts the allocations of the whole process, so the test does not run
// in parallel.
func TestSHA512Allocs(t *testing.T) {
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

// BenchmarkSHA384Hasher runs the standard Hasher bench contract on
// SHA-384.
func BenchmarkSHA384Hasher(b *testing.B) {
	cryptotest.BenchmarkHasherContract(b, func() crypto.Hasher { return cryptosha512.New384() },
		cryptotest.HasherBenchOnAlgorithm(bench.PureAllocsWithin[crypto.Hasher, crypto.Algorithm](0)),
		cryptotest.HasherBenchOnID(bench.PureAllocsWithin[crypto.Hasher, crypto.ID](0)),
		cryptotest.HasherBenchOnHash(bench.PureAllocsWithin[crypto.Hasher, crypto.Digest](0)),
		cryptotest.HasherBenchOnHashTagged(bench.PureAllocsWithin[crypto.Hasher, crypto.Digest](0)),
		cryptotest.HasherBenchOnCombineTagged(bench.PureAllocsWithin[crypto.Hasher, crypto.Digest](0)),
	)
}

// BenchmarkSHA512Hasher runs the standard Hasher bench contract on
// SHA-512.
func BenchmarkSHA512Hasher(b *testing.B) {
	cryptotest.BenchmarkHasherContract(b, func() crypto.Hasher { return cryptosha512.New512() },
		cryptotest.HasherBenchOnAlgorithm(bench.PureAllocsWithin[crypto.Hasher, crypto.Algorithm](0)),
		cryptotest.HasherBenchOnID(bench.PureAllocsWithin[crypto.Hasher, crypto.ID](0)),
		cryptotest.HasherBenchOnHash(bench.PureAllocsWithin[crypto.Hasher, crypto.Digest](0)),
		cryptotest.HasherBenchOnHashTagged(bench.PureAllocsWithin[crypto.Hasher, crypto.Digest](0)),
		cryptotest.HasherBenchOnCombineTagged(bench.PureAllocsWithin[crypto.Hasher, crypto.Digest](0)),
	)
}

// FuzzSHA384HasherModel is the coverage-guided fuzz wrapper around the
// model property of SHA-384.
func FuzzSHA384HasherModel(f *testing.F) {
	cryptotest.HasherModelFuzz(f, func() crypto.Hasher { return cryptosha512.New384() },
		cryptotest.HasherModelReference(func() crypto.Hasher {
			return cryptotest.NewStdlibHasherStub(f, sha384Spec)
		}),
		cryptotest.HasherModelExtraActions(
			cryptotest.HasherHashAction(),
			cryptotest.HasherHashTaggedAction(),
			cryptotest.HasherCombineTaggedAction(),
		),
	)
}

// FuzzSHA512HasherModel is the coverage-guided fuzz wrapper around the
// model property of SHA-512.
func FuzzSHA512HasherModel(f *testing.F) {
	cryptotest.HasherModelFuzz(f, func() crypto.Hasher { return cryptosha512.New512() },
		cryptotest.HasherModelReference(func() crypto.Hasher {
			return cryptotest.NewStdlibHasherStub(f, sha512Spec)
		}),
		cryptotest.HasherModelExtraActions(
			cryptotest.HasherHashAction(),
			cryptotest.HasherHashTaggedAction(),
			cryptotest.HasherCombineTaggedAction(),
		),
	)
}
