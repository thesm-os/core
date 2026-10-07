// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package note_test

import (
	"sync/atomic"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/crypto/sign/ed25519"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/note"
)

func TestKeyring(t *testing.T) {
	t.Parallel()

	t.Run("Verifier", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the Verifier that the Resolver builds", func(t *testing.T) {
			t.Parallel()
			var keys note.Keyring
			keys.Reset(note.Resolver{note.TypeEd25519: note.Text(ed25519.Resolve)})
			assertVerifier(t, mustVerifier(t, &keys, mustParseKey(t, peterKey)), mustParseKey(t, peterKey))
		})

		t.Run("returns the Verifier that it built for the key in the load before", func(t *testing.T) {
			t.Parallel()
			calls := &atomic.Int64{}
			r := countingResolver(calls)
			var keys note.Keyring
			keys.Reset(r)
			first := mustVerifier(t, &keys, mustParseKey(t, peterKey))
			keys.Reset(r)
			expect.Equal(t, mustVerifier(t, &keys, mustParseKey(t, peterKey)), first,
				"Verifier must return the Verifier of the load before", expect.ByIdentity())
			expect.Equal(t, calls.Load(), int64(1), "the Resolver must build the Verifier once")
		})

		t.Run("returns the Verifier that it built for the key in the current load", func(t *testing.T) {
			t.Parallel()
			calls := &atomic.Int64{}
			var keys note.Keyring
			keys.Reset(countingResolver(calls))
			first := mustVerifier(t, &keys, mustParseKey(t, peterKey))
			expect.Equal(t, mustVerifier(t, &keys, mustParseKey(t, peterKey)), first,
				"Verifier must return the Verifier that the load built", expect.ByIdentity())
			expect.Equal(t, calls.Load(), int64(1), "the Resolver must build the Verifier once")
		})

		t.Run("returns the Verifier of each of four keys that differ in one part", func(t *testing.T) {
			t.Parallel()
			base := note.Key{Name: "a", Type: note.TypeEd25519, PublicKey: publicKey()}
			otherName, otherType, otherPublicKey := base, base, base
			otherName.Name = "b"
			otherType.Type = pqType
			otherPublicKey.PublicKey = make([]byte, len(base.PublicKey))
			r := countingResolver(&atomic.Int64{})
			var keys note.Keyring
			for range 2 {
				keys.Reset(r)
				for _, k := range []note.Key{otherPublicKey, base, otherType, otherName} {
					assertVerifier(t, mustVerifier(t, &keys, k), k)
				}
			}
		})

		t.Run("returns ErrUnknownType for a type without an entry in the Resolver of the load", func(t *testing.T) {
			t.Parallel()
			var keys note.Keyring
			keys.Reset(note.Resolver{note.TypeEd25519: note.Text(ed25519.Resolve)})
			mustVerifier(t, &keys, mustParseKey(t, peterKey))
			keys.Reset(note.Resolver{pqType: note.Text(ed25519.Resolve)})
			v, err := keys.Verifier(mustParseKey(t, peterKey))
			expect.ErrorIs(t, err, note.ErrUnknownType, "Verifier must refuse a type that the load does not resolve")
			expect.Equal(t, errs.Classify(err), errs.Unsupported, "the error must classify as Unsupported")
			expect.Nil(t, v, "Verifier must return a nil Verifier with an error")
		})

		t.Run("returns ErrUnknownType for every key of the zero Keyring", func(t *testing.T) {
			t.Parallel()
			var keys note.Keyring
			_, err := keys.Verifier(mustParseKey(t, peterKey))
			assert.ErrorIs(t, err, note.ErrUnknownType, "the zero Keyring must resolve nothing")
		})

		t.Run("returns the error of the Resolver", func(t *testing.T) {
			t.Parallel()
			var keys note.Keyring
			keys.Reset(note.Resolver{note.TypeEd25519: note.Text(ed25519.Resolve)})
			_, err := keys.Verifier(note.Key{Name: "a", Type: note.TypeEd25519, PublicKey: []byte{1}})
			assert.ErrorIs(t, err, ed25519.ErrInvalidPublicKeySize, "Verifier must return the error of the entry")
		})
	})

	t.Run("Reset", func(t *testing.T) {
		t.Parallel()

		t.Run("drops each Verifier that the load before did not ask for", func(t *testing.T) {
			t.Parallel()
			calls := &atomic.Int64{}
			r := countingResolver(calls)
			var keys note.Keyring
			keys.Reset(r)
			first := mustVerifier(t, &keys, mustParseKey(t, peterKey))
			keys.Reset(r)
			mustVerifier(t, &keys, mustParseKey(t, enochKey))
			keys.Reset(r)
			expect.NotEqual(t, mustVerifier(t, &keys, mustParseKey(t, peterKey)), first,
				"Verifier must build the Verifier of a key that a load skipped again", expect.ByIdentity())
			expect.Equal(t, calls.Load(), int64(3), "the Resolver must build a Verifier for each load of a new key")
		})

		t.Run("keeps each Verifier that the load before asked for", func(t *testing.T) {
			t.Parallel()
			calls := &atomic.Int64{}
			r := countingResolver(calls)
			var keys note.Keyring
			keys.Reset(r)
			peter := mustVerifier(t, &keys, mustParseKey(t, peterKey))
			enoch := mustVerifier(t, &keys, mustParseKey(t, enochKey))
			for range 3 {
				keys.Reset(r)
				expect.Equal(t, mustVerifier(t, &keys, mustParseKey(t, enochKey)), enoch,
					"Verifier must return the Verifier that the Keyring keeps", expect.ByIdentity())
				expect.Equal(t, mustVerifier(t, &keys, mustParseKey(t, peterKey)), peter,
					"Verifier must return the Verifier that the Keyring keeps", expect.ByIdentity())
			}
			expect.Equal(t, calls.Load(), int64(2), "the Resolver must build each Verifier once")
		})
	})

	t.Run("Grow", func(t *testing.T) {
		t.Parallel()

		t.Run("keeps the Verifiers of the Keyring", func(t *testing.T) {
			t.Parallel()
			var keys note.Keyring
			keys.Reset(note.Resolver{note.TypeEd25519: note.Text(ed25519.Resolve)})
			first := mustVerifier(t, &keys, mustParseKey(t, peterKey))
			keys.Grow(16)
			assert.Equal(t, mustVerifier(t, &keys, mustParseKey(t, peterKey)), first,
				"Grow must keep the Verifier of the key", assert.ByIdentity())
		})
	})
}

