// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package xaesgcm_test

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/fips140"
	"encoding/hex"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/conform"

	"go.thesmos.sh/core/coretest/cryptotest"
	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/aesgcm"
	"go.thesmos.sh/core/crypto/xaesgcm"
	randcrypto "go.thesmos.sh/core/rand/crypto"
)

// childTimeout is the -test.timeout of the child process of TestFIPSOnlyMode.
// The child runs its checks in milliseconds. The bound ends a child whose
// parent has died, which no context of the parent can cancel.
const childTimeout = 30 * time.Second

// The goroutines of a concurrent case, and the round trips that each makes.
const (
	goroutines = 8
	rounds     = 20
)

// The parts of the test vectors of the specification, and the sizes of the
// output of Seal.
const (
	// vectorPlaintext is the plaintext of both vectors.
	vectorPlaintext = "XAES-256-GCM"

	// vectorNonce is the 192-bit nonce of both vectors.
	vectorNonce = "ABCDEFGHIJKLMNOPQRSTUVWX"

	// halfSize is the length of each half of the nonce of XAES-256-GCM.
	halfSize = 12

	// tagSize is the length of the tag of AES-256-GCM.
	tagSize = 16

	// plaintextSize is the size of the plaintext that the allocation test
	// seals.
	plaintextSize = 1024
)

// xaesID is the build-local identifier of the AEAD of New.
var xaesID = crypto.ID{'x', 'a', 'e', 's', '-', '2', '5', '6', '-', 'g', 'c', 'm', '/', 'v', '1'}

// testKey is the key of the tests that do not use a vector.
var testKey = []byte("contract-test-key-256-bit-fixed!")

// badKeySizes generates the key lengths up to 64 that New refuses. 16 and 24
// are among them: AES accepts both, and XAES-256-GCM is defined for 32 bytes.
var badKeySizes = prop.Integer(0, 64).Filter(func(n int) bool { return n != xaesgcm.KeySize })

// vectors are the two test vectors of the specification: the byte that the key
// repeats, the associated data, the derived key Kx, and the ciphertext with its
// tag.
var vectors = []struct {
	name       string
	key        byte
	aad        string
	derived    string
	ciphertext string
}{
	{
		name:       "the vector without associated data",
		key:        0x01,
		derived:    "c8612c9ed53fe43e8e005b828a1631a0bbcb6ab2f46514ec4f439fcfd0fa969b",
		ciphertext: "ce546ef63c9cc60765923609b33a9a1974e96e52daf2fcf7075e2271",
	},
	{
		name:       "the vector with associated data",
		key:        0x03,
		aad:        "c2sp.org/XAES-256-GCM",
		derived:    "e9c621d4cdd9b11b00a6427ad7e559aeedd66b3857646677748f8ca796cb3fd8",
		ciphertext: "986ec1832593df5443a179437fd083bf3fdb41abd740a21f71eb769d",
	},
}

// sizes are the plaintext sizes of the benchmarks.
var sizes = []struct {
	name string
	n    int
}{
	{name: "16 B", n: 16},
	{name: "1 KiB", n: 1024},
	{name: "64 KiB", n: 64 * 1024},
}

// TestAEADConformsToAEAD applies the checks of the suite of crypto.AEAD to the
// AEAD of New. The second instance of a check is another AEAD over the same
// key.
func TestAEADConformsToAEAD(t *testing.T) {
	t.Parallel()

	newAEAD := func(h *cryptotest.AEADHandle) crypto.AEAD { return mustNew(h, testKey) }
	cryptotest.RunAEADSuiteFromHandle(t, newAEAD, conform.Second(newAEAD))
}

