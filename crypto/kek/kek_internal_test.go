// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package kek

import (
	"bytes"
	"crypto/hkdf"
	"crypto/sha256"
	"errors"
	"hash"
	"runtime"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/aesgcm"
	randcrypto "go.thesmos.sh/core/rand/crypto"
)

// These tests are in package kek because they read and set the
// Keeper's unexported state: its wrap limit, its cipher cache, its KDF
// and its KEK.

// testKeyID is the key ID of the Keepers these tests build.
const testKeyID = "kek-internal/tenant"

var (
	// testDEK is the data key these tests wrap.
	testDEK = bytes.Repeat([]byte{0x5A}, 32)

	// errDerive is the derivation failure of failingKDF.
	errDerive = errors.New("kek: derivation failed in a test")

	// failingKDF is a kdf that always fails with errDerive.
	failingKDF kdf = func(func() hash.Hash, []byte, []byte, string, int) ([]byte, error) {
		return nil, errDerive
	}
)

func TestKeeperState(t *testing.T) {
	t.Parallel()

	t.Run("reserve", func(t *testing.T) {
		t.Parallel()

		t.Run("derives the next wrapping key after the limit", func(t *testing.T) {
			t.Parallel()
			k := newTestKeeper(t)
			k.limit = 4
			reader := twin(t, k)
			salts := map[[SaltSize]byte]int{}
			for range 10 {
				sealed, err := k.Wrap(t.Context(), testDEK)
				assert.NoError(t, err, "Wrap must succeed")
				salts[[SaltSize]byte(sealed[:SaltSize])]++
				got, err := reader.Unwrap(t.Context(), sealed)
				assert.NoError(t, err, "every wrapped DEK must unwrap")
				assert.Equal(t, got, testDEK, "Unwrap must return the DEK")
			}
			assert.Length(t, salts, 3, "10 wraps at a limit of 4 must use 3 salts")
			for _, n := range salts {
				expect.InRange(t, n, 1, 4, "no salt may appear in more wraps than the limit")
			}
		})

		t.Run("keeps each salt within the limit under concurrent wraps", func(t *testing.T) {
			t.Parallel()
			k := newTestKeeper(t)
			k.limit = 8
			const wrappers, wrapsEach = 8, 50
			outcomes := history.Concurrently(wrappers, 10*time.Second, func(int) (any, error) {
				var salts [][SaltSize]byte
				for range wrapsEach {
					sealed, err := k.Wrap(t.Context(), testDEK)
					if err != nil {
						return nil, err
					}
					salts = append(salts, [SaltSize]byte(sealed[:SaltSize]))
				}

				return salts, nil
			})
			counts := map[[SaltSize]byte]int{}
			for _, o := range outcomes {
				assert.True(t, o.Finished, "every client must finish")
				assert.NoError(t, o.Error, "every wrap must succeed")
				salts, _ := o.Output.([][SaltSize]byte)
				assert.Length(t, salts, wrapsEach, "every wrap must carry a salt")
				for _, salt := range salts {
					counts[salt]++
				}
			}
			for _, n := range counts {
				expect.InRange(t, n, 1, 8, "no salt may appear in more wraps than the limit")
			}
		})

		t.Run("seals at most 2^30 DEKs under one wrapping key", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, newTestKeeper(t).limit, uint64(1)<<30, "the limit must be 2^30")
		})
	})

	t.Run("rotate", func(t *testing.T) {
		t.Parallel()

		t.Run("reserves a wrap on a current key that has room", func(t *testing.T) {
			t.Parallel()
			k := newTestKeeper(t)
			k.limit = 4
			current, err := k.rotate()
			assert.NoError(t, err, "the first rotate must derive a wrapping key")
			current.wraps.Store(k.limit - 1)
			w, err := k.rotate()
			assert.NoError(t, err, "rotate must succeed")
			assert.Equal(t, w, current, "rotate must reserve the wrap on the current key", assert.ByIdentity())
			assert.Equal(t, w.wraps.Load(), k.limit, "the reservation must count against the limit")
		})

		t.Run("derives a new wrapping key when the current key is spent", func(t *testing.T) {
			t.Parallel()
			k := newTestKeeper(t)
			k.limit = 4
			spent, err := k.rotate()
			assert.NoError(t, err, "the first rotate must derive a wrapping key")
			spent.wraps.Store(k.limit)
			w, err := k.rotate()
			assert.NoError(t, err, "rotate must succeed")
			expect.NotEqual(t, w, spent, "rotate must not reserve a wrap on a spent key", expect.ByIdentity())
			expect.Equal(t, k.current.Load(), w, "the new key must become the current key", expect.ByIdentity())
			expect.Equal(t, w.wraps.Load(), uint64(1), "the new key must carry the wrap of the caller")
		})
	})

	t.Run("cipher", func(t *testing.T) {
		t.Parallel()

		t.Run("serves the current wrapping key without a derivation", func(t *testing.T) {
			t.Parallel()
			k := newTestKeeper(t)
			sealed, err := k.Wrap(t.Context(), testDEK)
			assert.NoError(t, err, "Wrap must succeed")
			_, err = k.Unwrap(t.Context(), sealed)
			assert.NoError(t, err, "Unwrap must succeed")
			assert.Empty(t, k.ciphers, "a DEK of the current wrapping key must not fill the cache")
		})

		t.Run("derives one cipher for the DEKs of one salt", func(t *testing.T) {
			t.Parallel()
			k := newTestKeeper(t)
			reader := twin(t, k)
			for range 100 {
				sealed, err := k.Wrap(t.Context(), testDEK)
				assert.NoError(t, err, "Wrap must succeed")
				_, err = reader.Unwrap(t.Context(), sealed)
				assert.NoError(t, err, "Unwrap must succeed")
			}
			assert.Length(t, reader.ciphers, 1, "the DEKs of one salt must share one derived cipher")
		})

		t.Run("derives no cipher for material that Unwrap refuses as short", func(t *testing.T) {
			t.Parallel()
			k := newTestKeeper(t)
			_, err := k.Unwrap(t.Context(), make([]byte, SaltSize))
			assert.ErrorIs(t, err, crypto.ErrCiphertextShort, "a salt alone must be refused")
			assert.Empty(t, k.ciphers, "a refused unwrap must not derive a cipher")
		})

		t.Run("keeps the cipher of every salt until a new one would exceed cipherCacheSize", func(t *testing.T) {
			t.Parallel()
			k := newTestKeeper(t)
			k.limit = 1
			reader := twin(t, k)
			for i := range 2 * cipherCacheSize {
				sealed, err := k.Wrap(t.Context(), testDEK)
				assert.NoError(t, err, "Wrap must succeed")
				_, err = reader.Unwrap(t.Context(), sealed)
				assert.NoError(t, err, "Unwrap must succeed")
				assert.Length(t, reader.ciphers, i%cipherCacheSize+1, "the cache must empty only when it is full")
			}
		})
	})

	t.Run("newCipher", func(t *testing.T) {
		t.Parallel()

		t.Run("derives the wrapping key with HKDF-SHA-256 over the KEK, the salt and the framed key ID",
			func(t *testing.T) {
				t.Parallel()
				k := newTestKeeper(t)
				sealed, err := k.Wrap(t.Context(), testDEK)
				assert.NoError(t, err, "Wrap must succeed")
				key, err := hkdf.Key(sha256.New, k.kek, sealed[:SaltSize], string(frameKeyID(testKeyID)),
					aesgcm.KeySize256)
				assert.NoError(t, err, "hkdf.Key must derive the wrapping key")
				a, err := aesgcm.NewRandomNonce(key)
				assert.NoError(t, err, "aesgcm.NewRandomNonce must accept the wrapping key")
				got, err := crypto.Open(a, sealed[SaltSize:], frameKeyID(testKeyID))
				assert.NoError(t, err, "the independently derived key must open the DEK")
				assert.Equal(t, got, testDEK, "the envelope must contain the DEK")
			})

		t.Run("fails Wrap when the derivation fails", func(t *testing.T) {
			t.Parallel()
			k := newTestKeeper(t)
			k.derive = failingKDF
			_, err := k.Wrap(t.Context(), testDEK)
			assert.ErrorIs(t, err, errDerive, "Wrap must return the error of the derivation")
		})

		t.Run("leaves no wrapping key when the derivation fails", func(t *testing.T) {
			t.Parallel()
			k := newTestKeeper(t)
			k.derive = failingKDF
			_, err := k.Wrap(t.Context(), testDEK)
			assert.HasError(t, err, "the test must wrap with a derivation that fails")
			assert.Nil(t, k.current.Load(), "a failed derivation must not become the wrapping key")
		})

		t.Run("fails Unwrap when the derivation fails", func(t *testing.T) {
			t.Parallel()
			k := newTestKeeper(t)
			sealed, err := k.Wrap(t.Context(), testDEK)
			assert.NoError(t, err, "Wrap must succeed")
			reader := twin(t, k)
			reader.derive = failingKDF
			_, err = reader.Unwrap(t.Context(), sealed)
			assert.ErrorIs(t, err, errDerive, "Unwrap must return the error of the derivation")
		})

		t.Run("zeroes the derived key", func(t *testing.T) {
			t.Parallel()
			k := newTestKeeper(t)
			var derived []byte
			k.derive = func(h func() hash.Hash, secret, salt []byte, info string, n int) ([]byte, error) {
				key, err := hkdf.Key(h, secret, salt, info, n)
				derived = key

				return key, err //nolint:wrapcheck // the test double passes the error through
			}
			_, err := k.Wrap(t.Context(), testDEK)
			assert.NoError(t, err, "Wrap must succeed")
			assert.Equal(t, derived, make([]byte, aesgcm.KeySize256), "the derived key must be zeroed after use")
		})

		t.Run("caches no cipher when the derivation fails", func(t *testing.T) {
			t.Parallel()
			k := newTestKeeper(t)
			sealed, err := k.Wrap(t.Context(), testDEK)
			assert.NoError(t, err, "Wrap must succeed")
			reader := twin(t, k)
			reader.derive = failingKDF
			_, err = reader.Unwrap(t.Context(), sealed)
			assert.HasError(t, err, "the test must unwrap with a derivation that fails")
			assert.Empty(t, reader.ciphers, "a failed derivation must not be cached")
		})
	})

	t.Run("newKeeper", func(t *testing.T) {
		t.Parallel()

		t.Run("zeroes the KEK of a Keeper that becomes unreachable", func(t *testing.T) {
			t.Parallel()
			kek := bytes.Repeat([]byte{0x5A}, KeySize)
			newKeeper(kek, testKeyID, frameKeyID(testKeyID), randcrypto.New())
			zeroed := make([]byte, KeySize)
			assert.EventuallyTrue(t, 5*time.Second, func() bool {
				runtime.GC()

				return bytes.Equal(kek, zeroed)
			}, "the cleanup must zero the KEK")
		})
	})

	t.Run("Close", func(t *testing.T) {
		t.Parallel()

		t.Run("zeroes the KEK", func(t *testing.T) {
			t.Parallel()
			k := newTestKeeper(t)
			kek := k.kek
			assert.NoError(t, k.Close(), "Close must succeed")
			assert.Equal(t, kek, make([]byte, KeySize), "Close must zero the KEK")
		})

		t.Run("drops the current wrapping key", func(t *testing.T) {
			t.Parallel()
			k := newTestKeeper(t)
			_, err := k.Wrap(t.Context(), testDEK)
			assert.NoError(t, err, "Wrap must succeed")
			assert.NoError(t, k.Close(), "Close must succeed")
			assert.Nil(t, k.current.Load(), "Close must drop the wrapping key")
		})

		t.Run("empties the cache of derived ciphers", func(t *testing.T) {
			t.Parallel()
			k := newTestKeeper(t)
			sealed, err := k.Wrap(t.Context(), testDEK)
			assert.NoError(t, err, "Wrap must succeed")
			reader := twin(t, k)
			_, err = reader.Unwrap(t.Context(), sealed)
			assert.NoError(t, err, "Unwrap must derive and cache a cipher")
			assert.NoError(t, reader.Close(), "Close must succeed")
			assert.Empty(t, reader.ciphers, "Close must drop the derived ciphers")
		})
	})
}

// BenchmarkKeeperState reports the cost of one derivation: the price of
// an Unwrap for a salt that the Keeper has not seen, and of the Wrap
// that starts a wrapping key. A derivation is a cold path, so the
// benchmark sets no allocation ceiling.
func BenchmarkKeeperState(b *testing.B) {
	b.Run("newCipher", func(b *testing.B) {
		k := newTestKeeper(b)
		salt := make([]byte, SaltSize)
		var err error

		c := bench.Start(b)
		defer c.End()

		for c.Loop() {
			_, err = k.newCipher(salt)
		}

		assert.NoError(b, err, "the benchmark must measure a derivation that succeeds")
	})
}

// newTestKeeper returns a Keeper over a random KEK, and closes it when
// the test ends.
func newTestKeeper(tb testing.TB) *Keeper {
	tb.Helper()

	kek := make([]byte, KeySize)
	_, err := randcrypto.New().Read(kek)
	assert.NoError(tb, err, "the KEK must read")

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
