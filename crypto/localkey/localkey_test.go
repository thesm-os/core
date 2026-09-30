// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package localkey_test

import (
	"bytes"
	"context"
	"crypto/fips140"
	"errors"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/coretest/cryptotest"
	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/aesgcm"
	"go.thesmos.sh/core/crypto/localkey"
	"go.thesmos.sh/core/errs"
	randcrypto "go.thesmos.sh/core/rand/crypto"
)

// testKeyID is the key ID that the tests pass to New.
const testKeyID = "local/test-root-key"

// concurrentCallers is the number of goroutines that call the Keepers of
// one key table concurrently.
const concurrentCallers = 32

var (
	// rootKey and otherKey are two distinct root keys of RootKeySize bytes.
	rootKey  = []byte("contract-test-root-key-32-bytes!")
	otherKey = []byte("a-different-root-key-of-32-bytes")

	// origin is the start time of every fake clock in the tests.
	origin = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// errUnwrapped is the error of useCreatedKey when Unwrap returns a data
	// key other than the one it wrapped.
	errUnwrapped = testkit.TestError("unwrapped data key differs")

	// errStillWraps is the error of useCreatedKey when a destroyed key
	// still wraps.
	errStillWraps = testkit.TestError("destroyed key still wraps")
)

// mustNew returns a Keeper over key, named keyID, with a cryptographic
// random source and a fake clock at origin. It fails tb when New returns
// an error.
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

func TestAADKeeperContract(t *testing.T) {
	t.Parallel()

	cryptotest.AssertAADKeeperContract(t, mustNew(t, testKeyID, rootKey))
}

func TestKeyCreatorContract(t *testing.T) {
	t.Parallel()

	cryptotest.AssertKeyCreatorContract(t, mustNew(t, testKeyID, rootKey))
}

// --- impl-specific ---

// TestNew covers the root-key sizes and key IDs that New validates, and
// its copy of the root key.
func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("has fixture root keys of RootKeySize bytes", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, len(rootKey), localkey.RootKeySize, "rootKey must be RootKeySize bytes")
		testkit.Equal(t, len(otherKey), localkey.RootKeySize, "otherKey must be RootKeySize bytes")
	})

	t.Run("returns a Keeper for a RootKeySize root key", func(t *testing.T) {
		t.Parallel()
		_, err := localkey.New(testKeyID, make([]byte, localkey.RootKeySize), randcrypto.New(), fake.New(origin))
		testkit.NoError(t, err, "New must accept the documented root-key size")
	})

	for _, n := range []int{0, 1, 16, 24, 31, 33, 64} {
		t.Run("returns ErrKeySize for a "+strconv.Itoa(n)+"-byte root key", func(t *testing.T) {
			t.Parallel()
			_, err := localkey.New(testKeyID, make([]byte, n), randcrypto.New(), fake.New(origin))
			testkit.ErrorIs(t, err, crypto.ErrKeySize, "only a RootKeySize root key is valid")
		})
	}

	t.Run("returns ErrKeyID for an empty key ID", func(t *testing.T) {
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

	t.Run("returns the key ID passed to New", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, mustNew(t, testKeyID, rootKey).KeyID(), testKeyID,
			"KeyID must return the key ID passed to New")
	})
}

// TestUnwrap covers envelopes that aesgcm.New sealed. Both AES-GCM
// constructions write the nonce at one offset, so a Keeper opens an
// envelope that aesgcm.New sealed under its root key.
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

