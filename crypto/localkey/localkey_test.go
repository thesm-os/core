// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package localkey_test

import (
	"bytes"
	"crypto/fips140"
	"io"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/coretest/cryptotest"
	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/aesgcm"
	"go.thesmos.sh/core/crypto/localkey"
	randcrypto "go.thesmos.sh/core/rand/crypto"
)

const testKeyID = "local/test-root-key"

var (
	rootKey  = []byte("contract-test-root-key-32-bytes!")
	otherKey = []byte("a-different-root-key-of-32-bytes")
	origin   = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
)

func mustNew(tb testing.TB, keyID string, key []byte) *localkey.Keeper {
	tb.Helper()

	k, err := localkey.New(keyID, key, randcrypto.New(), fake.New(origin))
	testkit.NoError(tb, err, "New must accept a 32-byte root key")

	return k
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

// TestNew covers construction. The key ID is persisted with every
// wrapped key, so New refuses an empty one: it would leave the material
// without a name for the key that unwraps it. A caller must be free to
// zero its root key as soon as New returns.
func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("the root-key fixtures are RootKeySize bytes", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, len(rootKey), localkey.RootKeySize, "rootKey must be RootKeySize bytes")
		testkit.Equal(t, len(otherKey), localkey.RootKeySize, "otherKey must be RootKeySize bytes")
	})

	t.Run("accepts a RootKeySize key", func(t *testing.T) {
		t.Parallel()
		_, err := localkey.New(testKeyID, make([]byte, localkey.RootKeySize), randcrypto.New(), fake.New(origin))
		testkit.NoError(t, err, "New must accept the documented root-key size")
	})

	for _, n := range []int{0, 1, 16, 24, 31, 33, 64} {
		t.Run("rejects a "+strconv.Itoa(n)+"-byte root key", func(t *testing.T) {
			t.Parallel()
			_, err := localkey.New(testKeyID, make([]byte, n), randcrypto.New(), fake.New(origin))
			testkit.ErrorIs(t, err, crypto.ErrKeySize, "only a RootKeySize root key is valid")
		})
	}

	t.Run("rejects an empty key ID", func(t *testing.T) {
		t.Parallel()
		_, err := localkey.New("", rootKey, randcrypto.New(), fake.New(origin))
		testkit.ErrorIs(t, err, crypto.ErrKeyID, "an empty key ID must be rejected")
	})

	t.Run("copies the root key", func(t *testing.T) {
		t.Parallel()
		k := bytes.Clone(rootKey)
		keeper := mustNew(t, testKeyID, k)

		wrapped, err := keeper.Wrap(t.Context(), []byte("data-key"))
		testkit.NoError(t, err, "Wrap must succeed")

		clear(k)

		got, err := keeper.Unwrap(t.Context(), wrapped)
		testkit.NoError(t, err, "zeroing the caller's root key must not affect the Keeper")
		testkit.Equal(t, got, []byte("data-key"), "Unwrap must still return the data key")
	})
}

func TestKeyID(t *testing.T) {
	t.Parallel()

	t.Run("returns the name given at construction", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, mustNew(t, testKeyID, rootKey).KeyID(), testKeyID,
			"KeyID must report the name given at construction")
	})
}

// TestUnwrap covers material from the other AES-GCM construction. Both
// constructions write the nonce at the same offset, so a Keeper opens
// an envelope that aesgcm.New sealed under its root key.
func TestUnwrap(t *testing.T) {
	t.Parallel()

	t.Run("opens an envelope that aesgcm.New sealed under the root key", func(t *testing.T) {
		t.Parallel()
		a, err := aesgcm.New(rootKey)
		testkit.NoError(t, err, "aesgcm.New must accept the root key")

		sealed, err := crypto.Seal(a, randcrypto.New(), []byte("data-key"), nil)
		testkit.NoError(t, err, "Seal must succeed")

		got, err := mustNew(t, testKeyID, rootKey).Unwrap(t.Context(), sealed)
		testkit.NoError(t, err, "Unwrap must open an envelope from aesgcm.New")
		testkit.Equal(t, got, []byte("data-key"), "Unwrap must return the data key")
	})
}

// TestFIPSOnlyMode checks the Keeper under GODEBUG=fips140=only. The
// mode is fixed when a process starts, so the test runs itself again
// in a child process with the mode set. In the child,
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
			testkit.True(t, bytes.Contains(out, []byte("wraps_and_unwraps_a_data_key")),
				"the child must run the FIPS checks, not skip them")
		})

		return
	}

	t.Run("wraps and unwraps a data key", func(t *testing.T) {
		t.Parallel()
		keeper := mustNew(t, testKeyID, rootKey)

		wrapped, err := keeper.Wrap(t.Context(), []byte("data-key"))
		testkit.NoError(t, err, "Wrap must succeed in FIPS 140-only mode")

		got, err := keeper.Unwrap(t.Context(), wrapped)
		testkit.NoError(t, err, "Unwrap must succeed in FIPS 140-only mode")
		testkit.Equal(t, got, []byte("data-key"), "Unwrap must return the data key")
	})

	t.Run("generates a data key that unwraps", func(t *testing.T) {
		t.Parallel()
		keeper := mustNew(t, testKeyID, rootKey)

		plaintext, wrapped, err := keeper.GenerateKey(t.Context(), 32)
		testkit.NoError(t, err, "GenerateKey must succeed in FIPS 140-only mode")

		got, err := keeper.Unwrap(t.Context(), wrapped)
		testkit.NoError(t, err, "Unwrap must open the generated key")
		testkit.Equal(t, got, plaintext, "Unwrap must return the generated data key")
	})
}

