// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package note_test

import (
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/crypto/sign"
	"go.thesmos.sh/core/crypto/sign/ed25519"
	"go.thesmos.sh/core/note"
)

// Every Verifier is a sign.Verifier, and every Signer a sign.Signer and a
// sign.AppendSigner.
var (
	_ sign.Verifier     = note.Verifier(nil)
	_ sign.Signer       = note.Signer(nil)
	_ sign.AppendSigner = note.Signer(nil)
)

// assertVerifier checks the rule of note.Verifier for v, a Verifier of
// k: its KeyID, public key and algorithm follow from its key.
func assertVerifier(tb testing.TB, v note.Verifier, k note.Key) {
	tb.Helper()

	testkit.Equal(tb, v.Key(), k, "Key must return the key of the Verifier")
	testkit.Equal(tb, v.KeyID(), note.KeyID(k.Name, k.ID()), "KeyID must return KeyID of the name and the key ID")
	testkit.Equal(tb, v.PublicKey(), k.PublicKey, "PublicKey must return the public key of the key")
	testkit.Equal(tb, v.Algorithm(), note.Algorithm, "Algorithm must return the algorithm of every note key")
}

func TestVerifier(t *testing.T) {
	t.Parallel()

	t.Run("Key", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the key whose KeyID, public key and algorithm the Verifier reports", func(t *testing.T) {
			t.Parallel()
			k := mustParseKey(t, peterKey)
			v, err := note.Text(ed25519.Resolve)(k)
			testkit.NoError(t, err, "Text must build the Verifier")
			assertVerifier(t, v, k)
		})
	})

	t.Run("Sign", func(t *testing.T) {
		t.Parallel()

		t.Run("is a sign.ContextSigner without Unwrap", func(t *testing.T) {
			t.Parallel()
			var s sign.Signer = peterSigner(t)
			_, isContextSigner := s.(sign.ContextSigner)
			testkit.True(t, isContextSigner, "a Signer must take a context")
			_, unwraps := s.(interface{ Unwrap() sign.Signer })
			testkit.False(t, unwraps, "a Signer must not expose the signer it wraps")
		})
	})
}
