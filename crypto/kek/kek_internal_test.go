// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package kek

import (
	"bytes"
	"crypto/hkdf"
	"crypto/sha256"
	"errors"
	"hash"
	"runtime"
	"sync"
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/aesgcm"
	randcrypto "go.thesmos.sh/core/rand/crypto"
)

// These tests are in package kek because they read and set the
// Keeper's unexported state: its wrap limit, its cipher cache, its KDF
// and its KEK.

// testKeyID is the key ID of the Keepers these tests build.
const testKeyID = "kek-internal/tenant"

// testDEK is the data key these tests wrap.
var testDEK = bytes.Repeat([]byte{0x5A}, 32)

// errDerive is the derivation failure that failingKDF returns.
var errDerive = errors.New("kek: derivation failed in a test")

// failingKDF is a kdf that always fails.
func failingKDF(func() hash.Hash, []byte, []byte, string, int) ([]byte, error) {
	return nil, errDerive
}

// newTestKeeper returns a Keeper over a random KEK, and closes it when
// the test ends.
func newTestKeeper(tb testing.TB) *Keeper {
	tb.Helper()

	kek := make([]byte, KeySize)
	_, err := randcrypto.New().Read(kek)
	testkit.NoError(tb, err, "the KEK must read")

	k := newKeeper(kek, testKeyID, frameKeyID(testKeyID), randcrypto.New())
	tb.Cleanup(func() { _ = k.Close() })

	return k
}

// twin returns a second Keeper over the KEK and key ID of k, with its own
// cipher cache, and closes it when the test ends.
func twin(tb testing.TB, k *Keeper) *Keeper {
	tb.Helper()

	other := newKeeper(bytes.Clone(k.kek), k.keyID, bytes.Clone(k.aad), randcrypto.New())
	tb.Cleanup(func() { _ = other.Close() })

	return other
}

// saltOf returns the salt at the start of a wrapped DEK.
func saltOf(sealed []byte) [SaltSize]byte {
	return [SaltSize]byte(sealed[:SaltSize])
}

func TestReserve(t *testing.T) {
	t.Parallel()

	t.Run("derives the next wrapping key after the limit", func(t *testing.T) {
		t.Parallel()
		k := newTestKeeper(t)
		k.limit = 4
		reader := twin(t, k)

		salts := map[[SaltSize]byte]int{}
		for range 10 {
			sealed, err := k.Wrap(t.Context(), testDEK)
			testkit.NoError(t, err, "Wrap must succeed")
			salts[saltOf(sealed)]++

			got, err := reader.Unwrap(t.Context(), sealed)
			testkit.NoError(t, err, "every wrapped DEK must unwrap")
			testkit.Equal(t, got, testDEK, "Unwrap must return the DEK")
		}

		testkit.Len(t, salts, 3, "10 wraps at a limit of 4 must use 3 salts")
		for _, n := range salts {
			testkit.True(t, n <= 4, "no salt may appear in more wraps than the limit")
		}
	})

	t.Run("keeps each salt within the limit under concurrent wraps", func(t *testing.T) {
		t.Parallel()
		k := newTestKeeper(t)
		k.limit = 8

		const wrappers, wrapsEach = 8, 50

		var (
			wg       sync.WaitGroup
			mu       sync.Mutex
			salts    = map[[SaltSize]byte]int{}
			failures []error
		)
		for range wrappers {
			wg.Go(func() {
				for range wrapsEach {
					sealed, err := k.Wrap(t.Context(), testDEK)
					mu.Lock()
					if err != nil {
						failures = append(failures, err)
					} else {
						salts[saltOf(sealed)]++
					}
					mu.Unlock()
				}
			})
		}
		wg.Wait()

		testkit.Len(t, failures, 0, "every wrap must succeed")
		total := 0
		for _, n := range salts {
			testkit.True(t, n <= 8, "no salt may appear in more wraps than the limit")
			total += n
		}
		testkit.Equal(t, total, wrappers*wrapsEach, "every wrap must carry a salt")
	})

	t.Run("seals at most 2^30 DEKs under one wrapping key", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, newTestKeeper(t).limit, uint64(1)<<30, "the limit must be 2^30")
	})
}

