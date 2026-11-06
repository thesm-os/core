// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
	"testing"

	"go.dokimi.dev/assert"

	"go.thesmos.sh/core/crypto/sign/ed25519"
	"go.thesmos.sh/core/note"
)

func TestLogInternal(t *testing.T) {
	t.Parallel()

	t.Run("keyring", func(t *testing.T) {
		t.Parallel()

		t.Run("reset", func(t *testing.T) {
			t.Parallel()

			key := newInternalLog(t, internalLogName).signer.Key()

			// verifier returns the Verifier of key that r keeps or builds.
			verifier := func(tb testing.TB, r *keyring) note.Verifier {
				tb.Helper()

				v, err := r.verifier(key)
				assert.NoError(tb, err, "the keyring must resolve the key")

				return v
			}

			t.Run("keeps the Verifier of a key that a call asked for since the reset before", func(t *testing.T) {
				t.Parallel()
				r := &keyring{resolver: note.Resolver{note.TypeEd25519: note.Text(ed25519.Resolve)}}
				r.reset()
				before := verifier(t, r)

				r.reset()
				assert.Equal(t, verifier(t, r), before, "the reset must keep the Verifier", assert.ByIdentity())
			})

			t.Run("drops the Verifier of a key that no call asked for since the reset before", func(t *testing.T) {
				t.Parallel()
				r := &keyring{resolver: note.Resolver{note.TypeEd25519: note.Text(ed25519.Resolve)}}
				r.reset()
				before := verifier(t, r)

				r.reset()
				r.reset()
				assert.NotEqual(t, verifier(t, r), before, "the reset must drop the Verifier", assert.ByIdentity())
			})
		})
	})
}
