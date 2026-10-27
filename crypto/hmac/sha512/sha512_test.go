// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package sha512_test

import (
	"bytes"
	stdsha512 "crypto/sha512"
	"encoding/hex"
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

// testKey is the HMAC key of the MACs under test and of their references, so
// both compute the same tags.
var testKey = []byte("contract-test-key")

// sha384Spec is the HMAC-SHA-384 of the standard library under testKey, the
// reference of the conformance test of MAC384.
var sha384Spec = cryptotest.StdlibMACSpec{
	Algorithm: crypto.AlgHMACSHA384,
	ID:        hmacSHA384ID,
	Size:      crypto.DigestSize384,
	Key:       testKey,
	NewHash:   stdsha512.New384,
}

// sha512Spec is the HMAC-SHA-512 of the standard library under testKey, the
// reference of the conformance test of MAC512.
var sha512Spec = cryptotest.StdlibMACSpec{
	Algorithm: crypto.AlgHMACSHA512,
	ID:        hmacSHA512ID,
	Size:      crypto.DigestSize512,
	Key:       testKey,
	NewHash:   stdsha512.New,
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

// macs are the two MACs of the package, with the ID, the Algorithm and the size
// of each, and their tags of the RFC 4231 inputs in the order of
// rfc4231Inputs.
var macs = []struct {
	name      string
	new       func(key []byte) crypto.MAC
	id        crypto.ID
	algorithm crypto.Algorithm
	size      int
	want      [5]string
}{
	{
		name:      "MAC384",
		new:       func(key []byte) crypto.MAC { return hmacsha512.NewSHA384(key) },
		id:        hmacSHA384ID,
		algorithm: crypto.AlgHMACSHA384,
		size:      crypto.DigestSize384,
		want: [5]string{
			"afd03944d84895626b0825f4ab46907f15f9dadbe4101ec682aa034c7cebc59cfaea9ea9076ede7f4af152e8b2fa9cb6",
			"af45d2e376484031617f78d2b58a6b1b9c7ef464f5a01b47e42ec3736322445e8e2240ca5e69e2c78b3239ecfab21649",
			"88062608d3e6ad8a0aa2ace014c8a86f0aa635d947ac9febe83ef4e55966144b2a5ab39dc13814b94e3ab6e101a34f27",
			"4ece084485813e9088d2c63a041bc5b44f9ef1012a2b588f3cd11f05033ac4c60c2ef6ab4030fe8296248df163f44952",
			"6617178e941f020d351e2f254e8fd32c602420feb0b8fb9adccebb82461e99c5a678cc31e799176d3860e6110c46523e",
		},
	},
	{
		name:      "MAC512",
		new:       func(key []byte) crypto.MAC { return hmacsha512.NewSHA512(key) },
		id:        hmacSHA512ID,
		algorithm: crypto.AlgHMACSHA512,
		size:      crypto.DigestSize512,
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

func TestSHA512(t *testing.T) {
	t.Parallel()

	for _, mac := range macs {
		t.Run(mac.name, func(t *testing.T) {
			t.Parallel()

			t.Run("ID", func(t *testing.T) {
				t.Parallel()

				t.Run("returns the ID of the MAC", func(t *testing.T) {
					t.Parallel()
					assert.Equal(t, mac.new(testKey).ID(), mac.id, "ID must return the ID of the MAC")
				})
			})

			t.Run("Algorithm", func(t *testing.T) {
				t.Parallel()

				t.Run("returns the name of the algorithm of the MAC", func(t *testing.T) {
					t.Parallel()
					assert.Equal(t, mac.new(testKey).Algorithm(), mac.algorithm,
						"Algorithm must return the name of the algorithm of the MAC")
				})
			})

			t.Run("Size", func(t *testing.T) {
				t.Parallel()

				t.Run("returns the size of a tag of the MAC", func(t *testing.T) {
					t.Parallel()
					assert.Equal(t, mac.new(testKey).Size(), mac.size, "Size must return the size of a tag of the MAC")
				})
			})

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

// newMAC384 returns the HMAC-SHA-384 under testKey. The conformance test of
// the package runs the suite of crypto.MAC on it.
func newMAC384() *hmacsha512.MAC384 {
	return hmacsha512.NewSHA384(testKey)
}

// mac384Options returns the options of the conformance test of MAC384: the
// HMAC-SHA-384 of the standard library under testKey as the reference.
func mac384Options() []cryptotest.MACOption {
	return []cryptotest.MACOption{
		conform.Reference(func(h *cryptotest.MACHandle) crypto.MAC {
			return cryptotest.NewStdlibMACStub(h, sha384Spec)
		}),
	}
}

// newMAC512 returns the HMAC-SHA-512 under testKey. The conformance test of
// the package runs the suite of crypto.MAC on it.
func newMAC512() *hmacsha512.MAC512 {
	return hmacsha512.NewSHA512(testKey)
}

// mac512Options returns the options of the conformance test of MAC512: the
// HMAC-SHA-512 of the standard library under testKey as the reference.
func mac512Options() []cryptotest.MACOption {
	return []cryptotest.MACOption{
		conform.Reference(func(h *cryptotest.MACHandle) crypto.MAC {
			return cryptotest.NewStdlibMACStub(h, sha512Spec)
		}),
	}
}