func TestXAESGCM(t *testing.T) {
	t.Parallel()

	t.Run("New", func(t *testing.T) {
		t.Parallel()

		t.Run("returns an AEAD of XAES-256-GCM for a key of 32 bytes", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, mustNew(t, testKey).Algorithm(), crypto.AlgXAES256GCM,
				"the AEAD must report the algorithm of XAES-256-GCM")
		})

		t.Run("returns ErrKeySize for a key of another length", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(n int) error {
				_, err := xaesgcm.New(make([]byte, n))

				return err
			}, crypto.ErrKeySize, "only a key of 32 bytes must be valid", prop.Using(badKeySizes),
				prop.Example(0), prop.Example(16), prop.Example(24), prop.Example(31), prop.Example(33))
		})

		t.Run("returns ErrKeySize for a nil key", func(t *testing.T) {
			t.Parallel()
			_, err := xaesgcm.New(nil)
			assert.ErrorIs(t, err, crypto.ErrKeySize, "a nil key must be refused")
		})

		t.Run("returns an AEAD with the ID of XAES-256-GCM", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, mustNew(t, testKey).ID(), xaesID, "the AEAD must have the ID of XAES-256-GCM")
		})

		t.Run("returns an AEAD whose nonce is the first half of the nonce of XAES-256-GCM", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, mustNew(t, testKey).NonceSize(), halfSize,
				"a change of the nonce size changes the wire format of every ciphertext")
		})

		t.Run("returns an AEAD whose overhead is the second half of the nonce and the tag", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, mustNew(t, testKey).Overhead(), halfSize+tagSize,
				"a change of the overhead changes the wire format of every ciphertext")
		})

		// The caller must be free to zero its key material at once.
		t.Run("returns an AEAD that a later write to the key leaves unchanged", func(t *testing.T) {
			t.Parallel()
			k := bytes.Clone(testKey)
			a := mustNew(t, k)
			sealed, err := crypto.Seal(a, randcrypto.New(), []byte("payload"), nil)
			assert.NoError(t, err, "Seal must succeed")
			clear(k)
			opened, err := crypto.Open(a, sealed, nil)
			assert.NoError(t, err, "a zeroed key must not change the AEAD")
			assert.Equal(t, opened, []byte("payload"), "Open must recover the plaintext")
		})

		t.Run("returns an AEAD that opens the envelopes that goroutines seal at once", func(t *testing.T) {
			t.Parallel()
			a := mustNew(t, testKey)
			outcomes := history.Concurrently(goroutines, 10*time.Second, func(client int) (any, error) {
				payload := []byte("payload " + strconv.Itoa(client))
				opened := make([][]byte, 0, rounds)
				for range rounds {
					sealed, err := crypto.Seal(a, randcrypto.New(), payload, nil)
					if err != nil {
						return opened, err
					}
					plain, err := crypto.Open(a, sealed, nil)
					if err != nil {
						return opened, err
					}
					opened = append(opened, plain)
				}

				return opened, nil
			})
			for _, o := range outcomes {
				assert.True(t, o.Finished, "every goroutine must finish")
				assert.NoError(t, o.Error, "every round trip must succeed")
				opened, _ := o.Output.([][]byte)
				want := slices.Repeat([][]byte{[]byte("payload " + strconv.Itoa(o.Client))}, rounds)
				assert.Equal(t, opened, want, "every envelope must open to the plaintext of its goroutine")
			}
		})
	})

	t.Run("Seal", func(t *testing.T) {
		t.Parallel()

		for _, v := range vectors {
			t.Run("returns a ciphertext that AES-256-GCM opens under the derived key of "+v.name, func(t *testing.T) {
				t.Parallel()
				a := mustNew(t, bytes.Repeat([]byte{v.key}, xaesgcm.KeySize))
				sealed := a.Seal(nil, []byte(vectorNonce[:halfSize]), []byte(vectorPlaintext), []byte(v.aad))
				opened, err := derivedGCM(t, v.derived).Open(nil, sealed[:halfSize], sealed[halfSize:], []byte(v.aad))
				assert.NoError(t, err, "AES-256-GCM under the derived key must open the output of Seal")
				assert.Equal(t, string(opened), vectorPlaintext, "AES-256-GCM must recover the plaintext")
			})
		}

		t.Run("returns another second half of the nonce for each call", func(t *testing.T) {
			t.Parallel()
			a := mustNew(t, testKey)
			nonce := make([]byte, halfSize)
			first, second := a.Seal(nil, nonce, nil, nil), a.Seal(nil, nonce, nil, nil)
			assert.NotEqual(t, first[:halfSize], second[:halfSize],
				"the FIPS 140-3 module must generate the second half of each nonce")
		})

		for _, n := range []int{0, 11, 13, 24} {
			t.Run("panics for a nonce of "+strconv.Itoa(n)+" bytes", func(t *testing.T) {
				t.Parallel()
				a := mustNew(t, testKey)
				assert.Panics(t, func() { a.Seal(nil, make([]byte, n), nil, nil) },
					"Seal must refuse a nonce of another length, as cipher.AEAD documents")
			})
		}
	})

	t.Run("Open", func(t *testing.T) {
		t.Parallel()

		for _, v := range vectors {
			t.Run("returns the plaintext of "+v.name, func(t *testing.T) {
				t.Parallel()
				a := mustNew(t, bytes.Repeat([]byte{v.key}, xaesgcm.KeySize))
				sealed := append([]byte(vectorNonce[halfSize:]), decodeHex(t, v.ciphertext)...)
				opened, err := a.Open(nil, []byte(vectorNonce[:halfSize]), sealed, []byte(v.aad))
				assert.NoError(t, err, "Open must open the ciphertext of the vector")
				assert.Equal(t, string(opened), vectorPlaintext, "Open must recover the plaintext of the vector")
			})
		}

		t.Run("returns an error for a ciphertext shorter than the nonce half and the tag", func(t *testing.T) {
			t.Parallel()
			_, err := mustNew(t, testKey).Open(nil, make([]byte, halfSize), make([]byte, halfSize+tagSize-1), nil)
			assert.HasError(t, err, "Open must refuse a ciphertext without room for the nonce half and the tag")
		})

		for _, n := range []int{0, 11, 13, 24} {
			t.Run("panics for a nonce of "+strconv.Itoa(n)+" bytes", func(t *testing.T) {
				t.Parallel()
				a := mustNew(t, testKey)
				assert.Panics(t, func() { _, _ = a.Open(nil, make([]byte, n), make([]byte, halfSize+tagSize), nil) },
					"Open must refuse a nonce of another length, as cipher.AEAD documents")
			})
		}
	})
}

