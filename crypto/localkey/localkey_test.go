// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package localkey_test

import (
	"bytes"
	"io"
	"strconv"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/coretest/cryptotest"
	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/localkey"
	randcrypto "go.thesmos.sh/core/rand/crypto"
)

const testKeyID = "local/test-root-key"

var (
	rootKey  = []byte("contract-test-root-key-32-bytes!")
	otherKey = []byte("a-different-root-key-of-32-bytes")
)

func mustNew(tb testing.TB, keyID string, key []byte) *localkey.Keeper {
	tb.Helper()

	k, err := localkey.New(keyID, key, randcrypto.New())
	testkit.NoError(tb, err, "New must accept a 32-byte root key")

	return k
}

func TestRootKeyFixtureLengths(t *testing.T) {
	t.Parallel()

	// A wrong-length fixture would fail far from its cause.
	testkit.Equal(t, len(rootKey), localkey.RootKeySize, "rootKey must be RootKeySize bytes")
	testkit.Equal(t, len(otherKey), localkey.RootKeySize, "otherKey must be RootKeySize bytes")
}

// --- testkit-driven contract layer ---

func TestKeeperContract(t *testing.T) {
	t.Parallel()

	factory := func() crypto.Keeper { return mustNew(t, testKeyID, rootKey) }
	cryptotest.AssertKeeperContract(t, factory,
		append(cryptotest.KeeperContractAssertions(),
			cryptotest.KeeperCrossInstanceAssertion(factory),
			cryptotest.KeeperForeignKeyAssertion(func() crypto.Keeper {
				return mustNew(t, "local/other-root-key", otherKey)
			}),
		)...,
	)
}

func TestDestroyerContract(t *testing.T) {
	t.Parallel()

	cryptotest.AssertDestroyerContract(t, mustNew(t, testKeyID, rootKey))
}

func TestKeyGeneratorContract(t *testing.T) {
	t.Parallel()

	cryptotest.AssertKeyGeneratorContract(t, mustNew(t, testKeyID, rootKey))
}

// --- impl-specific ---

func TestNewRootKeySizes(t *testing.T) {
	t.Parallel()

	t.Run("accepts a RootKeySize key", func(t *testing.T) {
		t.Parallel()
		_, err := localkey.New(testKeyID, make([]byte, localkey.RootKeySize), randcrypto.New())
		testkit.NoError(t, err, "New must accept the documented root-key size")
	})

	for _, n := range []int{0, 1, 16, 24, 31, 33, 64} {
		t.Run("rejects a "+strconv.Itoa(n)+"-byte root key", func(t *testing.T) {
			t.Parallel()
			_, err := localkey.New(testKeyID, make([]byte, n), randcrypto.New())
			testkit.ErrorIs(t, err, crypto.ErrKeySize, "only a RootKeySize root key is valid")
		})
	}

	t.Run("rejects an empty key ID", func(t *testing.T) {
		t.Parallel()
		// KeyID is persisted with every wrapped key; an empty one
		// strands the material it names.
		_, err := localkey.New("", rootKey, randcrypto.New())
		testkit.ErrorIs(t, err, crypto.ErrKeyID, "an empty key ID must be rejected")
	})
}

func TestKeyIDIsReported(t *testing.T) {
	t.Parallel()

	testkit.Equal(t, mustNew(t, testKeyID, rootKey).KeyID(), testKeyID,
		"KeyID must report the name given at construction")
}

func TestRootKeyIsCopied(t *testing.T) {
	t.Parallel()

	// The caller must be free to zero its root key immediately after
	// construction.
	k := bytes.Clone(rootKey)
	keeper := mustNew(t, testKeyID, k)

	wrapped, err := keeper.Wrap(t.Context(), []byte("data-key"))
	testkit.NoError(t, err, "Wrap must succeed")

	for i := range k {
		k[i] = 0
	}

	got, err := keeper.Unwrap(t.Context(), wrapped)
	testkit.NoError(t, err, "zeroing the caller's root key must not affect the Keeper")
	testkit.Equal(t, got, []byte("data-key"), "Unwrap must still return the data key")
}