// childTimeout is the -test.timeout of the child process of
// TestFIPSOnlyMode. The child runs its checks in milliseconds. The
// bound ends a child whose parent has died, which no context of the
// parent can cancel.
const childTimeout = 30 * time.Second

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
			cmd := exec.CommandContext(t.Context(), os.Args[0],
				"-test.run=^TestFIPSOnlyMode$", "-test.v", "-test.timeout="+childTimeout.String())
			cmd.Env = append(os.Environ(), "GODEBUG=fips140=only")
			out, err := cmd.CombinedOutput()
			testkit.NoError(t, err, "the fips140=only child must pass:\n"+string(out))
			testkit.True(t, bytes.Contains(out, []byte("round-trips_a_data_key")),
				"the child must run the FIPS checks, not skip them")
		})

		return
	}

	t.Run("round-trips a data key", func(t *testing.T) {
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

	t.Run("creates a key that round-trips a data key", func(t *testing.T) {
		t.Parallel()
		created, err := mustNew(t, testKeyID, rootKey).CreateKey(t.Context())
		testkit.NoError(t, err, "CreateKey must succeed in FIPS 140-only mode")

		wrapped, err := created.Wrap(t.Context(), []byte("data-key"))
		testkit.NoError(t, err, "a created key must wrap in FIPS 140-only mode")

		got, err := created.Unwrap(t.Context(), wrapped)
		testkit.NoError(t, err, "a created key must unwrap in FIPS 140-only mode")
		testkit.Equal(t, got, []byte("data-key"), "Unwrap must return the data key")
	})
}

// TestDestroy covers the key IDs that Destroy validates and the time that
// it returns. It also covers the errors of a destroyed key, and Destroy of
// a key in concurrent use.
func TestDestroy(t *testing.T) {
	t.Parallel()

	t.Run("returns ErrKeyID for an unknown key ID", func(t *testing.T) {
		t.Parallel()
		keeper := mustNew(t, testKeyID, rootKey)
		_, err := keeper.Destroy(t.Context(), "local/not-this-one")
		testkit.ErrorIs(t, err, crypto.ErrKeyID, "Destroy must reject a key ID it does not have")
		testkit.Equal(t, errs.Classify(err), errs.NotFound, "ErrKeyID must classify as NotFound")
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

	t.Run("makes GenerateKey return ErrKeyDestroyed", func(t *testing.T) {
		t.Parallel()
		keeper := mustNew(t, testKeyID, rootKey)
		_, err := keeper.Destroy(t.Context(), testKeyID)
		testkit.NoError(t, err, "Destroy must succeed")

		_, _, err = keeper.GenerateKey(t.Context(), 32)
		testkit.ErrorIs(t, err, crypto.ErrKeyDestroyed, "a destroyed Keeper must not generate keys")
	})

	t.Run("makes Unwrap return ErrKeyDestroyed", func(t *testing.T) {
		t.Parallel()
		keeper := mustNew(t, testKeyID, rootKey)
		wrapped, err := keeper.Wrap(t.Context(), []byte("data-key"))
		testkit.NoError(t, err, "Wrap must succeed")
		_, err = keeper.Destroy(t.Context(), testKeyID)
		testkit.NoError(t, err, "Destroy must succeed")

		_, err = keeper.Unwrap(t.Context(), wrapped)
		testkit.ErrorIs(t, err, crypto.ErrKeyDestroyed, "Unwrap must return ErrKeyDestroyed")
		testkit.Equal(t, errs.Classify(err), errs.Denied, "ErrKeyDestroyed must classify as Denied")
	})

	t.Run("destroys a created key without destroying its creator's key", func(t *testing.T) {
		t.Parallel()
		creator := mustNew(t, testKeyID, rootKey)
		created, err := creator.CreateKey(t.Context())
		testkit.NoError(t, err, "CreateKey must succeed")

		_, err = creator.Destroy(t.Context(), created.KeyID())
		testkit.NoError(t, err, "the creator must destroy a created key")

		_, err = created.Wrap(t.Context(), []byte("data-key"))
		testkit.ErrorIs(t, err, crypto.ErrKeyDestroyed, "the created key must be destroyed")
		_, err = creator.Wrap(t.Context(), []byte("data-key"))
		testkit.NoError(t, err, "the creator's own key must still wrap")
	})

	t.Run("destroys a key in concurrent use", func(t *testing.T) {
		t.Parallel()
		keeper := mustNew(t, testKeyID, rootKey)
		wrapped, err := keeper.Wrap(t.Context(), []byte("data-key"))
		testkit.NoError(t, err, "Wrap must succeed")

		failures := make([]error, concurrentCallers)
		var wg sync.WaitGroup
		for i := range concurrentCallers {
			wg.Go(func() { failures[i] = useDuringDestroy(t.Context(), keeper, wrapped) })
		}

		_, err = keeper.Destroy(t.Context(), testKeyID)
		wg.Wait()
		testkit.NoError(t, err, "Destroy must succeed while callers use the key")
		for _, failure := range failures {
			testkit.NoError(t, failure, "a call that races Destroy must succeed or return ErrKeyDestroyed")
		}

		_, err = keeper.Wrap(t.Context(), []byte("data-key"))
		testkit.ErrorIs(t, err, crypto.ErrKeyDestroyed, "Wrap must return ErrKeyDestroyed once Destroy returns")
	})
}

// useDuringDestroy wraps a data key under k and unwraps wrapped. It returns
// the first error other than [crypto.ErrKeyDestroyed], which both calls
// return for a key destroyed before them.
func useDuringDestroy(ctx context.Context, k *localkey.Keeper, wrapped []byte) error {
	if _, err := k.Wrap(ctx, []byte("data-key")); err != nil && !errors.Is(err, crypto.ErrKeyDestroyed) {
		return err
	}

	if _, err := k.Unwrap(ctx, wrapped); err != nil && !errors.Is(err, crypto.ErrKeyDestroyed) {
		return err
	}

	return nil
}

// TestCreateKey covers the key IDs that CreateKey assigns, the keys that it
// returns to concurrent callers, and its failure on an exhausted random
// source.
func TestCreateKey(t *testing.T) {
	t.Parallel()

	t.Run("names created keys after the key ID passed to New", func(t *testing.T) {
		t.Parallel()
		creator := mustNew(t, testKeyID, rootKey)
		for _, want := range []string{testKeyID + "/1", testKeyID + "/2"} {
			created, err := creator.CreateKey(t.Context())
			testkit.NoError(t, err, "CreateKey must succeed")
			testkit.Equal(t, created.KeyID(), want, "CreateKey must number the keys it creates from 1")
		}
	})

	t.Run("numbers keys across every Keeper of the table", func(t *testing.T) {
		t.Parallel()
		creator := mustNew(t, testKeyID, rootKey)
		first, err := creator.CreateKey(t.Context())
		testkit.NoError(t, err, "CreateKey must succeed")

		kc, ok := crypto.AsKeyCreator(first)
		testkit.True(t, ok, "a created Keeper must be a KeyCreator")

		second, err := kc.CreateKey(t.Context())
		testkit.NoError(t, err, "a created Keeper must create keys")
		testkit.Equal(t, second.KeyID(), testKeyID+"/2", "a created Keeper must share its creator's numbering")
	})

	t.Run("returns the random source's failure without creating a key", func(t *testing.T) {
		t.Parallel()
		creator, err := localkey.New(testKeyID, rootKey,
			randcrypto.NewWithReader(&testkit.FailingReader{
				Source: bytes.NewReader(nil),
				Err:    io.ErrUnexpectedEOF,
			}), fake.New(origin))
		testkit.NoError(t, err, "New must accept a 32-byte root key")

		created, err := creator.CreateKey(t.Context())
		testkit.ErrorIs(t, err, io.ErrUnexpectedEOF, "CreateKey must return the entropy failure")
		testkit.True(t, created == nil, "no Keeper may accompany an error")

		_, err = creator.OpenKey(t.Context(), testKeyID+"/1")
		testkit.ErrorIs(t, err, crypto.ErrKeyID, "a failed CreateKey must not add a key")
	})

	t.Run("returns a distinct key to each concurrent caller", func(t *testing.T) {
		t.Parallel()
		creator := mustNew(t, testKeyID, rootKey)
		keyIDs := make([]string, concurrentCallers)
		failures := make([]error, concurrentCallers)
		var wg sync.WaitGroup
		for i := range concurrentCallers {
			wg.Go(func() { keyIDs[i], failures[i] = useCreatedKey(t.Context(), creator) })
		}
		wg.Wait()

		seen := make(map[string]bool, concurrentCallers)
		for i, keyID := range keyIDs {
			testkit.NoError(t, failures[i], "every caller must use and destroy its own key")
			testkit.False(t, seen[keyID], "no two callers may receive one key ID")
			seen[keyID] = true
		}
	})
}

// useCreatedKey creates a key through creator and wraps a data key under
// it. It unwraps the data key through the Keeper that OpenKey returns for
// the new key ID. It then destroys the key through creator and checks that
// Wrap returns [crypto.ErrKeyDestroyed]. It returns the new key ID, or the
// first error.
func useCreatedKey(ctx context.Context, creator *localkey.Keeper) (string, error) {
	created, err := creator.CreateKey(ctx)
	if err != nil {
		return "", err
	}

	wrapped, err := created.Wrap(ctx, []byte("data-key"))
	if err != nil {
		return "", err
	}

	opened, err := creator.OpenKey(ctx, created.KeyID())
	if err != nil {
		return "", err
	}

	got, err := opened.Unwrap(ctx, wrapped)
	if err != nil {
		return "", err
	}

	if !bytes.Equal(got, []byte("data-key")) {
		return "", errUnwrapped
	}

	if _, err = creator.Destroy(ctx, created.KeyID()); err != nil {
		return "", err
	}

	if _, err = created.Wrap(ctx, []byte("data-key")); !errors.Is(err, crypto.ErrKeyDestroyed) {
		return "", errStillWraps
	}

	return created.KeyID(), nil
}

// TestOpenKey covers OpenKey of the key passed to New and of a key in
// another table.
func TestOpenKey(t *testing.T) {
	t.Parallel()

	t.Run("returns a Keeper of the key passed to New", func(t *testing.T) {
		t.Parallel()
		creator := mustNew(t, testKeyID, rootKey)
		wrapped, err := creator.Wrap(t.Context(), []byte("data-key"))
		testkit.NoError(t, err, "Wrap must succeed")

		opened, err := creator.OpenKey(t.Context(), testKeyID)
		testkit.NoError(t, err, "OpenKey must open the key passed to New")
		got, err := opened.Unwrap(t.Context(), wrapped)
		testkit.NoError(t, err, "the opened Keeper must unwrap the creator's material")
		testkit.Equal(t, got, []byte("data-key"), "the opened Keeper must return the data key")
	})

	t.Run("returns ErrKeyID for a key of another table", func(t *testing.T) {
		t.Parallel()
		other := mustNew(t, "local/other-root-key", otherKey)
		_, err := mustNew(t, testKeyID, rootKey).OpenKey(t.Context(), other.KeyID())
		testkit.ErrorIs(t, err, crypto.ErrKeyID, "OpenKey must not open a key outside its table")
		testkit.Equal(t, errs.Classify(err), errs.NotFound, "ErrKeyID must classify as NotFound")
	})
}

// TestGenerateKey covers the data-key sizes that GenerateKey validates and
// its failure on an exhausted random source.
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
		t.Run("returns ErrKeySize for size "+strconv.Itoa(size), func(t *testing.T) {
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