// TestFIPSOnlyMode checks New, Seal and Open under GODEBUG=fips140=only. The
// mode is fixed when a process starts, so the test runs itself again in a child
// process with the mode set. In the child, fips140.Enforced reports true and
// the checks run there.
func TestFIPSOnlyMode(t *testing.T) {
	t.Parallel()

	if !fips140.Enforced() {
		t.Run("passes in a child process under fips140=only", func(t *testing.T) {
			t.Parallel()
			cmd := exec.CommandContext(t.Context(), os.Args[0],
				"-test.run=^TestFIPSOnlyMode$", "-test.v", "-test.timeout="+childTimeout.String())
			cmd.Env = append(os.Environ(), "GODEBUG=fips140=only")
			out, err := cmd.CombinedOutput()
			assert.NoError(t, err, "the fips140=only child must pass:\n"+string(out))
			assert.Contains(t, string(out), "New_seals_and_opens", "the child must run the FIPS checks")
		})

		return
	}

	t.Run("New seals and opens", func(t *testing.T) {
		t.Parallel()
		a := mustNew(t, testKey)
		assert.RoundTrip(t, func(p []byte) ([]byte, error) { return crypto.Seal(a, randcrypto.New(), p, nil) },
			func(sealed []byte) ([]byte, error) { return crypto.Open(a, sealed, nil) },
			[]byte("payload"), "Open must recover the plaintext that Seal sealed in FIPS 140-only mode")
	})
}

