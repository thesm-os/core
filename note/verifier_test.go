// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package note_test

import (
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/crypto/sign"
	"go.thesmos.sh/core/note"
)

// Every Verifier is a sign.Verifier, every Signer a sign.Signer and a
// sign.AppendSigner, and the TextSigner a sign.ContextSigner.
var (
	_ sign.Verifier      = note.Verifier(nil)
	_ sign.Signer        = note.Signer(nil)
	_ sign.AppendSigner  = note.Signer(nil)
	_ sign.ContextSigner = (*note.TextSigner)(nil)
)

func TestVerifier(t *testing.T) {
	t.Parallel()

	t.Run("Key", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the key of a Verifier of Text", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, textVerifier(t, peterKey).Key(), mustParseKey(t, peterKey),
				"Key must return the key of the Verifier")
		})
	})

	t.Run("KeyID", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the KeyID of the key", func(t *testing.T) {
			t.Parallel()
			k := mustParseKey(t, peterKey)
			assert.Equal(t, textVerifier(t, peterKey).KeyID(), note.KeyID(k.Name, k.ID()),
				"KeyID must return KeyID of the name and the key ID")
		})
	})

	t.Run("PublicKey", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the public key of the key", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, textVerifier(t, peterKey).PublicKey(), mustParseKey(t, peterKey).PublicKey,
				"PublicKey must return the public key of the key")
		})
	})

	t.Run("Algorithm", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the algorithm of every note key", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, textVerifier(t, peterKey).Algorithm(), note.Algorithm,
				"Algorithm must return the algorithm of every note key")
		})
	})

	t.Run("Signer", func(t *testing.T) {
		t.Parallel()

		t.Run("is a TextSigner without an Unwrap method", func(t *testing.T) {
			t.Parallel()
			var s note.Signer = peterSigner(t)
			_, unwraps := s.(interface{ Unwrap() sign.Signer })
			assert.False(t, unwraps, "a Signer must not expose the signer that it wraps")
		})
	})
}

// assertVerifier checks the rule of note.Verifier for v, a Verifier of k:
// it returns k from Key, and the KeyID, the public key and the algorithm
// that follow from k.
func assertVerifier(tb testing.TB, v note.Verifier, k note.Key) {
	tb.Helper()

	expect.Equal(tb, v.Key(), k, "Key must return the key of the Verifier")
	expect.Equal(tb, v.KeyID(), note.KeyID(k.Name, k.ID()), "KeyID must return KeyID of the name and the key ID")
	expect.Equal(tb, v.PublicKey(), k.PublicKey, "PublicKey must return the public key of the key")
	expect.Equal(tb, v.Algorithm(), note.Algorithm, "Algorithm must return the algorithm of every note key")
}
