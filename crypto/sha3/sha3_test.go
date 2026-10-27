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
	"go.dokimi.dev/conform"

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

// sha3_256Spec is the spec of the SHA3-256 of the standard library, the
// reference of the conformance test of Hasher256.
var sha3_256Spec = cryptotest.StdlibHasherSpec{
	Algorithm: crypto.AlgSHA3_256,
	ID:        sha3_256ID,
	Sum:       func(d []byte) []byte { h := sha3.Sum256(d); return h[:] },
	NewHash:   func() hash.Hash { return sha3.New256() },
}

// sha3_384Spec is the spec of the SHA3-384 of the standard library, the
// reference of the conformance test of Hasher384.
var sha3_384Spec = cryptotest.StdlibHasherSpec{
	Algorithm: crypto.AlgSHA3_384,
	ID:        sha3_384ID,
	Sum:       func(d []byte) []byte { h := sha3.Sum384(d); return h[:] },
	NewHash:   func() hash.Hash { return sha3.New384() },
}

// sha3_512Spec is the spec of the SHA3-512 of the standard library, the
// reference of the conformance test of Hasher512.
var sha3_512Spec = cryptotest.StdlibHasherSpec{
	Algorithm: crypto.AlgSHA3_512,
	ID:        sha3_512ID,
	Sum:       func(d []byte) []byte { h := sha3.Sum512(d); return h[:] },
	NewHash:   func() hash.Hash { return sha3.New512() },
}

// hashers are the three Hashers of the package, with the spec of their
// reference and their FIPS 202 vectors of the empty message and of "abc".
var hashers = []struct {
	name string
	h    crypto.Hasher
	spec cryptotest.StdlibHasherSpec
	fips [2]string
}{
	{
		name: "Hasher256",
		h:    cryptosha3.New256(),
		spec: sha3_256Spec,
		fips: [2]string{
			"a7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a",
			"3a985da74fe225b2045c172d6bd390bd855f086e3e9d525b46bfe24511431532",
		},
	},
	{
		name: "Hasher384",
		h:    cryptosha3.New384(),
		spec: sha3_384Spec,
		fips: [2]string{
			"0c63a75b845e4f7d01107d852e4c2485c51a50aaaa94fc61995e71bbee983a2ac3713831264adb47fb6bd1e058d5f004",
			"ec01498288516fc926459f58e2c6ad8df9b473cb0fc08c2596da7cf0e49be4b298d88cea927ac7f539f1edf228376d25",
		},
	},
	{
		name: "Hasher512",
		h:    cryptosha3.New512(),
		spec: sha3_512Spec,
		fips: [2]string{
			"a69f73cca23a9ac5c8b567dc185a756e97c982164fe25859e0d1dcc1475c80a6" +
				"15b2123af1f5f94c11e3e9402c3ac558f500199d95b6d3e301758586281dcd26",
			"b751850b1a57168a5693cd924b6b096e08f621827444f70d884f5d0240d2712e" +
				"10e116e9192af3c91a7ec57647e3934057340b4cf408d5a56592f8274eec53f0",
		},
	},
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

			t.Run("ID", func(t *testing.T) {
				t.Parallel()

				t.Run("returns the ID of the Hasher", func(t *testing.T) {
					t.Parallel()
					assert.Equal(t, h.ID(), hasher.spec.ID, "ID must return the ID of the Hasher")
				})
			})

			t.Run("Algorithm", func(t *testing.T) {
				t.Parallel()

				t.Run("returns the name of the algorithm of the Hasher", func(t *testing.T) {
					t.Parallel()
					assert.Equal(t, h.Algorithm(), hasher.spec.Algorithm,
						"Algorithm must return the name of the algorithm of the Hasher")
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

// hasher256Options returns the options of the conformance test of
// Hasher256: the SHA3-256 of the standard library as the reference, and a
// digest of SHA3-256 as the operand of each generated call of CombineTagged.
func hasher256Options() []cryptotest.HasherOption {
	return []cryptotest.HasherOption{
		conform.Reference(func(h *cryptotest.HasherHandle) crypto.Hasher {
			return cryptotest.NewStdlibHasherStub(h, sha3_256Spec)
		}),
		conform.Using[crypto.Hasher](prop.Just(cryptosha3.New256().Hash(nil))),
	}
}

// hasher384Options returns the options of the conformance test of
// Hasher384: the SHA3-384 of the standard library as the reference, and a
// digest of SHA3-384 as the operand of each generated call of CombineTagged.
func hasher384Options() []cryptotest.HasherOption {
	return []cryptotest.HasherOption{
		conform.Reference(func(h *cryptotest.HasherHandle) crypto.Hasher {
			return cryptotest.NewStdlibHasherStub(h, sha3_384Spec)
		}),
		conform.Using[crypto.Hasher](prop.Just(cryptosha3.New384().Hash(nil))),
	}
}

// hasher512Options returns the options of the conformance test of
// Hasher512: the SHA3-512 of the standard library as the reference, and a
// digest of SHA3-512 as the operand of each generated call of CombineTagged.
func hasher512Options() []cryptotest.HasherOption {
	return []cryptotest.HasherOption{
		conform.Reference(func(h *cryptotest.HasherHandle) crypto.Hasher {
			return cryptotest.NewStdlibHasherStub(h, sha3_512Spec)
		}),
		conform.Using[crypto.Hasher](prop.Just(cryptosha3.New512().Hash(nil))),
	}
}
