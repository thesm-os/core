// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package aesgcm_test

import (
	"bytes"
	"crypto/fips140"
	"encoding/hex"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/history"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/coretest/cryptotest"
	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/aesgcm"
	"go.thesmos.sh/core/errs"
	randcrypto "go.thesmos.sh/core/rand/crypto"
)

// childTimeout is the -test.timeout of the child process of
// TestFIPSOnlyMode. The child runs its checks in milliseconds. The
// bound ends a child whose parent has died, which no context of the
// parent can cancel.
const childTimeout = 30 * time.Second

// The goroutines of a concurrent case, and the round trips that each
// makes.
const (
	goroutines = 8
	rounds     = 20
)

// The canonical build-local identifiers, distinct per constructor and
// key size.
var (
	aesGCM128ID  = crypto.ID{'a', 'e', 's', '-', '1', '2', '8', '-', 'g', 'c', 'm', '/', 'v', '1'}
	aesGCM256ID  = crypto.ID{'a', 'e', 's', '-', '2', '5', '6', '-', 'g', 'c', 'm', '/', 'v', '1'}
	aesGCMR128ID = crypto.ID{'a', 'e', 's', '-', '1', '2', '8', '-', 'g', 'c', 'm', '-', 'r', '/', 'v', '1'}
	aesGCMR256ID = crypto.ID{'a', 'e', 's', '-', '2', '5', '6', '-', 'g', 'c', 'm', '-', 'r', '/', 'v', '1'}
)

// Shared keys for SUT and reference. Their lengths select the
// construction, which the Algorithm assertions of the contract suites
// confirm.
var (
	testKey128 = []byte("aesgcm-test-key1")
	testKey256 = []byte("contract-test-key-256-bit-fixed!")
)

// badKeySizes generates the key lengths up to 64 that neither
// constructor accepts. 24 is among them: AES-192 is a valid AES key size
// that the package does not support, as most modern protocol profiles
// do not.
var badKeySizes = prop.Integer(0, 64).Filter(func(n int) bool {
	return n != aesgcm.KeySize128 && n != aesgcm.KeySize256
})

// constructors are the two ways to build an AEAD, with the nonce size
// and the overhead of each, which NIST SP 800-38D fixes.
var constructors = []struct {
	name      string
	build     func([]byte) (crypto.AEAD, error)
	nonceSize int
	overhead  int
}{
	{name: "New", build: aesgcm.New, nonceSize: 12, overhead: 16},
	{name: "NewRandomNonce", build: aesgcm.NewRandomNonce, nonceSize: 0, overhead: 28},
}

// TestAES256GCMContract runs the contract suite of crypto.AEAD on
// AES-256-GCM with caller nonces.
func TestAES256GCMContract(t *testing.T) {
	t.Parallel()

	factory := func() crypto.AEAD { return mustBuild(t, aesgcm.New, testKey256) }
	cryptotest.AssertAEADContract(t, factory,
		append(cryptotest.AEADContractAssertions(),
			cryptotest.AEADIDAssertion(aesGCM256ID),
			cryptotest.AEADAlgorithmAssertion(crypto.AlgAES256GCM),
			cryptotest.AEADCrossInstanceAssertion(factory),
		)...,
	)
}

// TestAES128GCMContract runs the contract suite of crypto.AEAD on
// AES-128-GCM with caller nonces.
func TestAES128GCMContract(t *testing.T) {
	t.Parallel()

	factory := func() crypto.AEAD { return mustBuild(t, aesgcm.New, testKey128) }
	cryptotest.AssertAEADContract(t, factory,
		append(cryptotest.AEADContractAssertions(),
			cryptotest.AEADIDAssertion(aesGCM128ID),
			cryptotest.AEADAlgorithmAssertion(crypto.AlgAES128GCM),
			cryptotest.AEADCrossInstanceAssertion(factory),
		)...,
	)
}

// TestAES256GCMRandomNonceContract runs the contract suite of
// crypto.AEAD on AES-256-GCM with module nonces.
func TestAES256GCMRandomNonceContract(t *testing.T) {
	t.Parallel()

	factory := func() crypto.AEAD { return mustBuild(t, aesgcm.NewRandomNonce, testKey256) }
	cryptotest.AssertAEADContract(t, factory,
		append(cryptotest.AEADContractAssertions(),
			cryptotest.AEADIDAssertion(aesGCMR256ID),
			cryptotest.AEADAlgorithmAssertion(crypto.AlgAES256GCM),
			cryptotest.AEADCrossInstanceAssertion(factory),
		)...,
	)
}

