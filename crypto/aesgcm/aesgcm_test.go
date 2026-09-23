// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package aesgcm_test

import (
	"bytes"
	"crypto/fips140"
	"os"
	"os/exec"
	"strconv"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/coretest/cryptotest"
	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/aesgcm"
	"go.thesmos.sh/core/errs"
	randcrypto "go.thesmos.sh/core/rand/crypto"
)

// The canonical build-local identifiers, distinct per constructor and
// key size.
var (
	aesGCM128ID  = crypto.ID{'a', 'e', 's', '-', '1', '2', '8', '-', 'g', 'c', 'm', '/', 'v', '1'}
	aesGCM256ID  = crypto.ID{'a', 'e', 's', '-', '2', '5', '6', '-', 'g', 'c', 'm', '/', 'v', '1'}
	aesGCMR128ID = crypto.ID{'a', 'e', 's', '-', '1', '2', '8', '-', 'g', 'c', 'm', '-', 'r', '/', 'v', '1'}
	aesGCMR256ID = crypto.ID{'a', 'e', 's', '-', '2', '5', '6', '-', 'g', 'c', 'm', '-', 'r', '/', 'v', '1'}
)

// constructors are the two ways to build an AEAD, so every
// construction-level test runs against both.
var constructors = []struct {
	build func([]byte) (crypto.AEAD, error)
	name  string
}{
	{aesgcm.New, "New"},
	{aesgcm.NewRandomNonce, "NewRandomNonce"},
}

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

