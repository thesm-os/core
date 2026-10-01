// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package note_test

import (
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/sign"
	"go.thesmos.sh/core/crypto/sign/ed25519"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/note"
)

// otherVerifier is a note.Verifier that reports the Key, the KeyID, the
// public key or the algorithm it sets instead of those of the Verifier it
// embeds.
type otherVerifier struct {
	note.Verifier

	key   *note.Key
	keyID *sign.KeyID
	alg   crypto.Algorithm
	pub   []byte
}

// Key returns the Key that v sets, or that of the embedded Verifier.
func (v otherVerifier) Key() note.Key {
	if v.key != nil {
		return *v.key
	}

	return v.Verifier.Key()
}

// KeyID returns the KeyID that v sets, or that of the embedded Verifier.
func (v otherVerifier) KeyID() sign.KeyID {
	if v.keyID != nil {
		return *v.keyID
	}

	return v.Verifier.KeyID()
}

// PublicKey returns the public key that v sets, or that of the embedded
// Verifier.
func (v otherVerifier) PublicKey() []byte {
	if v.pub != nil {
		return v.pub
	}

	return v.Verifier.PublicKey()
}

// Algorithm returns the algorithm that v sets, or that of the embedded
// Verifier.
func (v otherVerifier) Algorithm() crypto.Algorithm {
	if v.alg != "" {
		return v.alg
	}

	return v.Verifier.Algorithm()
}

// entryOf returns a Resolver entry that builds the Ed25519 text Verifier
// of a key and passes it to change.
func entryOf(change func(note.Verifier) note.Verifier) func(note.Key) (note.Verifier, error) {
	return func(k note.Key) (note.Verifier, error) {
		v, err := note.Text(ed25519.Resolve)(k)
		if err != nil {
			return nil, err
		}

		return change(v), nil
	}
}

func TestResolver(t *testing.T) {
	t.Parallel()

	t.Run("Verifier", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the Verifier that the entry of the type builds", func(t *testing.T) {
			t.Parallel()
			k := mustParseKey(t, peterKey)
			v, err := note.Resolver{note.TypeEd25519: note.Text(ed25519.Resolve)}.Verifier(k)
			testkit.NoError(t, err, "Verifier must resolve a type with an entry")
			assertVerifier(t, v, k)
		})

		t.Run("returns the error of the entry", func(t *testing.T) {
			t.Parallel()
			refused := testkit.TestError("the entry refuses the key")
			r := note.Resolver{note.TypeEd25519: func(note.Key) (note.Verifier, error) { return nil, refused }}
			_, err := r.Verifier(mustParseKey(t, peterKey))
			testkit.ErrorIs(t, err, refused, "Verifier must return the error of the entry")
		})

		otherKeyID := note.KeyID("another", 1)
		peter := mustParseKey(t, peterKey)
		otherName, otherType, otherPublicKey := peter, peter, peter
		otherName.Name = "PeterNeumann2"
		otherType.Type = pqType
		otherPublicKey.PublicKey = publicKey()
		tests := []struct {
			resolver note.Resolver
			name     string
		}{
			{name: "returns ErrUnknownType for an empty Resolver", resolver: note.Resolver{}},
			{
				name:     "returns ErrUnknownType for a type without an entry",
				resolver: note.Resolver{pqType: note.Text(ed25519.Resolve)},
			},
			{name: "returns ErrUnknownType for a nil entry", resolver: note.Resolver{note.TypeEd25519: nil}},
			{
				name: "returns ErrUnknownType for an entry that returns neither a Verifier nor an error",
				resolver: note.Resolver{note.TypeEd25519: func(note.Key) (note.Verifier, error) {
					return nil, nil //nolint:nilnil // the case is an entry that returns neither
				}},
			},
			{
				name: "returns ErrUnknownType for a Verifier of a key of another name",
				resolver: note.Resolver{note.TypeEd25519: entryOf(func(v note.Verifier) note.Verifier {
					return otherVerifier{Verifier: v, key: &otherName}
				})},
			},
			{
				name: "returns ErrUnknownType for a Verifier of a key of another type",
				resolver: note.Resolver{note.TypeEd25519: entryOf(func(v note.Verifier) note.Verifier {
					return otherVerifier{Verifier: v, key: &otherType}
				})},
			},
			{
				name: "returns ErrUnknownType for a Verifier of a key of another public key",
				resolver: note.Resolver{note.TypeEd25519: entryOf(func(v note.Verifier) note.Verifier {
					return otherVerifier{Verifier: v, key: &otherPublicKey}
				})},
			},
			{
				name: "returns ErrUnknownType for a Verifier of another KeyID",
				resolver: note.Resolver{note.TypeEd25519: entryOf(func(v note.Verifier) note.Verifier {
					return otherVerifier{Verifier: v, keyID: &otherKeyID}
				})},
			},
			{
				name: "returns ErrUnknownType for a Verifier of another public key",
				resolver: note.Resolver{note.TypeEd25519: entryOf(func(v note.Verifier) note.Verifier {
					return otherVerifier{Verifier: v, pub: publicKey()}
				})},
			},
			{
				name: "returns ErrUnknownType for a Verifier of another algorithm",
				resolver: note.Resolver{note.TypeEd25519: entryOf(func(v note.Verifier) note.Verifier {
					return otherVerifier{Verifier: v, alg: crypto.AlgEd25519}
				})},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				v, err := tt.resolver.Verifier(mustParseKey(t, peterKey))
				testkit.ErrorIs(t, err, note.ErrUnknownType, "Verifier must refuse the entry")
				testkit.Equal(t, errs.Classify(err), errs.Unsupported, "the error must classify as Unsupported")
				testkit.True(t, v == nil, "Verifier must return a nil Verifier with an error")
			})
		}
	})
}

func BenchmarkResolver(b *testing.B) {
	r := note.Resolver{note.TypeEd25519: note.Text(ed25519.Resolve)}
	k := mustParseKey(b, peterKey)

	b.Run("Verifier", func(b *testing.B) {
		benchAllocs(b, 2, func() { sinkVerifier, errSink = r.Verifier(k) })
	})
}