// TestAES128GCMRandomNonceContract runs the contract suite of
// crypto.AEAD on AES-128-GCM with module nonces.
func TestAES128GCMRandomNonceContract(t *testing.T) {
	t.Parallel()

	factory := func() crypto.AEAD { return mustBuild(t, aesgcm.NewRandomNonce, testKey128) }
	cryptotest.AssertAEADContract(t, factory,
		append(cryptotest.AEADContractAssertions(),
			cryptotest.AEADIDAssertion(aesGCMR128ID),
			cryptotest.AEADAlgorithmAssertion(crypto.AlgAES128GCM),
			cryptotest.AEADCrossInstanceAssertion(factory),
		)...,
	)
}

func TestAESGCM(t *testing.T) {
	t.Parallel()

	for _, c := range constructors {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			sizes := []struct {
				name string
				key  []byte
				want crypto.Algorithm
			}{
				{
					name: "returns an AEAD of AES-128-GCM for a key of 16 bytes",
					key:  testKey128,
					want: crypto.AlgAES128GCM,
				},
				{
					name: "returns an AEAD of AES-256-GCM for a key of 32 bytes",
					key:  testKey256,
					want: crypto.AlgAES256GCM,
				},
			}
			for _, tt := range sizes {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					a, err := c.build(tt.key)
					assert.NoError(t, err, "the constructor must accept the key")
					assert.Equal(t, a.Algorithm(), tt.want, "the length of the key must select the construction")
				})
			}

			t.Run("returns ErrKeySize for a key of another length", func(t *testing.T) {
				t.Parallel()
				prop.ErrorIs(t, func(n int) error {
					_, err := c.build(make([]byte, n))

					return err
				}, crypto.ErrKeySize, "only keys of 16 and 32 bytes must be valid", prop.Using(badKeySizes),
					prop.Example(0), prop.Example(15), prop.Example(17), prop.Example(24), prop.Example(33))
			})

			t.Run("returns ErrKeySize for a nil key", func(t *testing.T) {
				t.Parallel()
				_, err := c.build(nil)
				assert.ErrorIs(t, err, crypto.ErrKeySize, "a nil key must be refused")
			})

			t.Run("returns an AEAD of the nonce size that NIST SP 800-38D fixes", func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, mustBuild(t, c.build, testKey256).NonceSize(), c.nonceSize,
					"a change of the nonce size changes the wire format of every ciphertext")
			})

			t.Run("returns an AEAD of the overhead that NIST SP 800-38D fixes", func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, mustBuild(t, c.build, testKey256).Overhead(), c.overhead,
					"a change of the overhead changes the wire format of every ciphertext")
			})

			// The caller must be free to zero its key material at once.
			t.Run("returns an AEAD that a later write to the key leaves unchanged", func(t *testing.T) {
				t.Parallel()
				k := bytes.Clone(testKey256)
				a := mustBuild(t, c.build, k)
				sealed, err := crypto.Seal(a, randcrypto.New(), []byte("payload"), nil)
				assert.NoError(t, err, "Seal must succeed")
				clear(k)
				opened, err := crypto.Open(a, sealed, nil)
				assert.NoError(t, err, "a zeroed key must not change the AEAD")
				assert.Equal(t, opened, []byte("payload"), "Open must recover the plaintext")
			})

			// The two constructors write the nonce at the same offset.
			t.Run("returns an AEAD whose envelopes the other constructor opens", func(t *testing.T) {
				t.Parallel()
				other := constructors[0].build
				if c.name == constructors[0].name {
					other = constructors[1].build
				}
				sealer, opener := mustBuild(t, c.build, testKey256), mustBuild(t, other, testKey256)
				assert.RoundTrip(t, func(p []byte) ([]byte, error) {
					return crypto.Seal(sealer, randcrypto.New(), p, []byte("aad"))
				}, func(sealed []byte) ([]byte, error) {
					return crypto.Open(opener, sealed, []byte("aad"))
				}, []byte("payload"), "the other constructor must open the envelope to its plaintext")
			})

			t.Run("returns an AEAD that opens the envelopes that goroutines seal at once", func(t *testing.T) {
				t.Parallel()
				a := mustBuild(t, c.build, testKey256)
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
	}

	t.Run("New", func(t *testing.T) {
		t.Parallel()

		// McGrew and Viega case 3, in the NIST SP 800-38D validation set.
		// The embedded cipher.AEAD fixes the nonce, which crypto.Seal
		// makes impossible. Without a known answer the contract suite
		// passes against any self-consistent construction.
		t.Run("returns an AEAD that reproduces the AES-GCM vector of McGrew and Viega case 3", func(t *testing.T) {
			t.Parallel()
			key, err := hex.DecodeString("feffe9928665731c6d6a8f9467308308")
			assert.NoError(t, err, "the key must be hexadecimal")
			nonce, err := hex.DecodeString("cafebabefacedbaddecaf888")
			assert.NoError(t, err, "the nonce must be hexadecimal")
			plain, err := hex.DecodeString("d9313225f88406e5a55909c5aff5269a86a7a9531534f7da2e4c303d8a318a72" +
				"1c3c0c95956809532fcf0e2449a6b525b16aedf5aa0de657ba637b391aafd255")
			assert.NoError(t, err, "the plaintext must be hexadecimal")
			assert.Equal(t, hex.EncodeToString(mustBuild(t, aesgcm.New, key).Seal(nil, nonce, plain, nil)),
				"42831ec2217774244b7221b784d0d49ce3aa212f2c02a4e035c17e2329aca12e"+
					"21d514b25466931c7d8f6a5aac84aa051ba30b396a0aac973d58e091473f5985"+
					"4d5c2af327cd64a62cf35abd2ba6fab4",
				"Seal must reproduce the ciphertext and the tag of the vector")
		})
	})

	t.Run("NewRandomNonce", func(t *testing.T) {
		t.Parallel()

		// A receipt that names one construction must not be satisfied by
		// another.
		t.Run("returns IDs other than those of New", func(t *testing.T) {
			t.Parallel()
			assert.NoDuplicates(t, func() ([]crypto.ID, error) {
				return []crypto.ID{
					mustBuild(t, aesgcm.New, testKey128).ID(),
					mustBuild(t, aesgcm.New, testKey256).ID(),
					mustBuild(t, aesgcm.NewRandomNonce, testKey128).ID(),
					mustBuild(t, aesgcm.NewRandomNonce, testKey256).ID(),
				}, nil
			}, "every constructor and key size must have its own ID")
		})
	})
}

