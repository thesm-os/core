// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package sign_test

import (
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/sign"
	"go.thesmos.sh/core/crypto/sign/ed25519"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/rand"
	"go.thesmos.sh/core/rand/seeded"
)

// BenchmarkResolverVerifier measures resolving an Ed25519 key. The
// cost is the lookup plus the constructor's copy of the key.
func BenchmarkResolverVerifier(b *testing.B) {
	signer, err := ed25519.Generate(seeded.New(rand.Seed(1)))
	testkit.NoError(b, err, "ed25519.Generate must succeed")

	r := sign.Resolver{crypto.AlgEd25519: ed25519.Resolve}
	pub := signer.PublicKey()
	b.ReportAllocs()

	for b.Loop() {
		_, _ = r.Verifier(crypto.AlgEd25519, pub)
	}
}

func TestResolverVerifier(t *testing.T) {
	t.Parallel()

	signer, err := ed25519.Generate(seeded.New(rand.Seed(1)))
	testkit.NoError(t, err, "ed25519.Generate must succeed")

	t.Run("returns the Verifier the entry builds", func(t *testing.T) {
		t.Parallel()
		r := sign.Resolver{crypto.AlgEd25519: ed25519.Resolve}
		v, err := r.Verifier(crypto.AlgEd25519, signer.PublicKey())
		testkit.NoError(t, err, "a listed algorithm must resolve")
		testkit.Equal(t, v.KeyID(), signer.KeyID(), "the Verifier must have the key's KeyID")

		sig, err := signer.Sign(message)
		testkit.NoError(t, err, "Sign must succeed")
		testkit.True(t, v.Verify(message, sig), "the Verifier must accept the key's signature")
	})

	t.Run("returns ErrUnknownAlgorithm classified Unsupported for a missing name", func(t *testing.T) {
		t.Parallel()
		r := sign.Resolver{crypto.AlgEd25519: ed25519.Resolve}
		v, err := r.Verifier(crypto.AlgMLDSA87, signer.PublicKey())
		testkit.ErrorIs(t, err, sign.ErrUnknownAlgorithm, "an unlisted algorithm must be refused")
		testkit.Equal(t, errs.Classify(err), errs.Unsupported, "ErrUnknownAlgorithm must classify as Unsupported")
		testkit.True(t, v == nil, "the Verifier must be a nil interface")
	})

	t.Run("treats a nil entry as a missing name", func(t *testing.T) {
		t.Parallel()
		r := sign.Resolver{crypto.AlgEd25519: nil}
		_, err := r.Verifier(crypto.AlgEd25519, signer.PublicKey())
		testkit.ErrorIs(t, err, sign.ErrUnknownAlgorithm, "a nil entry must be refused")
	})

	t.Run("returns the entry's error and a nil Verifier", func(t *testing.T) {
		t.Parallel()
		r := sign.Resolver{crypto.AlgEd25519: ed25519.Resolve}
		v, err := r.Verifier(crypto.AlgEd25519, make([]byte, 31))
		testkit.ErrorIs(t, err, ed25519.ErrInvalidPublicKeySize, "the entry's error must be returned")
		testkit.True(t, v == nil, "the Verifier must be a nil interface")
	})

	t.Run("refuses an entry that builds a verifier for another algorithm", func(t *testing.T) {
		t.Parallel()
		wrong := func([]byte) (sign.Verifier, error) {
			return &key{pub: []byte("k"), alg: crypto.AlgMLDSA44}, nil
		}
		r := sign.Resolver{crypto.AlgEd25519: wrong}
		_, err := r.Verifier(crypto.AlgEd25519, signer.PublicKey())
		testkit.ErrorIs(t, err, sign.ErrUnknownAlgorithm, "an entry for the wrong algorithm must be refused")
	})

	t.Run("refuses an entry that returns no verifier", func(t *testing.T) {
		t.Parallel()
		empty := func([]byte) (sign.Verifier, error) { return nil, nil } //nolint:nilnil // the defect under test
		r := sign.Resolver{crypto.AlgEd25519: empty}
		_, err := r.Verifier(crypto.AlgEd25519, signer.PublicKey())
		testkit.ErrorIs(t, err, sign.ErrUnknownAlgorithm, "an entry without a verifier must be refused")
	})
}