func TestCipher(t *testing.T) {
	t.Parallel()

	t.Run("serves the current wrapping key without a derivation", func(t *testing.T) {
		t.Parallel()
		k := newTestKeeper(t)
		sealed, err := k.Wrap(t.Context(), testDEK)
		testkit.NoError(t, err, "Wrap must succeed")

		_, err = k.Unwrap(t.Context(), sealed)
		testkit.NoError(t, err, "Unwrap must succeed")
		testkit.Len(t, k.ciphers, 0, "a DEK of the current wrapping key must not fill the cache")
	})

	t.Run("derives one cipher for the DEKs of one salt", func(t *testing.T) {
		t.Parallel()
		k := newTestKeeper(t)
		reader := twin(t, k)

		for range 100 {
			sealed, err := k.Wrap(t.Context(), testDEK)
			testkit.NoError(t, err, "Wrap must succeed")
			_, err = reader.Unwrap(t.Context(), sealed)
			testkit.NoError(t, err, "Unwrap must succeed")
		}

		testkit.Len(t, reader.ciphers, 1, "the DEKs of one salt must share one derived cipher")
	})

	t.Run("rejects a salt with no envelope before it derives anything", func(t *testing.T) {
		t.Parallel()
		k := newTestKeeper(t)

		_, err := k.Unwrap(t.Context(), make([]byte, SaltSize))
		testkit.ErrorIs(t, err, crypto.ErrCiphertextShort, "a salt alone must be refused")
		testkit.Len(t, k.ciphers, 0, "a refused unwrap must not derive a cipher")
	})

	t.Run("keeps at most cipherCacheSize ciphers", func(t *testing.T) {
		t.Parallel()
		k := newTestKeeper(t)
		k.limit = 1
		reader := twin(t, k)

		for range 2 * cipherCacheSize {
			sealed, err := k.Wrap(t.Context(), testDEK)
			testkit.NoError(t, err, "Wrap must succeed")
			_, err = reader.Unwrap(t.Context(), sealed)
			testkit.NoError(t, err, "Unwrap must succeed")
			testkit.True(t, len(reader.ciphers) <= cipherCacheSize,
				"the cache must hold at most cipherCacheSize ciphers")
		}
	})
}

func TestNewCipher(t *testing.T) {
	t.Parallel()

	t.Run("derives with HKDF-SHA-256 over the KEK, the salt and the framed key ID", func(t *testing.T) {
		t.Parallel()
		k := newTestKeeper(t)
		sealed, err := k.Wrap(t.Context(), testDEK)
		testkit.NoError(t, err, "Wrap must succeed")

		salt := saltOf(sealed)
		key, err := hkdf.Key(sha256.New, k.kek, salt[:], string(frameKeyID(testKeyID)), aesgcm.KeySize256)
		testkit.NoError(t, err, "hkdf.Key must derive the wrapping key")
		a, err := aesgcm.NewRandomNonce(key)
		testkit.NoError(t, err, "aesgcm.NewRandomNonce must accept the wrapping key")

		got, err := crypto.Open(a, sealed[SaltSize:], frameKeyID(testKeyID))
		testkit.NoError(t, err, "the independently derived key must open the DEK")
		testkit.Equal(t, got, testDEK, "the envelope must contain the DEK")
	})

	t.Run("a failed derivation fails Wrap", func(t *testing.T) {
		t.Parallel()
		k := newTestKeeper(t)
		k.derive = failingKDF

		_, err := k.Wrap(t.Context(), testDEK)
		testkit.ErrorIs(t, err, errDerive, "Wrap must return the derivation's error")
		testkit.True(t, k.current.Load() == nil, "a failed derivation must not become the wrapping key")
	})

	t.Run("a failed derivation fails Unwrap and is not cached", func(t *testing.T) {
		t.Parallel()
		k := newTestKeeper(t)
		sealed, err := k.Wrap(t.Context(), testDEK)
		testkit.NoError(t, err, "Wrap must succeed")

		reader := twin(t, k)
		reader.derive = failingKDF

		_, err = reader.Unwrap(t.Context(), sealed)
		testkit.ErrorIs(t, err, errDerive, "Unwrap must return the derivation's error")
		testkit.Len(t, reader.ciphers, 0, "a failed derivation must not be cached")
	})
}

// BenchmarkNewCipher reports the cost and the allocations of one
// derivation: the price of an Unwrap for a salt the Keeper has not seen,
// and of the Wrap that starts a wrapping key.
func BenchmarkNewCipher(b *testing.B) {
	k := newTestKeeper(b)
	var salt [SaltSize]byte

	b.ReportAllocs()
	for b.Loop() {
		_, _ = k.newCipher(salt)
	}
}

func TestCleanup(t *testing.T) {
	t.Parallel()

	t.Run("zeroes the KEK of a Keeper that becomes unreachable", func(t *testing.T) {
		t.Parallel()
		kek := bytes.Repeat([]byte{0x5A}, KeySize)
		newKeeper(kek, testKeyID, frameKeyID(testKeyID), randcrypto.New())

		zeroed := make([]byte, KeySize)
		for range 100 {
			if bytes.Equal(kek, zeroed) {
				break
			}
			runtime.GC()
			time.Sleep(time.Millisecond)
		}
		testkit.Equal(t, kek, zeroed, "the cleanup must zero the KEK")
	})

	t.Run("Close zeroes the KEK", func(t *testing.T) {
		t.Parallel()
		k := newTestKeeper(t)
		kek := k.kek

		testkit.NoError(t, k.Close(), "Close must succeed")
		testkit.Equal(t, kek, make([]byte, KeySize), "Close must zero the KEK")
	})
}