func TestDestroy(t *testing.T) {
	t.Parallel()

	t.Run("rejects an unknown key ID", func(t *testing.T) {
		t.Parallel()
		// Destroying the wrong key silently would leave the caller
		// believing data was erased when it was not.
		keeper := mustNew(t, testKeyID, rootKey)
		testkit.ErrorIs(t, keeper.Destroy(t.Context(), "local/not-this-one"), crypto.ErrKeyID,
			"Destroy must reject a key ID it does not hold")
	})

	t.Run("is idempotent", func(t *testing.T) {
		t.Parallel()
		keeper := mustNew(t, testKeyID, rootKey)
		testkit.NoError(t, keeper.Destroy(t.Context(), testKeyID), "the first Destroy must succeed")
		testkit.ErrorIs(t, keeper.Destroy(t.Context(), testKeyID), crypto.ErrKeyDestroyed,
			"a second Destroy must report the key is already gone")
	})

	t.Run("GenerateKey fails after Destroy", func(t *testing.T) {
		t.Parallel()
		keeper := mustNew(t, testKeyID, rootKey)
		testkit.NoError(t, keeper.Destroy(t.Context(), testKeyID), "Destroy must succeed")

		_, _, err := keeper.GenerateKey(t.Context(), 32)
		testkit.ErrorIs(t, err, crypto.ErrKeyDestroyed, "a destroyed Keeper must not mint keys")
	})

	t.Run("Unwrap reports the key is destroyed, not that material is corrupt", func(t *testing.T) {
		t.Parallel()
		// The two are different diagnoses and a caller acts on them
		// differently: one is unrecoverable by design, the other
		// suggests damaged storage.
		keeper := mustNew(t, testKeyID, rootKey)
		wrapped, err := keeper.Wrap(t.Context(), []byte("data-key"))
		testkit.NoError(t, err, "Wrap must succeed")
		testkit.NoError(t, keeper.Destroy(t.Context(), testKeyID), "Destroy must succeed")

		_, err = keeper.Unwrap(t.Context(), wrapped)
		testkit.ErrorIs(t, err, crypto.ErrKeyDestroyed, "Unwrap must name the cause")
	})
}

func TestGenerateKeySizes(t *testing.T) {
	t.Parallel()

	for _, size := range []int{16, 32, 64} {
		t.Run("mints a "+strconv.Itoa(size)+"-byte data key", func(t *testing.T) {
			t.Parallel()
			plaintext, wrapped, err := mustNew(t, testKeyID, rootKey).GenerateKey(t.Context(), size)
			testkit.NoError(t, err, "GenerateKey must succeed")
			testkit.Equal(t, len(plaintext), size, "the data key must be the requested size")
			testkit.True(t, len(wrapped) > size, "the wrapped form carries a nonce and a tag")
		})
	}

	for _, size := range []int{0, -1} {
		t.Run("rejects size "+strconv.Itoa(size), func(t *testing.T) {
			t.Parallel()
			_, _, err := mustNew(t, testKeyID, rootKey).GenerateKey(t.Context(), size)
			testkit.ErrorIs(t, err, crypto.ErrKeySize, "a non-positive size must be rejected")
		})
	}

	t.Run("propagates an entropy failure", func(t *testing.T) {
		t.Parallel()
		// A data key drawn from a failed entropy source would be
		// predictable, so this must fail loudly rather than return
		// short or zeroed material.
		failing, err := localkey.New(testKeyID, rootKey,
			randcrypto.NewWithReader(&testkit.FailingReader{
				Source:     bytes.NewReader(nil),
				BeforeFail: 0,
				Err:        io.ErrUnexpectedEOF,
			}))
		testkit.NoError(t, err, "New must accept a 32-byte root key")

		plaintext, wrapped, err := failing.GenerateKey(t.Context(), 32)
		testkit.ErrorIs(t, err, io.ErrUnexpectedEOF, "GenerateKey must surface the entropy failure")
		testkit.Equal(t, plaintext, []byte(nil), "no key material may be returned alongside an error")
		testkit.Equal(t, wrapped, []byte(nil), "no wrapped material may be returned alongside an error")
	})
}

func BenchmarkWrap(b *testing.B) {
	keeper := mustNew(b, testKeyID, rootKey)
	dek := make([]byte, 32)
	b.ReportAllocs()

	var sink []byte
	for b.Loop() {
		sink, _ = keeper.Wrap(b.Context(), dek)
	}
	testkit.True(b, sink != nil, "Wrap must produce output")
}

func BenchmarkUnwrap(b *testing.B) {
	keeper := mustNew(b, testKeyID, rootKey)
	wrapped, err := keeper.Wrap(b.Context(), make([]byte, 32))
	testkit.NoError(b, err, "Wrap must succeed")
	b.ReportAllocs()

	var sink []byte
	for b.Loop() {
		sink, _ = keeper.Unwrap(b.Context(), wrapped)
	}
	testkit.True(b, sink != nil, "Unwrap must produce output")
}
