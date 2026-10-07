// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package sha3_test

import (
	"bytes"
	stdhmac "crypto/hmac"
	stdsha3 "crypto/sha3"
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
	hmacsha3 "go.thesmos.sh/core/crypto/hmac/sha3"
)

// The goroutines of a concurrent case, and the tags that each computes.
const (
	goroutines = 8
	rounds     = 20
)

// Canonical build-local IDs.
var (
	hmacSHA3256ID = crypto.ID{
		'h', 'm', 'a', 'c', '-', 's', 'h', 'a', '3', '-', '2', '5', '6', '/', 'v', '1',
	}
	hmacSHA3384ID = crypto.ID{
		'h', 'm', 'a', 'c', '-', 's', 'h', 'a', '3', '-', '3', '8', '4', '/', 'v', '1',
	}
	hmacSHA3512ID = crypto.ID{
		'h', 'm', 'a', 'c', '-', 's', 'h', 'a', '3', '-', '5', '1', '2', '/', 'v', '1',
	}
)

// testKey is the shared HMAC key for SUT and reference. Both
// sides must construct with the same key for byte-exact
// equivalence.
var testKey = []byte("contract-test-key")

// The stdlib SHA-3 constructors return *sha3.SHA3, and crypto/hmac.New
// takes a constructor of a hash.Hash.
var (
	stdNewSHA3256 = func() hash.Hash { return stdsha3.New256() }
	stdNewSHA3384 = func() hash.Hash { return stdsha3.New384() }
	stdNewSHA3512 = func() hash.Hash { return stdsha3.New512() }
)

// rfc4231Inputs are the keys and data of RFC 4231 cases 4.2, 4.3 and
// 4.4. Case 4.5 truncates the output, which the MACs never do, and the
// keys of cases 4.6 and 4.7 are in the sweep of the contract suite.
var rfc4231Inputs = []struct {
	name string
	key  []byte
	data []byte
}{
	{name: "4.2", key: bytes.Repeat([]byte{0x0b}, 20), data: []byte("Hi There")},
	{name: "4.3", key: []byte("Jefe"), data: []byte("what do ya want for nothing?")},
	{name: "4.4", key: bytes.Repeat([]byte{0xaa}, 20), data: bytes.Repeat([]byte{0xdd}, 50)},
}

// macs are the three MACs of the package, with the stdlib reference of
// the model and their tags of the RFC 4231 inputs in the order of
// rfc4231Inputs. The tags come from crypto/hmac over crypto/sha3, and
// match the NIST CAVP outputs of HMAC-SHA-3 for the same inputs.
var macs = []struct {
	name string
	new  func(key []byte) crypto.MAC
	spec cryptotest.StdlibMACSpec
	want [3]string
}{
	{
		name: "MAC256",
		new:  func(key []byte) crypto.MAC { return hmacsha3.NewSHA3_256(key) },
		spec: cryptotest.StdlibMACSpec{
			Algorithm: crypto.AlgHMACSHA3_256, ID: hmacSHA3256ID, Size: crypto.DigestSize256,
			Key: testKey, NewHash: stdNewSHA3256,
		},
		want: [3]string{
			"ba85192310dffa96e2a3a40e69774351140bb7185e1202cdcc917589f95e16bb",
			"c7d4072e788877ae3596bbb0da73b887c9171f93095b294ae857fbe2645e1ba5",
			"84ec79124a27107865cedd8bd82da9965e5ed8c37b0ac98005a7f39ed58a4207",
		},
	},
	{
		name: "MAC384",
		new:  func(key []byte) crypto.MAC { return hmacsha3.NewSHA3_384(key) },
		spec: cryptotest.StdlibMACSpec{
			Algorithm: crypto.AlgHMACSHA3_384, ID: hmacSHA3384ID, Size: crypto.DigestSize384,
			Key: testKey, NewHash: stdNewSHA3384,
		},
		want: [3]string{
			"68d2dcf7fd4ddd0a2240c8a437305f61fb7334cfb5d0226e1bc27dc10a2e723a20d370b47743130e26ac7e3d532886bd",
			"f1101f8cbf9766fd6764d2ed61903f21ca9b18f57cf3e1a23ca13508a93243ce48c045dc007f26a21b3f5e0e9df4c20a",
			"275cd0e661bb8b151c64d288f1f782fb91a8abd56858d72babb2d476f0458373b41b6ab5bf174bec422e53fc3135ac6e",
		},
	},
	{
		name: "MAC512",
		new:  func(key []byte) crypto.MAC { return hmacsha3.NewSHA3_512(key) },
		spec: cryptotest.StdlibMACSpec{
			Algorithm: crypto.AlgHMACSHA3_512, ID: hmacSHA3512ID, Size: crypto.DigestSize512,
			Key: testKey, NewHash: stdNewSHA3512,
		},
		want: [3]string{
			"eb3fbd4b2eaab8f5c504bd3a41465aacec15770a7cabac531e482f860b5ec7ba" +
				"47ccb2c6f2afce8f88d22b6dc61380f23a668fd3888bb80537c0a0b86407689e",
			"5a4bfeab6166427c7a3647b747292b8384537cdb89afb3bf5665e4c5e709350b" +
				"287baec921fd7ca0ee7a0c31d022a95e1fc92ba9d77df883960275beb4e62024",
			"309e99f9ec075ec6c6d475eda1180687fcf1531195802a99b5677449a8625182" +
				"851cb332afb6a89c411325fbcbcd42afcb7b6e5aab7ea42c660f97fd8584bf03",
		},
	},
}