func mustNewRandomNonce(tb testing.TB, key []byte) crypto.AEAD {
	tb.Helper()

	a, err := aesgcm.NewRandomNonce(key)
	testkit.NoError(tb, err, "NewRandomNonce must accept a valid key length")

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

func TestAES256GCMRandomNonceContract(t *testing.T) {
	t.Parallel()

	factory := func() crypto.AEAD { return mustNewRandomNonce(t, testKey256) }
	cryptotest.AssertAEADContract(t, factory,
		append(cryptotest.AEADContractAssertions(),
			cryptotest.AEADIDAssertion(aesGCMR256ID),
			cryptotest.AEADAlgorithmAssertion(crypto.AlgAES256GCM),
			cryptotest.AEADCrossInstanceAssertion(factory),
		)...,
	)
}

func TestAES128GCMRandomNonceContract(t *testing.T) {
	t.Parallel()

	factory := func() crypto.AEAD { return mustNewRandomNonce(t, testKey128) }
	cryptotest.AssertAEADContract(t, factory,
		append(cryptotest.AEADContractAssertions(),
			cryptotest.AEADIDAssertion(aesGCMR128ID),
			cryptotest.AEADAlgorithmAssertion(crypto.AlgAES128GCM),
			cryptotest.AEADCrossInstanceAssertion(factory),
		)...,
	)
}

func BenchmarkAESGCM(b *testing.B) {
	// Both key sizes: AES-256 runs 14 rounds to AES-128's 10, so the
	// two have materially different throughput and each needs its own
	// baseline.
	b.Run("AES-128", func(b *testing.B) {
		cryptotest.BenchmarkAEADContract(b, func() crypto.AEAD { return mustNew(b, testKey128) })
	})
	b.Run("AES-256", func(b *testing.B) {
		cryptotest.BenchmarkAEADContract(b, func() crypto.AEAD { return mustNew(b, testKey256) })
	})
	b.Run("AES-256 random nonce", func(b *testing.B) {
		cryptotest.BenchmarkAEADContract(b, func() crypto.AEAD { return mustNewRandomNonce(b, testKey256) })
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

	for _, c := range constructors {
		for _, tc := range valid {
			t.Run(c.name+" accepts a "+strconv.Itoa(tc.size)+"-byte key", func(t *testing.T) {
				t.Parallel()
				a, err := c.build(make([]byte, tc.size))
				testkit.NoError(t, err, "the constructor must accept the key length")
				testkit.Equal(t, a.Algorithm(), tc.alg, "the key length selects the construction")
			})
		}

		// 24 is included deliberately: AES-192 is a valid AES key size
		// that this package does not support, matching most modern
		// protocol profiles.
		for _, n := range []int{0, 1, 15, 17, 24, 31, 33, 64} {
			t.Run(c.name+" rejects a "+strconv.Itoa(n)+"-byte key", func(t *testing.T) {
				t.Parallel()
				_, err := c.build(make([]byte, n))
				testkit.ErrorIs(t, err, crypto.ErrKeySize, "only 16- and 32-byte keys are valid")
			})
		}

		t.Run(c.name+" rejects a nil key", func(t *testing.T) {
			t.Parallel()
			_, err := c.build(nil)
			testkit.ErrorIs(t, err, crypto.ErrKeySize, "a nil key must be rejected")
		})
	}
}

func TestConstructionsHaveDistinctIDs(t *testing.T) {
	t.Parallel()

	// A receipt naming one construction must not be satisfiable by
	// another.
	ids := map[crypto.ID]bool{
		mustNew(t, testKey128).ID():            true,
		mustNew(t, testKey256).ID():            true,
		mustNewRandomNonce(t, testKey128).ID(): true,
		mustNewRandomNonce(t, testKey256).ID(): true,
	}
	testkit.Equal(t, len(ids), 4, "every constructor and key size must have its own ID")
}

func TestEnvelopesInteroperate(t *testing.T) {
	t.Parallel()

	// The two constructors write the nonce at the same offset, so an
	// envelope from either opens under the other with the same key.
	pairs := []struct {
		seal, open crypto.AEAD
		name       string
	}{
		{mustNew(t, testKey256), mustNewRandomNonce(t, testKey256), "New to NewRandomNonce"},
		{mustNewRandomNonce(t, testKey256), mustNew(t, testKey256), "NewRandomNonce to New"},
	}
	for _, tt := range pairs {
		t.Run("an envelope opens from "+tt.name, func(t *testing.T) {
			t.Parallel()
			sealed, err := crypto.Seal(tt.seal, randcrypto.New(), []byte("payload"), []byte("aad"))
			testkit.NoError(t, err, "Seal must succeed")

			opened, err := crypto.Open(tt.open, sealed, []byte("aad"))
			testkit.NoError(t, err, "the other constructor must open the envelope")
			testkit.Equal(t, opened, []byte("payload"), "Open must recover the plaintext")
		})
	}
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
			cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestFIPSOnlyMode$", "-test.v")
			cmd.Env = append(os.Environ(), "GODEBUG=fips140=only")
			out, err := cmd.CombinedOutput()
			testkit.NoError(t, err, "the fips140=only child must pass:\n"+string(out))
			testkit.True(t, bytes.Contains(out, []byte("NewRandomNonce_seals_and_opens")),
				"the child must run the FIPS checks, not skip them")
		})

		return
	}

	t.Run("New refuses caller-supplied nonces", func(t *testing.T) {
		t.Parallel()
		_, err := aesgcm.New(testKey256)
		testkit.Equal(t, errs.Classify(err), errs.Unsupported,
			"FIPS 140-only mode must refuse caller-supplied nonces as Unsupported")
	})

	t.Run("NewRandomNonce seals and opens", func(t *testing.T) {
		t.Parallel()
		a := mustNewRandomNonce(t, testKey256)

		sealed, err := crypto.Seal(a, nil, []byte("payload"), nil)
		testkit.NoError(t, err, "Seal must succeed without a random source")

		opened, err := crypto.Open(a, sealed, nil)
		testkit.NoError(t, err, "Open must succeed on Seal's own output")
		testkit.Equal(t, opened, []byte("payload"), "Open must recover the plaintext")
	})
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
	t.Run("New takes a 96-bit nonce and appends a 128-bit tag", func(t *testing.T) {
		t.Parallel()
		a := mustNew(t, testKey256)
		testkit.Equal(t, a.NonceSize(), 12, "GCM uses a 96-bit nonce")
		testkit.Equal(t, a.Overhead(), 16, "GCM appends a 128-bit tag")
	})

	t.Run("NewRandomNonce carries its 96-bit nonce in the overhead", func(t *testing.T) {
		t.Parallel()
		a := mustNewRandomNonce(t, testKey256)
		testkit.Equal(t, a.NonceSize(), 0, "the caller supplies no nonce")
		testkit.Equal(t, a.Overhead(), 28, "the overhead is the 12-byte nonce and the 16-byte tag")
	})
}
