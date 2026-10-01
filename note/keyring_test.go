// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package note_test

import (
	"sync/atomic"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/crypto/sign/ed25519"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/note"
)

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
	testkit.NoError(tb, err, "Verifier must resolve the key")

	return v
}

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
			testkit.True(t, mustVerifier(t, &keys, mustParseKey(t, peterKey)) == first,
				"Verifier must return the Verifier of the load before")
			testkit.Equal(t, calls.Load(), int64(1), "the Resolver must build the Verifier once")
		})

		t.Run("returns the Verifier that it built for the key in the current load", func(t *testing.T) {
			t.Parallel()
			calls := &atomic.Int64{}
			var keys note.Keyring
			keys.Reset(countingResolver(calls))
			first := mustVerifier(t, &keys, mustParseKey(t, peterKey))
			testkit.True(t, mustVerifier(t, &keys, mustParseKey(t, peterKey)) == first,
				"Verifier must return the Verifier that the load built")
			testkit.Equal(t, calls.Load(), int64(1), "the Resolver must build the Verifier once")
		})

		t.Run("returns the Verifier of each of keys that differ in one part", func(t *testing.T) {
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
			testkit.ErrorIs(t, err, note.ErrUnknownType, "Verifier must refuse a type that the load does not resolve")
			testkit.Equal(t, errs.Classify(err), errs.Unsupported, "the error must classify as Unsupported")
			testkit.True(t, v == nil, "Verifier must return a nil Verifier with an error")
		})

		t.Run("returns ErrUnknownType for every key of the zero Keyring", func(t *testing.T) {
			t.Parallel()
			var keys note.Keyring
			_, err := keys.Verifier(mustParseKey(t, peterKey))
			testkit.ErrorIs(t, err, note.ErrUnknownType, "the zero Keyring must resolve nothing")
		})

		t.Run("returns the error of the Resolver", func(t *testing.T) {
			t.Parallel()
			var keys note.Keyring
			keys.Reset(note.Resolver{note.TypeEd25519: note.Text(ed25519.Resolve)})
			_, err := keys.Verifier(note.Key{Name: "a", Type: note.TypeEd25519, PublicKey: []byte{1}})
			testkit.ErrorIs(t, err, ed25519.ErrInvalidPublicKeySize, "Verifier must return the error of the entry")
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
			testkit.False(t, mustVerifier(t, &keys, mustParseKey(t, peterKey)) == first,
				"Verifier must build the Verifier of a key that a load skipped again")
			testkit.Equal(t, calls.Load(), int64(3), "the Resolver must build a Verifier for each load of a new key")
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
				testkit.True(t, mustVerifier(t, &keys, mustParseKey(t, enochKey)) == enoch,
					"Verifier must return the Verifier that the Keyring keeps")
				testkit.True(t, mustVerifier(t, &keys, mustParseKey(t, peterKey)) == peter,
					"Verifier must return the Verifier that the Keyring keeps")
			}
			testkit.Equal(t, calls.Load(), int64(2), "the Resolver must build each Verifier once")
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
			testkit.True(t, mustVerifier(t, &keys, mustParseKey(t, peterKey)) == first,
				"Grow must keep the Verifier of the key")
		})
	})
}

func BenchmarkKeyring(b *testing.B) {
	r := note.Resolver{note.TypeEd25519: note.Text(ed25519.Resolve)}
	k := mustParseKey(b, peterKey)

	b.Run("Verifier", func(b *testing.B) {
		var keys note.Keyring
		keys.Reset(r)
		mustVerifier(b, &keys, k)
		benchZeroAlloc(b, func() { sinkVerifier, errSink = keys.Verifier(k) })
	})

	b.Run("Verifier of a new key", func(b *testing.B) {
		benchAllocs(b, 3, func() {
			var keys note.Keyring
			keys.Reset(r)
			sinkVerifier, errSink = keys.Verifier(k)
		})
	})

	b.Run("Reset", func(b *testing.B) {
		var keys note.Keyring
		keys.Reset(r)
		mustVerifier(b, &keys, k)
		benchZeroAlloc(b, func() {
			keys.Reset(r)
			sinkVerifier, errSink = keys.Verifier(k)
		})
	})

	b.Run("Grow", func(b *testing.B) {
		var keys note.Keyring
		keys.Grow(1)
		benchZeroAlloc(b, func() { keys.Grow(1) })
	})
}