// TestDestroy covers the destruction contract. Destroying another key
// without an error would leave the caller believing data was erased
// when it was not. After Destroy, Unwrap reports the destroyed key and
// not corrupt material: a destroyed key is unrecoverable by design,
// while corrupt material points to damaged storage.
func TestDestroy(t *testing.T) {
	t.Parallel()

	t.Run("rejects an unknown key ID", func(t *testing.T) {
		t.Parallel()
		keeper := mustNew(t, testKeyID, rootKey)
		_, err := keeper.Destroy(t.Context(), "local/not-this-one")
		testkit.ErrorIs(t, err, crypto.ErrKeyID, "Destroy must reject a key ID it does not have")
	})

	t.Run("returns the time of the first call on every call", func(t *testing.T) {
		t.Parallel()
		c := fake.New(origin)
		keeper, err := localkey.New(testKeyID, rootKey, randcrypto.New(), c)
		testkit.NoError(t, err, "New must accept a 32-byte root key")

		first, err := keeper.Destroy(t.Context(), testKeyID)
		testkit.NoError(t, err, "the first Destroy must succeed")
		testkit.Equal(t, first, origin, "Destroy must return the clock's time")

		c.Advance(time.Hour)
		again, err := keeper.Destroy(t.Context(), testKeyID)
		testkit.NoError(t, err, "a second Destroy must succeed")
		testkit.Equal(t, again, first, "a second Destroy must return the first call's time")
	})

	t.Run("GenerateKey fails after Destroy", func(t *testing.T) {
		t.Parallel()
		keeper := mustNew(t, testKeyID, rootKey)
		_, err := keeper.Destroy(t.Context(), testKeyID)
		testkit.NoError(t, err, "Destroy must succeed")

		_, _, err = keeper.GenerateKey(t.Context(), 32)
		testkit.ErrorIs(t, err, crypto.ErrKeyDestroyed, "a destroyed Keeper must not generate keys")
	})

	t.Run("Unwrap reports the key is destroyed, not that material is corrupt", func(t *testing.T) {
		t.Parallel()
		keeper := mustNew(t, testKeyID, rootKey)
		wrapped, err := keeper.Wrap(t.Context(), []byte("data-key"))
		testkit.NoError(t, err, "Wrap must succeed")
		_, err = keeper.Destroy(t.Context(), testKeyID)
		testkit.NoError(t, err, "Destroy must succeed")

		_, err = keeper.Unwrap(t.Context(), wrapped)
		testkit.ErrorIs(t, err, crypto.ErrKeyDestroyed, "Unwrap must name the cause")
	})
}

// TestGenerateKey covers the sizes GenerateKey accepts and its entropy
// failure. A data key read from a failed entropy source would be
// predictable, so GenerateKey returns the failure and no key material.
func TestGenerateKey(t *testing.T) {
	t.Parallel()

	for _, size := range []int{16, 32, 64} {
		t.Run("returns a "+strconv.Itoa(size)+"-byte data key", func(t *testing.T) {
			t.Parallel()
			plaintext, wrapped, err := mustNew(t, testKeyID, rootKey).GenerateKey(t.Context(), size)
			testkit.NoError(t, err, "GenerateKey must succeed")
			testkit.Equal(t, len(plaintext), size, "the data key must be the requested size")
			testkit.True(t, len(wrapped) > size, "the wrapped form contains a nonce and a tag")
		})
	}

	for _, size := range []int{0, -1} {
		t.Run("rejects size "+strconv.Itoa(size), func(t *testing.T) {
			t.Parallel()
			_, _, err := mustNew(t, testKeyID, rootKey).GenerateKey(t.Context(), size)
			testkit.ErrorIs(t, err, crypto.ErrKeySize, "a non-positive size must be rejected")
		})
	}

	t.Run("returns an entropy failure without key material", func(t *testing.T) {
		t.Parallel()
		failing, err := localkey.New(testKeyID, rootKey,
			randcrypto.NewWithReader(&testkit.FailingReader{
				Source:     bytes.NewReader(nil),
				BeforeFail: 0,
				Err:        io.ErrUnexpectedEOF,
			}), fake.New(origin))
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
