// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package sha512_test

import (
	"bytes"
	stdhmac "crypto/hmac"
	stdsha512 "crypto/sha512"
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
	hmacsha512 "go.thesmos.sh/core/crypto/hmac/sha512"
)

// The goroutines of a concurrent case, and the tags that each computes.
const (
	goroutines = 8
	rounds     = 20
)

// Canonical build-local IDs.
var (
	hmacSHA384ID = crypto.ID{'h', 'm', 'a', 'c', '-', 's', 'h', 'a', '3', '8', '4', '/', 'v', '1'}
	hmacSHA512ID = crypto.ID{'h', 'm', 'a', 'c', '-', 's', 'h', 'a', '5', '1', '2', '/', 'v', '1'}
)

// testKey is the shared HMAC key for SUT and reference. Both
// sides must construct with the same key for byte-exact
// equivalence.
var testKey = []byte("contract-test-key")

// sha384Spec describes the stdlib HMAC-SHA-384 reference of the model.
var sha384Spec = cryptotest.StdlibMACSpec{
	Algorithm: crypto.AlgHMACSHA384,
	ID:        hmacSHA384ID,
	Size:      crypto.DigestSize384,
	Key:       testKey,
	NewHash:   func() hash.Hash { return stdsha512.New384() },
}

// sha512Spec describes the stdlib HMAC-SHA-512 reference of the model.
var sha512Spec = cryptotest.StdlibMACSpec{
	Algorithm: crypto.AlgHMACSHA512,
	ID:        hmacSHA512ID,
	Size:      crypto.DigestSize512,
	Key:       testKey,
	NewHash:   func() hash.Hash { return stdsha512.New() },
}

// rfc4231Inputs are the keys and data of RFC 4231 cases 4.2, 4.3, 4.4,
// 4.6 and 4.7. Case 4.5 truncates the output, which the MACs never do.
var rfc4231Inputs = []struct {
	name string
	key  []byte
	data []byte
}{
	{name: "4.2", key: bytes.Repeat([]byte{0x0b}, 20), data: []byte("Hi There")},
	{name: "4.3", key: []byte("Jefe"), data: []byte("what do ya want for nothing?")},
	{name: "4.4", key: bytes.Repeat([]byte{0xaa}, 20), data: bytes.Repeat([]byte{0xdd}, 50)},
	{
		name: "4.6",
		key:  bytes.Repeat([]byte{0xaa}, 131),
		data: []byte("Test Using Larger Than Block-Size Key - Hash Key First"),
	},
	{
		name: "4.7",
		key:  bytes.Repeat([]byte{0xaa}, 131),
		data: []byte("This is a test using a larger than block-size key and a larger than block-size data. " +
			"The key needs to be hashed before being used by the HMAC algorithm."),
	},
}

// macs are the two MACs of the package, with their tags of the RFC 4231
// inputs in the order of rfc4231Inputs.
var macs = []struct {
	name string
	new  func(key []byte) crypto.MAC
	want [5]string
}{
	{
		name: "MAC384",
		new:  func(key []byte) crypto.MAC { return hmacsha512.NewSHA384(key) },
		want: [5]string{
			"afd03944d84895626b0825f4ab46907f15f9dadbe4101ec682aa034c7cebc59cfaea9ea9076ede7f4af152e8b2fa9cb6",
			"af45d2e376484031617f78d2b58a6b1b9c7ef464f5a01b47e42ec3736322445e8e2240ca5e69e2c78b3239ecfab21649",
			"88062608d3e6ad8a0aa2ace014c8a86f0aa635d947ac9febe83ef4e55966144b2a5ab39dc13814b94e3ab6e101a34f27",
			"4ece084485813e9088d2c63a041bc5b44f9ef1012a2b588f3cd11f05033ac4c60c2ef6ab4030fe8296248df163f44952",
			"6617178e941f020d351e2f254e8fd32c602420feb0b8fb9adccebb82461e99c5a678cc31e799176d3860e6110c46523e",
		},
	},
	{
		name: "MAC512",
		new:  func(key []byte) crypto.MAC { return hmacsha512.NewSHA512(key) },
		want: [5]string{
			"87aa7cdea5ef619d4ff0b4241a1d6cb02379f4e2ce4ec2787ad0b30545e17cde" +
				"daa833b7d6b8a702038b274eaea3f4e4be9d914eeb61f1702e696c203a126854",
			"164b7a7bfcf819e2e395fbe73b56e0a387bd64222e831fd610270cd7ea250554" +
				"9758bf75c05a994a6d034f65f8f0e6fdcaeab1a34d4a6b4b636e070a38bce737",
			"fa73b0089d56a284efb0f0756c890be9b1b5dbdd8ee81a3655f83e33b2279d39" +
				"bf3e848279a722c806b485a47e67c807b946a337bee8942674278859e13292fb",
			"80b24263c7c1a3ebb71493c1dd7be8b49b46d1f41b4aeec1121b013783f8f352" +
				"6b56d037e05f2598bd0fd2215d6a1e5295e64f73f63f0aec8b915a985d786598",
			"e37b6a775dc87dbaa4dfa9f96e5e3ffddebd71f8867289865df5a32d20cdc944" +
				"b6022cac3c4982b10d5eeb55c3e4de15134676fb6de0446065c97440fa8c6a58",
		},
	},
}