// TestKeyringAllocs checks the allocation contract of each method.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
//
//nolint:paralleltest // see above
func TestKeyringAllocs(t *testing.T) {
	r := note.Resolver{note.TypeEd25519: note.Text(ed25519.Resolve)}
	k := mustParseKey(t, peterKey)

	t.Run("Verifier", func(t *testing.T) {
		t.Run("of a key whose Verifier the Keyring keeps", func(t *testing.T) {
			var keys note.Keyring
			keys.Reset(r)
			mustVerifier(t, &keys, k)

			var err error
			expect.MaxAllocs(t, func() { _, err = keys.Verifier(k) }, 0,
				"Verifier must not allocate for a Verifier that the Keyring keeps")
			assert.NoError(t, err, "the test must measure a key that the Keyring resolves")
		})

		t.Run("of a new key", func(t *testing.T) {
			var err error
			expect.MaxAllocsWithSetup(t, func() *note.Keyring {
				keys := &note.Keyring{}
				keys.Reset(r)

				return keys
			}, func(keys *note.Keyring) { _, err = keys.Verifier(k) }, 3,
				"Verifier must allocate the Verifier, the Ed25519 key and the growth of the Keyring alone")
			assert.NoError(t, err, "the test must measure a key that the Keyring resolves")
		})
	})

	t.Run("Reset", func(t *testing.T) {
		var keys note.Keyring
		keys.Reset(r)
		mustVerifier(t, &keys, k)

		var err error
		expect.MaxAllocs(t, func() {
			keys.Reset(r)
			_, err = keys.Verifier(k)
		}, 0, "Reset must not allocate")
		assert.NoError(t, err, "the test must measure a key that the Keyring resolves")
	})

	t.Run("Grow", func(t *testing.T) {
		var err error
		expect.MaxAllocsWithSetup(t, func() *note.Keyring {
			keys := &note.Keyring{}
			keys.Reset(r)
			keys.Grow(1)

			return keys
		}, func(keys *note.Keyring) { _, err = keys.Verifier(k) }, 2,
			"Verifier must not grow a Keyring that Grow made room in")
		assert.NoError(t, err, "the test must measure a key that the Keyring resolves")
	})
}

// BenchmarkKeyring reports the cost of each method, and fails above the
// allocations that their contracts state.
func BenchmarkKeyring(b *testing.B) {
	r := note.Resolver{note.TypeEd25519: note.Text(ed25519.Resolve)}
	k := mustParseKey(b, peterKey)

	b.Run("Verifier", func(b *testing.B) {
		b.Run("of a key whose Verifier the Keyring keeps", func(b *testing.B) {
			var keys note.Keyring
			keys.Reset(r)
			mustVerifier(b, &keys, k)

			var err error

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				_, err = keys.Verifier(k)
			}

			assert.NoError(b, err, "the benchmark must measure a key that the Keyring resolves")
		})

		b.Run("of a new key", func(b *testing.B) {
			var err error

			c := bench.Start(b).MaxAllocs(3)
			defer c.End()

			for c.Loop() {
				var keys note.Keyring
				c.Excluding(func() { keys.Reset(r) })
				_, err = keys.Verifier(k)
			}

			assert.NoError(b, err, "the benchmark must measure a key that the Keyring resolves")
		})
	})

	b.Run("Reset", func(b *testing.B) {
		var keys note.Keyring
		keys.Reset(r)
		mustVerifier(b, &keys, k)

		var err error

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			keys.Reset(r)
			_, err = keys.Verifier(k)
		}

		assert.NoError(b, err, "the benchmark must measure a key that the Keyring resolves")
	})

	b.Run("Grow", func(b *testing.B) {
		var err error

		c := bench.Start(b).MaxAllocs(2)
		defer c.End()

		for c.Loop() {
			var keys note.Keyring
			c.Excluding(func() {
				keys.Reset(r)
				keys.Grow(1)
			})
			_, err = keys.Verifier(k)
		}

		assert.NoError(b, err, "the benchmark must measure a key that the Keyring resolves")
	})
}

// countingResolver returns a Resolver whose entries for type 0x01 and for
// pqType build the Ed25519 text Verifier of a key, and add each Verifier
// that they build to calls.
func countingResolver(calls *atomic.Int64) note.Resolver {
	entry := func(k note.Key) (note.Verifier, error) {
		calls.Add(1)

		return note.Text(ed25519.Resolve)(k)
	}

	return note.Resolver{note.TypeEd25519: entry, pqType: entry}
}

// mustVerifier returns the Verifier of k in the current load of keys, and
// fails the test when the Keyring returns an error.
func mustVerifier(tb testing.TB, keys *note.Keyring, k note.Key) note.Verifier {
	tb.Helper()

	v, err := keys.Verifier(k)
	assert.NoError(tb, err, "Verifier must resolve the key")

	return v
}