// TestHMACSHA3Contract runs the contract suite of crypto.MAC on each MAC
// with its ID, its Algorithm, its size and the tags of the standard
// library.
func TestHMACSHA3Contract(t *testing.T) {
	t.Parallel()

	for _, mac := range macs {
		t.Run(mac.name, func(t *testing.T) {
			t.Parallel()
			cryptotest.AssertMACContract(t, func() crypto.MAC { return mac.new(testKey) },
				append(cryptotest.MACContractAssertions(),
					cryptotest.MACIDAssertion(mac.spec.ID),
					cryptotest.MACAlgorithmAssertion(mac.spec.Algorithm),
					cryptotest.MACSizeAssertion(mac.spec.Size),
					cryptotest.MACCrossStdlibAssertion(func(data []byte) []byte {
						h := stdhmac.New(mac.spec.NewHash, testKey)
						_, _ = h.Write(data)

						return h.Sum(nil)
					}),
				)...,
			)
		})
	}
}

// TestHMACSHA3Model checks each MAC against the stdlib reference on
// every Sign and Verify.
func TestHMACSHA3Model(t *testing.T) {
	t.Parallel()

	for _, mac := range macs {
		t.Run(mac.name, func(t *testing.T) {
			t.Parallel()
			cryptotest.MACModelTest(t, func() crypto.MAC { return mac.new(testKey) },
				cryptotest.MACModelReference(func() crypto.MAC {
					return cryptotest.NewStdlibMACStub(t, mac.spec)
				}),
				cryptotest.MACModelExtraActions(
					cryptotest.MACSignAction(),
					cryptotest.MACVerifyAction(),
				),
			)
		})
	}
}

func TestSHA3(t *testing.T) {
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
					assert.Pure(t, func() crypto.Digest { return m.Sign([]byte("payload")) }, func() {
						for i := range key {
							key[i] = 0xff
						}
					}, "a write to key must not change the tag")
				})
			})

			t.Run("Sign", func(t *testing.T) {
				t.Parallel()

				for i, in := range rfc4231Inputs {
					t.Run("returns the tag of RFC 4231 case "+in.name, func(t *testing.T) {
						t.Parallel()
						assert.Equal(t, hex.EncodeToString(mac.new(in.key).Sign(in.data).Bytes()), mac.want[i],
							"Sign must match the recorded tag")
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

// TestSHA3Allocs checks that Sign, Verify and a pooled stream of each MAC
// allocate nothing on the warm path, with data on the heap. MaxAllocs
// counts the allocations of the whole process, so the test does not run
// in parallel.
//
//nolint:paralleltest // see above
func TestSHA3Allocs(t *testing.T) {
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

// BenchmarkHMACSHA3 runs the benchmarks of the contract suite of
// crypto.MAC on each MAC.
func BenchmarkHMACSHA3(b *testing.B) {
	for _, mac := range macs {
		b.Run(mac.name, func(b *testing.B) {
			cryptotest.BenchmarkMACContract(b, func() crypto.MAC { return mac.new(testKey) },
				cryptotest.MACBenchOnAlgorithm(bench.PureAllocsWithin[crypto.MAC, crypto.Algorithm](0)),
				cryptotest.MACBenchOnID(bench.PureAllocsWithin[crypto.MAC, crypto.ID](0)),
				cryptotest.MACBenchOnSize(bench.PureAllocsWithin[crypto.MAC, int](0)),
				cryptotest.MACBenchOnSign(
					bench.PureAllocsWithin[crypto.MAC, crypto.Digest](0),
					bench.PureConcurrentThroughput[crypto.MAC, crypto.Digest](32),
				),
				cryptotest.MACBenchOnVerify(bench.PredicateAllocsWithin[crypto.MAC](0)),
			)
		})
	}
}

// FuzzHMACSHA3256Model is the coverage-guided fuzz wrapper around the
// model property of HMAC-SHA3-256.
func FuzzHMACSHA3256Model(f *testing.F) {
	cryptotest.MACModelFuzz(f, func() crypto.MAC { return hmacsha3.NewSHA3_256(testKey) },
		cryptotest.MACModelReference(func() crypto.MAC {
			return cryptotest.NewStdlibMACStub(f, macs[0].spec)
		}),
		cryptotest.MACModelExtraActions(
			cryptotest.MACSignAction(),
			cryptotest.MACVerifyAction(),
		),
	)
}

// FuzzHMACSHA3384Model is the coverage-guided fuzz wrapper around the
// model property of HMAC-SHA3-384.
func FuzzHMACSHA3384Model(f *testing.F) {
	cryptotest.MACModelFuzz(f, func() crypto.MAC { return hmacsha3.NewSHA3_384(testKey) },
		cryptotest.MACModelReference(func() crypto.MAC {
			return cryptotest.NewStdlibMACStub(f, macs[1].spec)
		}),
		cryptotest.MACModelExtraActions(
			cryptotest.MACSignAction(),
			cryptotest.MACVerifyAction(),
		),
	)
}

// FuzzHMACSHA3512Model is the coverage-guided fuzz wrapper around the
// model property of HMAC-SHA3-512.
func FuzzHMACSHA3512Model(f *testing.F) {
	cryptotest.MACModelFuzz(f, func() crypto.MAC { return hmacsha3.NewSHA3_512(testKey) },
		cryptotest.MACModelReference(func() crypto.MAC {
			return cryptotest.NewStdlibMACStub(f, macs[2].spec)
		}),
		cryptotest.MACModelExtraActions(
			cryptotest.MACSignAction(),
			cryptotest.MACVerifyAction(),
		),
	)
}
