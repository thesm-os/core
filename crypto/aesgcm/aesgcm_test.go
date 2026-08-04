// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package aesgcm_test

import (
	"bytes"
	"strconv"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/coretest/cryptotest"
	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/aesgcm"
	randcrypto "go.thesmos.sh/core/rand/crypto"
)

// aesGCM128ID and aesGCM256ID are the canonical build-local
// identifiers, distinct per construction.
var (
	aesGCM128ID = crypto.ID{'a', 'e', 's', '-', '1', '2', '8', '-', 'g', 'c', 'm', '/', 'v', '1'}
	aesGCM256ID = crypto.ID{'a', 'e', 's', '-', '2', '5', '6', '-', 'g', 'c', 'm', '/', 'v', '1'}
)

// Shared keys for SUT and reference. Their lengths select the
// construction, so TestKeyFixtureLengths pins them.
var (
	testKey128 = []byte("aesgcm-test-key1")
	testKey256 = []byte("contract-test-key-256-bit-fixed!")
)

func mustNew(tb testing.TB, key []byte) crypto.AEAD {
	tb.Helper()

	a, err := aesgcm.New(key)
	testkit.NoError(tb, err, "New must accept a valid key length")

	return a
}

func TestKeyFixtureLengths(t *testing.T) {
	t.Parallel()

	// A wrong-length fixture would silently test the other
	// construction, or fail far from its cause.
	testkit.Equal(t, len(testKey128), aesgcm.KeySize128, "testKey128 must be KeySize128 bytes")
	testkit.Equal(t, len(testKey256), aesgcm.KeySize256, "testKey256 must be KeySize256 bytes")
}

// --- testkit-driven contract layer ---

func TestAES256GCMContract(t *testing.T) {
	t.Parallel()

	factory := func() crypto.AEAD { return mustNew(t, testKey256) }
	cryptotest.AssertAEADContract(t, factory,
		append(cryptotest.AEADContractAssertions(),
			cryptotest.AEADIDAssertion(aesGCM256ID),
			cryptotest.AEADAlgorithmAssertion(crypto.AlgAES256GCM),
			cryptotest.AEADCrossInstanceAssertion(factory),
		)...,
	)
}

func TestAES128GCMContract(t *testing.T) {
	t.Parallel()

	factory := func() crypto.AEAD { return mustNew(t, testKey128) }
	cryptotest.AssertAEADContract(t, factory,
		append(cryptotest.AEADContractAssertions(),
			cryptotest.AEADIDAssertion(aesGCM128ID),
			cryptotest.AEADAlgorithmAssertion(crypto.AlgAES128GCM),
			cryptotest.AEADCrossInstanceAssertion(factory),
		)...,
	)
}

func BenchmarkAESGCM(b *testing.B) {
	// Both constructions: AES-256 runs 14 rounds to AES-128's 10, so
	// the two have materially different throughput and each needs
	// its own baseline.
	b.Run("AES-128", func(b *testing.B) {
		cryptotest.BenchmarkAEADContract(b, func() crypto.AEAD { return mustNew(b, testKey128) })
	})
	b.Run("AES-256", func(b *testing.B) {
		cryptotest.BenchmarkAEADContract(b, func() crypto.AEAD { return mustNew(b, testKey256) })
	})
}

// --- impl-specific ---

