// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package sha256_test

import (
	"bytes"
	stdhmac "crypto/hmac"
	stdsha256 "crypto/sha256"
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
	hmacsha256 "go.thesmos.sh/core/crypto/hmac/sha256"
)

// The goroutines of a concurrent case, and the tags that each computes.
const (
	goroutines = 8
	rounds     = 20
)

// hmacSHA256ID is the canonical build-local identifier.
var hmacSHA256ID = crypto.ID{'h', 'm', 'a', 'c', '-', 's', 'h', 'a', '2', '5', '6', '/', 'v', '1'}

// testKey is the shared HMAC key for SUT and reference. Both
// sides must construct with the same key for byte-exact
// equivalence.
var testKey = []byte("contract-test-key")

// stdlibSpec describes the stdlib HMAC-SHA-256 reference of the model.
var stdlibSpec = cryptotest.StdlibMACSpec{
	Algorithm: crypto.AlgHMACSHA256,
	ID:        hmacSHA256ID,
	Size:      crypto.DigestSize256,
	Key:       testKey,
	NewHash:   func() hash.Hash { return stdsha256.New() },
}

// TestHMACSHA256Contract runs the contract suite of crypto.MAC with the
// ID, the Algorithm, the size and the tags of the standard library.
func TestHMACSHA256Contract(t *testing.T) {
	t.Parallel()
	cryptotest.AssertMACContract(t, func() crypto.MAC { return hmacsha256.New(testKey) },
		append(cryptotest.MACContractAssertions(),
			cryptotest.MACIDAssertion(hmacSHA256ID),
			cryptotest.MACAlgorithmAssertion(crypto.AlgHMACSHA256),
			cryptotest.MACSizeAssertion(crypto.DigestSize256),
			cryptotest.MACCrossStdlibAssertion(func(data []byte) []byte {
				h := stdhmac.New(stdsha256.New, testKey)
				_, _ = h.Write(data)

				return h.Sum(nil)
			}),
		)...,
	)
}

// TestHMACSHA256Model checks the MAC against the stdlib reference on
// every Sign and Verify.
func TestHMACSHA256Model(t *testing.T) {
	t.Parallel()
	cryptotest.MACModelTest(t, func() crypto.MAC { return hmacsha256.New(testKey) },
		cryptotest.MACModelReference(func() crypto.MAC {
			return cryptotest.NewStdlibMACStub(t, stdlibSpec)
		}),
		cryptotest.MACModelExtraActions(
			cryptotest.MACSignAction(),
			cryptotest.MACVerifyAction(),
		),
	)
}

func TestMAC(t *testing.T) {
	t.Parallel()

	t.Run("New", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a MAC over a copy of key", func(t *testing.T) {
			t.Parallel()
			key := []byte("mutable-key")
			m := hmacsha256.New(key)
			assert.Pure(t, func() crypto.Digest { return m.Sign([]byte("payload")) }, func() {
				for i := range key {
					key[i] = 0xff
				}
			}, "a write to key must not change the tag")
		})
	})

	t.Run("Sign", func(t *testing.T) {
		t.Parallel()

		// RFC 4231 is the algorithm of record. Its case 4.5 truncates
		// the output, which the MAC never does.
		vectors := []struct {
			name string
			key  []byte
			data []byte
			want string
		}{
			{
				name: "returns the tag of RFC 4231 case 4.2",
				key:  bytes.Repeat([]byte{0x0b}, 20),
				data: []byte("Hi There"),
				want: "b0344c61d8db38535ca8afceaf0bf12b881dc200c9833da726e9376c2e32cff7",
			},
			{
				name: "returns the tag of RFC 4231 case 4.3",
				key:  []byte("Jefe"),
				data: []byte("what do ya want for nothing?"),
				want: "5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843",
			},
			{
				name: "returns the tag of RFC 4231 case 4.4",
				key:  bytes.Repeat([]byte{0xaa}, 20),
				data: bytes.Repeat([]byte{0xdd}, 50),
				want: "773ea91e36800e46854db8ebd09181a72959098b3ef8c122d9635514ced565fe",
			},
			{
				name: "returns the tag of RFC 4231 case 4.6",
				key:  bytes.Repeat([]byte{0xaa}, 131),
				data: []byte("Test Using Larger Than Block-Size Key - Hash Key First"),
				want: "60e431591ee0b67f0d8a26aacbf5b77f8e0bc6213728c5140546040f0ee37f54",
			},
			{
				name: "returns the tag of RFC 4231 case 4.7",
				key:  bytes.Repeat([]byte{0xaa}, 131),
				data: []byte("This is a test using a larger than block-size key and a larger than block-size data. " +
					"The key needs to be hashed before being used by the HMAC algorithm."),
				want: "9b09ffa71b942fcb27635fbcd5b0e944bfdc63644f0713938a7f51535c3a35e2",
			},
		}
		for _, tt := range vectors {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, hex.EncodeToString(hmacsha256.New(tt.key).Sign(tt.data).Bytes()), tt.want,
					"Sign must match the RFC 4231 vector")
			})
		}

		t.Run("returns the tag of one call to goroutines that sign at once", func(t *testing.T) {
			t.Parallel()
			m := hmacsha256.New(testKey)
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

		// Each Verify computes its own tag, whatever the pooled state
		// of the Sign before it contains.
		t.Run("reports false for the tag of other data", func(t *testing.T) {
			t.Parallel()
			m := hmacsha256.New(testKey)
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
			m := hmacsha256.New(testKey)
			prop.Equal(t, func(p []byte) int {
				n, _ := m.NewStream().Write(p)

				return n
			}, func(p []byte) int { return len(p) }, "Write must report the length of p",
				prop.Using(prop.Bytes(prop.MaxSize(256))))
		})

		t.Run("returns Streams of the tags of Sign to goroutines that stream at once", func(t *testing.T) {
			t.Parallel()
			m := hmacsha256.New(testKey)
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
}

// TestMACAllocs checks that Sign, Verify and a pooled stream allocate
// nothing on the warm path, with data on the heap. MaxAllocs counts the
// allocations of the whole process, so the test does not run in
// parallel.
func TestMACAllocs(t *testing.T) {
	m := hmacsha256.New(testKey)
	data := make([]byte, 1024)
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
}

// BenchmarkHMACSHA256 runs the benchmarks of the contract suite of
// crypto.MAC.
func BenchmarkHMACSHA256(b *testing.B) {
	cryptotest.BenchmarkMACContract(b, func() crypto.MAC { return hmacsha256.New(testKey) },
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

// FuzzHMACSHA256Model is the coverage-guided fuzz wrapper around the
// model property.
func FuzzHMACSHA256Model(f *testing.F) {
	cryptotest.MACModelFuzz(f, func() crypto.MAC { return hmacsha256.New(testKey) },
		cryptotest.MACModelReference(func() crypto.MAC {
			return cryptotest.NewStdlibMACStub(f, stdlibSpec)
		}),
		cryptotest.MACModelExtraActions(
			cryptotest.MACSignAction(),
			cryptotest.MACVerifyAction(),
		),
	)
}