// TestHMACSHA384Contract runs the contract suite of crypto.MAC on
// HMAC-SHA-384 with its ID, its Algorithm, its size and the tags of the
// standard library.
func TestHMACSHA384Contract(t *testing.T) {
	t.Parallel()
	cryptotest.AssertMACContract(t, func() crypto.MAC { return hmacsha512.NewSHA384(testKey) },
		append(cryptotest.MACContractAssertions(),
			cryptotest.MACIDAssertion(hmacSHA384ID),
			cryptotest.MACAlgorithmAssertion(crypto.AlgHMACSHA384),
			cryptotest.MACSizeAssertion(crypto.DigestSize384),
			cryptotest.MACCrossStdlibAssertion(func(data []byte) []byte {
				h := stdhmac.New(stdsha512.New384, testKey)
				_, _ = h.Write(data)

				return h.Sum(nil)
			}),
		)...,
	)
}

// TestHMACSHA384Model checks HMAC-SHA-384 against the stdlib reference
// on every Sign and Verify.
func TestHMACSHA384Model(t *testing.T) {
	t.Parallel()
	cryptotest.MACModelTest(t, func() crypto.MAC { return hmacsha512.NewSHA384(testKey) },
		cryptotest.MACModelReference(func() crypto.MAC {
			return cryptotest.NewStdlibMACStub(t, sha384Spec)
		}),
		cryptotest.MACModelExtraActions(
			cryptotest.MACSignAction(),
			cryptotest.MACVerifyAction(),
		),
	)
}

// TestHMACSHA512Contract runs the contract suite of crypto.MAC on
// HMAC-SHA-512 with its ID, its Algorithm, its size and the tags of the
// standard library.
func TestHMACSHA512Contract(t *testing.T) {
	t.Parallel()
	cryptotest.AssertMACContract(t, func() crypto.MAC { return hmacsha512.NewSHA512(testKey) },
		append(cryptotest.MACContractAssertions(),
			cryptotest.MACIDAssertion(hmacSHA512ID),
			cryptotest.MACAlgorithmAssertion(crypto.AlgHMACSHA512),
			cryptotest.MACSizeAssertion(crypto.DigestSize512),
			cryptotest.MACCrossStdlibAssertion(func(data []byte) []byte {
				h := stdhmac.New(stdsha512.New, testKey)
				_, _ = h.Write(data)

				return h.Sum(nil)
			}),
		)...,
	)
}

// TestHMACSHA512Model checks HMAC-SHA-512 against the stdlib reference
// on every Sign and Verify.
func TestHMACSHA512Model(t *testing.T) {
	t.Parallel()
	cryptotest.MACModelTest(t, func() crypto.MAC { return hmacsha512.NewSHA512(testKey) },
		cryptotest.MACModelReference(func() crypto.MAC {
			return cryptotest.NewStdlibMACStub(t, sha512Spec)
		}),
		cryptotest.MACModelExtraActions(
			cryptotest.MACSignAction(),
			cryptotest.MACVerifyAction(),
		),
	)
}