// TestXAESGCMAllocs checks that Seal and Open of a plaintext of 1 KiB into a
// buffer of enough capacity allocate the AES-256 block and the GCM state of the
// derived key, and nothing else. MaxAllocs counts the allocations of the whole
// process, so the test does not run in parallel.
func TestXAESGCMAllocs(t *testing.T) {
	a := mustNew(t, testKey)
	nonce := make([]byte, xaesgcm.NonceSize)
	plaintext := make([]byte, plaintextSize)
	sealed := a.Seal(nil, nonce, plaintext, nil)

	t.Run("Seal", func(t *testing.T) {
		dst := make([]byte, 0, len(sealed))
		expect.MaxAllocs(t, func() { _ = a.Seal(dst, nonce, plaintext, nil) }, 2,
			"Seal must allocate only the AES-256 block and the GCM state of the derived key")
	})

	t.Run("Open", func(t *testing.T) {
		dst := make([]byte, 0, plaintextSize)
		var err error
		expect.MaxAllocs(t, func() { _, err = a.Open(dst, nonce, sealed, nil) }, 2,
			"Open must allocate only the AES-256 block and the GCM state of the derived key")
		assert.NoError(t, err, "the test measures an Open that succeeds")
	})
}

// BenchmarkXAESGCM measures Seal and Open of 16 B, 1 KiB and 64 KiB, for
// XAES-256-GCM and for AES-256-GCM with the nonces of the FIPS 140-3 module.
func BenchmarkXAESGCM(b *testing.B) {
	xaes := mustNew(b, testKey)
	aes256, err := aesgcm.NewRandomNonce(testKey)
	assert.NoError(b, err, "aesgcm must accept a key of 32 bytes")

	constructions := []struct {
		name   string
		a      crypto.AEAD
		allocs uint64
	}{
		{name: "XAES-256-GCM", a: xaes, allocs: 2},
		{name: "AES-256-GCM", a: aes256, allocs: 0},
	}

	b.Run("Seal", func(b *testing.B) {
		for _, s := range sizes {
			b.Run(s.name, func(b *testing.B) {
				for _, c := range constructions {
					b.Run(c.name, func(b *testing.B) {
						nonce := make([]byte, c.a.NonceSize())
						plaintext := make([]byte, s.n)
						dst := make([]byte, 0, s.n+c.a.Overhead())
						bc := bench.Start(b).MaxAllocs(c.allocs)
						defer bc.End()

						var got []byte
						for bc.Loop() {
							got = c.a.Seal(dst, nonce, plaintext, nil)
						}

						assert.Length(b, got, s.n+c.a.Overhead(),
							"the benchmark measures a Seal of the whole plaintext")
					})
				}
			})
		}
	})

	b.Run("Open", func(b *testing.B) {
		for _, s := range sizes {
			b.Run(s.name, func(b *testing.B) {
				for _, c := range constructions {
					b.Run(c.name, func(b *testing.B) {
						nonce := make([]byte, c.a.NonceSize())
						sealed := c.a.Seal(nil, nonce, make([]byte, s.n), nil)
						dst := make([]byte, 0, s.n)
						bc := bench.Start(b).MaxAllocs(c.allocs)
						defer bc.End()

						var err error
						for bc.Loop() {
							_, err = c.a.Open(dst, nonce, sealed, nil)
						}

						assert.NoError(b, err, "the benchmark measures an Open that succeeds")
					})
				}
			})
		}
	})
}

// mustNew returns the AEAD of New over key. It fails tb when New refuses the
// key.
func mustNew(tb assert.TB, key []byte) crypto.AEAD {
	tb.Helper()

	a, err := xaesgcm.New(key)
	assert.NoError(tb, err, "New must accept a key of 32 bytes")

	return a
}

// derivedGCM returns AES-256-GCM of the standard library, with nonces from the
// caller, under the key that the hexadecimal derived encodes. It fails tb when
// the key does not decode or the standard library refuses it.
func derivedGCM(tb assert.TB, derived string) cipher.AEAD {
	tb.Helper()

	block, err := aes.NewCipher(decodeHex(tb, derived))
	assert.NoError(tb, err, "AES must accept the derived key of 32 bytes")
	gcm, err := cipher.NewGCM(block)
	assert.NoError(tb, err, "GCM must accept the block cipher of AES")

	return gcm
}

// decodeHex returns the bytes that s encodes. It fails tb when s is not
// hexadecimal.
func decodeHex(tb assert.TB, s string) []byte {
	tb.Helper()

	b, err := hex.DecodeString(s)
	assert.NoError(tb, err, "the vector must be hexadecimal")

	return b
}