// TestGCMVector checks the implementation against the canonical
// AES-GCM test vector (McGrew & Viega case 3, carried into the NIST
// SP 800-38D validation set). It uses the embedded cipher.AEAD
// directly so the nonce is fixed; crypto.Seal deliberately makes that
// impossible.
//
// Without a known-answer test the contract suite would pass against
// any self-consistent construction. This is what pins the bytes to
// AES-GCM.
func TestGCMVector(t *testing.T) {
	t.Parallel()

	var (
		key   = testkit.MustDecodeHex(t, "feffe9928665731c6d6a8f9467308308")
		nonce = testkit.MustDecodeHex(t, "cafebabefacedbaddecaf888")
		plain = testkit.MustDecodeHex(t,
			"d9313225f88406e5a55909c5aff5269a"+
				"86a7a9531534f7da2e4c303d8a318a72"+
				"1c3c0c95956809532fcf0e2449a6b525"+
				"b16aedf5aa0de657ba637b391aafd255")
		// Ciphertext followed by the 128-bit tag.
		want = testkit.MustDecodeHex(t,
			"42831ec2217774244b7221b784d0d49c"+
				"e3aa212f2c02a4e035c17e2329aca12e"+
				"21d514b25466931c7d8f6a5aac84aa05"+
				"1ba30b396a0aac973d58e091473f5985"+
				"4d5c2af327cd64a62cf35abd2ba6fab4")
	)

	a := mustNew(t, key)
	testkit.Equal(t, a.Seal(nil, nonce, plain, nil), want,
		"Seal must reproduce the canonical AES-128-GCM vector")
}

func TestNewKeySizes(t *testing.T) {
	t.Parallel()

	valid := []struct {
		size int
		alg  crypto.Algorithm
	}{
		{aesgcm.KeySize128, crypto.AlgAES128GCM},
		{aesgcm.KeySize256, crypto.AlgAES256GCM},
	}
	for _, tc := range valid {
		t.Run("accepts a "+strconv.Itoa(tc.size)+"-byte key", func(t *testing.T) {
			t.Parallel()
			a, err := aesgcm.New(make([]byte, tc.size))
			testkit.NoError(t, err, "New must accept the key length")
			testkit.Equal(t, a.Algorithm(), tc.alg, "the key length selects the construction")
		})
	}

	// 24 is included deliberately: AES-192 is a valid AES key size
	// that this package does not support, matching most modern
	// protocol profiles.
	for _, n := range []int{0, 1, 15, 17, 24, 31, 33, 64} {
		t.Run("rejects a "+strconv.Itoa(n)+"-byte key", func(t *testing.T) {
			t.Parallel()
			_, err := aesgcm.New(make([]byte, n))
			testkit.ErrorIs(t, err, crypto.ErrKeySize, "only 16- and 32-byte keys are valid")
		})
	}

	t.Run("rejects a nil key", func(t *testing.T) {
		t.Parallel()
		_, err := aesgcm.New(nil)
		testkit.ErrorIs(t, err, crypto.ErrKeySize, "a nil key must be rejected")
	})
}

func TestConstructionsHaveDistinctIDs(t *testing.T) {
	t.Parallel()

	// A receipt naming one construction must not be satisfiable by
	// the other.
	testkit.NotEqual(t, mustNew(t, testKey128).ID(), mustNew(t, testKey256).ID(),
		"AES-128 and AES-256 must not share an ID")
}

func TestKeyIsCopied(t *testing.T) {
	t.Parallel()

	// The caller must be free to zero its key material immediately
	// after construction. Not a contract assertion because it needs
	// control of the key bytes, which the seam does not expose.
	k := bytes.Clone(testKey256)
	a := mustNew(t, k)

	sealed, err := crypto.Seal(a, randcrypto.New(), []byte("payload"), nil)
	testkit.NoError(t, err, "Seal must succeed")

	for i := range k {
		k[i] = 0
	}

	opened, err := crypto.Open(a, sealed, nil)
	testkit.NoError(t, err, "zeroing the caller's key must not affect the AEAD")
	testkit.True(t, bytes.Equal(opened, []byte("payload")), "Open must still recover the plaintext")
}

func TestGCMParameters(t *testing.T) {
	t.Parallel()

	// Fixed by NIST SP 800-38D. A change here would be a wire-format
	// change for every stored ciphertext.
	a := mustNew(t, testKey256)
	testkit.Equal(t, a.NonceSize(), 12, "GCM uses a 96-bit nonce")
	testkit.Equal(t, a.Overhead(), 16, "GCM appends a 128-bit tag")
}