func TestSHA512(t *testing.T) {
	t.Parallel()

	for _, mac := range macs {
		t.Run(mac.name, func(t *testing.T) {
			t.Parallel()

			t.Run("New", func(t *testing.T) {
				t.Parallel()

				t.Run("returns a MAC over a copy of key", func(t *testing.T) {
					t.Parallel()
					key := []byte("mutable-key")
					m := mac.new(key)
					want := m.Sign([]byte("payload"))
					for i := range key {
						key[i] = 0xff
					}
					assert.Equal(t, m.Sign([]byte("payload")), want, "a write to key must not change the tag")
				})
			})

			t.Run("Sign", func(t *testing.T) {
				t.Parallel()

				// RFC 4231 is the algorithm of record.
				for i, in := range rfc4231Inputs {
					t.Run("returns the tag of RFC 4231 case "+in.name, func(t *testing.T) {
						t.Parallel()
						assert.Equal(t, hex.EncodeToString(mac.new(in.key).Sign(in.data).Bytes()), mac.want[i],
							"Sign must match the RFC 4231 vector")
					})
				}

				t.Run("returns the tag of one call to goroutines that sign at once", func(t *testing.T) {
					t.Parallel()
					m := mac.new(testKey)
					outcomes := history.Concurrently(goroutines, 10*time.Second, func(client int) (any, error) {
						data := []byte("payload " + strconv.Itoa(client))
						tags := make([]crypto.Digest, 0, rounds)
						for range rounds {
							tags = append(tags, m.Sign(data))
						}

						return tags, nil
					})
					for _, o := range outcomes {
						assert.True(t, o.Finished, "every goroutine must finish")
						tags, _ := o.Output.([]crypto.Digest)
						want := m.Sign([]byte("payload " + strconv.Itoa(o.Client)))
						assert.Equal(t, tags, slices.Repeat([]crypto.Digest{want}, rounds),
							"every tag must equal the tag of one call")
					}
				})
			})

			t.Run("Verify", func(t *testing.T) {
				t.Parallel()

				// Each Verify computes its own tag, whatever the pooled
				// state of the Sign before it contains.
				t.Run("reports false for the tag of other data", func(t *testing.T) {
					t.Parallel()
					m := mac.new(testKey)
					prop.ForAll(t, "Verify must refuse the tag of other data", func(c *prop.Case) {
						data := c.Draw(prop.Bytes(prop.MaxSize(64)), "data")
						other := c.Draw(prop.Bytes(prop.MaxSize(64)), "other")
						c.Assume(!bytes.Equal(data, other))
						assert.False(c, m.Verify(other, m.Sign(data).Bytes()), "the tag of data must not verify other")
					})
				})
			})

			t.Run("NewStream", func(t *testing.T) {
				t.Parallel()

				t.Run("returns a Stream whose Write reports every byte of p", func(t *testing.T) {
					t.Parallel()
					m := mac.new(testKey)
					prop.Equal(t, func(p []byte) int {
						n, _ := m.NewStream().Write(p)

						return n
					}, func(p []byte) int { return len(p) }, "Write must report the length of p",
						prop.Using(prop.Bytes(prop.MaxSize(256))))
				})

				t.Run("returns Streams of the tags of Sign to goroutines that stream at once", func(t *testing.T) {
					t.Parallel()
					m := mac.new(testKey)
					outcomes := history.Concurrently(goroutines, 10*time.Second, func(client int) (any, error) {
						data := []byte("payload " + strconv.Itoa(client))
						tags := make([]crypto.Digest, 0, rounds)
						for range rounds {
							s := m.NewStream()
							_, _ = s.Write(data)
							tags = append(tags, s.Sum())
							s.Close()
						}

						return tags, nil
					})
					for _, o := range outcomes {
						assert.True(t, o.Finished, "every goroutine must finish")
						tags, _ := o.Output.([]crypto.Digest)
						want := m.Sign([]byte("payload " + strconv.Itoa(o.Client)))
						assert.Equal(t, tags, slices.Repeat([]crypto.Digest{want}, rounds),
							"every Stream must return the tag of Sign")
					}
				})
			})
		})
	}
}