// TestFIPSOnlyMode checks both constructors under GODEBUG=fips140=only.
// The mode is fixed when a process starts, so the test runs itself
// again in a child process with the mode set. In the child,
// fips140.Enforced reports true and the checks run there.
func TestFIPSOnlyMode(t *testing.T) {
	t.Parallel()

	if !fips140.Enforced() {
		t.Run("passes in a child process under fips140=only", func(t *testing.T) {
			t.Parallel()
			//nolint:gosec // G204: the child is this test binary, run again with a fixed pattern.
			cmd := exec.CommandContext(t.Context(), os.Args[0],
				"-test.run=^TestFIPSOnlyMode$", "-test.v", "-test.timeout="+childTimeout.String())
			cmd.Env = append(os.Environ(), "GODEBUG=fips140=only")
			out, err := cmd.CombinedOutput()
			assert.NoError(t, err, "the fips140=only child must pass:\n"+string(out))
			assert.Contains(t, string(out), "NewRandomNonce_seals_and_opens", "the child must run the FIPS checks")
		})

		return
	}

	t.Run("New refuses caller-supplied nonces", func(t *testing.T) {
		t.Parallel()
		_, err := aesgcm.New(testKey256)
		assert.Equal(t, errs.Classify(err), errs.Unsupported,
			"FIPS 140-only mode must refuse caller-supplied nonces as Unsupported")
	})

	t.Run("NewRandomNonce seals and opens", func(t *testing.T) {
		t.Parallel()
		a := mustBuild(t, aesgcm.NewRandomNonce, testKey256)
		assert.RoundTrip(t, func(p []byte) ([]byte, error) { return crypto.Seal(a, nil, p, nil) },
			func(sealed []byte) ([]byte, error) { return crypto.Open(a, sealed, nil) },
			[]byte("payload"), "Open must recover the plaintext that Seal sealed without a random source")
	})
}

// BenchmarkAESGCM runs the benchmarks of the contract suite of
// crypto.AEAD at both key sizes, because AES-256 runs 14 rounds to the
// 10 of AES-128, and with module nonces.
func BenchmarkAESGCM(b *testing.B) {
	b.Run("AES-128", func(b *testing.B) {
		cryptotest.BenchmarkAEADContract(b, func() crypto.AEAD { return mustBuild(b, aesgcm.New, testKey128) })
	})
	b.Run("AES-256", func(b *testing.B) {
		cryptotest.BenchmarkAEADContract(b, func() crypto.AEAD { return mustBuild(b, aesgcm.New, testKey256) })
	})
	b.Run("AES-256 random nonce", func(b *testing.B) {
		cryptotest.BenchmarkAEADContract(b, func() crypto.AEAD {
			return mustBuild(b, aesgcm.NewRandomNonce, testKey256)
		})
	})
}

// mustBuild returns the AEAD that build makes from key. It fails tb when
// build refuses the key.
func mustBuild(tb testing.TB, build func([]byte) (crypto.AEAD, error), key []byte) crypto.AEAD {
	tb.Helper()

	a, err := build(key)
	assert.NoError(tb, err, "the constructor must accept a key of a valid length")

	return a
}