// TestSHA512Allocs checks that Sign, Verify and a pooled stream of each
// MAC allocate nothing on the warm path, with data on the heap. MaxAllocs
// counts the allocations of the whole process, so the test does not run
// in parallel.
//
//nolint:paralleltest // see above
func TestSHA512Allocs(t *testing.T) {
	data := make([]byte, 1024)

	for _, mac := range macs {
		t.Run(mac.name, func(t *testing.T) {
			m := mac.new(testKey)
			tag := m.Sign(data).Bytes()

			t.Run("Sign", func(t *testing.T) {
				var got crypto.Digest
				expect.MaxAllocs(t, func() { got = m.Sign(data) }, 0, "Sign must not allocate on the warm path")
				assert.Equal(t, got.Bytes(), tag, "the test must measure the tag of data")
			})

			t.Run("Verify", func(t *testing.T) {
				var ok bool
				expect.MaxAllocs(t, func() { ok = m.Verify(data, tag) }, 0, "Verify must not allocate on the warm path")
				assert.True(t, ok, "the test must measure a tag that verifies")
			})

			t.Run("NewStream", func(t *testing.T) {
				var got crypto.Digest
				expect.MaxAllocs(t, func() {
					s := m.NewStream()
					_, _ = s.Write(data)
					got = s.Sum()
					s.Close()
				}, 0, "a pooled stream must not allocate on the warm path")
				assert.Equal(t, got.Bytes(), tag, "the test must measure the tag of data")
			})
		})
	}
}

// BenchmarkHMACSHA384 runs the benchmarks of the contract suite of
// crypto.MAC on HMAC-SHA-384.
func BenchmarkHMACSHA384(b *testing.B) {
	cryptotest.BenchmarkMACContract(b, func() crypto.MAC { return hmacsha512.NewSHA384(testKey) },
		cryptotest.MACBenchOnAlgorithm(bench.PureAllocsWithin[crypto.MAC, crypto.Algorithm](0)),
		cryptotest.MACBenchOnID(bench.PureAllocsWithin[crypto.MAC, crypto.ID](0)),
		cryptotest.MACBenchOnSize(bench.PureAllocsWithin[crypto.MAC, int](0)),
		cryptotest.MACBenchOnSign(
			bench.PureAllocsWithin[crypto.MAC, crypto.Digest](0),
			bench.PureConcurrentThroughput[crypto.MAC, crypto.Digest](32),
		),
		cryptotest.MACBenchOnVerify(bench.PredicateAllocsWithin[crypto.MAC](0)),
	)
}

// BenchmarkHMACSHA512 runs the benchmarks of the contract suite of
// crypto.MAC on HMAC-SHA-512.
func BenchmarkHMACSHA512(b *testing.B) {
	cryptotest.BenchmarkMACContract(b, func() crypto.MAC { return hmacsha512.NewSHA512(testKey) },
		cryptotest.MACBenchOnAlgorithm(bench.PureAllocsWithin[crypto.MAC, crypto.Algorithm](0)),
		cryptotest.MACBenchOnID(bench.PureAllocsWithin[crypto.MAC, crypto.ID](0)),
		cryptotest.MACBenchOnSize(bench.PureAllocsWithin[crypto.MAC, int](0)),
		cryptotest.MACBenchOnSign(
			bench.PureAllocsWithin[crypto.MAC, crypto.Digest](0),
			bench.PureConcurrentThroughput[crypto.MAC, crypto.Digest](32),
		),
		cryptotest.MACBenchOnVerify(bench.PredicateAllocsWithin[crypto.MAC](0)),
	)
}

// FuzzHMACSHA384Model is the coverage-guided fuzz wrapper around the
// model property of HMAC-SHA-384.
func FuzzHMACSHA384Model(f *testing.F) {
	cryptotest.MACModelFuzz(f, func() crypto.MAC { return hmacsha512.NewSHA384(testKey) },
		cryptotest.MACModelReference(func() crypto.MAC {
			return cryptotest.NewStdlibMACStub(f, sha384Spec)
		}),
		cryptotest.MACModelExtraActions(
			cryptotest.MACSignAction(),
			cryptotest.MACVerifyAction(),
		),
	)
}

// FuzzHMACSHA512Model is the coverage-guided fuzz wrapper around the
// model property of HMAC-SHA-512.
func FuzzHMACSHA512Model(f *testing.F) {
	cryptotest.MACModelFuzz(f, func() crypto.MAC { return hmacsha512.NewSHA512(testKey) },
		cryptotest.MACModelReference(func() crypto.MAC {
			return cryptotest.NewStdlibMACStub(f, sha512Spec)
		}),
		cryptotest.MACModelExtraActions(
			cryptotest.MACSignAction(),
			cryptotest.MACVerifyAction(),
		),
	)
}
