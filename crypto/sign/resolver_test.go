// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package sign_test

import (
	"slices"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/sign"
	"go.thesmos.sh/core/crypto/sign/ed25519"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/rand"
	"go.thesmos.sh/core/rand/seeded"
)

// resolveAllocs is the ceiling of a resolution of an Ed25519 key: the
// Verifier that ed25519.Resolve builds, with its copy of the key, in one
// allocation.
const resolveAllocs = 1

func TestResolver(t *testing.T) {
	t.Parallel()

	signer, err := ed25519.Generate(seeded.New(rand.Seed(1)))
	assert.NoError(t, err, "ed25519.Generate must succeed")

	t.Run("Verifier", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the Verifier that the entry builds", func(t *testing.T) {
			t.Parallel()
			r := sign.Resolver{crypto.AlgEd25519: ed25519.Resolve}
			v, err := r.Verifier(crypto.AlgEd25519, signer.PublicKey())
			assert.NoError(t, err, "a listed algorithm must resolve")
			sig, err := signer.Sign(message)
			assert.NoError(t, err, "Sign must succeed")
			expect.Equal(t, v.KeyID(), signer.KeyID(), "the Verifier must have the KeyID of the key")
			expect.True(t, v.Verify(message, sig), "the Verifier must accept the signature of the key")
		})

		t.Run("returns Verifiers of the key to goroutines that resolve at once", func(t *testing.T) {
			t.Parallel()
			r := sign.Resolver{crypto.AlgEd25519: ed25519.Resolve}
			sig, err := signer.Sign(message)
			assert.NoError(t, err, "Sign must succeed")
			outcomes := history.Concurrently(goroutines, 10*time.Second, func(int) (any, error) {
				verified := make([]bool, 0, rounds)
				for range rounds {
					v, err := r.Verifier(crypto.AlgEd25519, signer.PublicKey())
					if err != nil {
						return verified, err
					}
					verified = append(verified, v.Verify(message, sig))
				}

				return verified, nil
			})
			for _, o := range outcomes {
				assert.True(t, o.Finished, "every goroutine must finish")
				assert.NoError(t, o.Error, "Verifier must resolve the key on every goroutine")
				verified, _ := o.Output.([]bool)
				assert.Equal(t, verified, slices.Repeat([]bool{true}, rounds),
					"every Verifier must accept the signature of the key")
			}
		})

		t.Run("returns ErrUnknownAlgorithm for a missing name", func(t *testing.T) {
			t.Parallel()
			r := sign.Resolver{crypto.AlgEd25519: ed25519.Resolve}
			_, err := r.Verifier(crypto.AlgMLDSA87, signer.PublicKey())
			assert.ErrorIs(t, err, sign.ErrUnknownAlgorithm, "an unlisted algorithm must be refused")
		})

		t.Run("returns an error of class Unsupported for a missing name", func(t *testing.T) {
			t.Parallel()
			r := sign.Resolver{crypto.AlgEd25519: ed25519.Resolve}
			_, err := r.Verifier(crypto.AlgMLDSA87, signer.PublicKey())
			assert.Equal(t, errs.Classify(err), errs.Unsupported, "ErrUnknownAlgorithm must classify as Unsupported")
		})

		t.Run("returns a nil Verifier for a missing name", func(t *testing.T) {
			t.Parallel()
			r := sign.Resolver{crypto.AlgEd25519: ed25519.Resolve}
			v, err := r.Verifier(crypto.AlgMLDSA87, signer.PublicKey())
			assert.HasError(t, err, "the test must resolve a missing name")
			assert.Nil(t, v, "the Verifier must be a nil interface")
		})

		t.Run("returns ErrUnknownAlgorithm for a nil entry", func(t *testing.T) {
			t.Parallel()
			r := sign.Resolver{crypto.AlgEd25519: nil}
			_, err := r.Verifier(crypto.AlgEd25519, signer.PublicKey())
			assert.ErrorIs(t, err, sign.ErrUnknownAlgorithm, "a nil entry must be refused")
		})

		t.Run("returns the error of the entry", func(t *testing.T) {
			t.Parallel()
			r := sign.Resolver{crypto.AlgEd25519: ed25519.Resolve}
			_, err := r.Verifier(crypto.AlgEd25519, make([]byte, 31))
			assert.ErrorIs(t, err, ed25519.ErrInvalidPublicKeySize, "the error of the entry must be returned")
		})

		t.Run("returns a nil Verifier with the error of the entry", func(t *testing.T) {
			t.Parallel()
			r := sign.Resolver{crypto.AlgEd25519: ed25519.Resolve}
			v, err := r.Verifier(crypto.AlgEd25519, make([]byte, 31))
			assert.HasError(t, err, "the test must resolve a key that the entry refuses")
			assert.Nil(t, v, "the Verifier must be a nil interface")
		})

		t.Run("returns ErrUnknownAlgorithm for an entry of another algorithm", func(t *testing.T) {
			t.Parallel()
			wrong := func([]byte) (sign.Verifier, error) {
				return &key{pub: []byte("k"), alg: crypto.AlgMLDSA44}, nil
			}
			r := sign.Resolver{crypto.AlgEd25519: wrong}
			_, err := r.Verifier(crypto.AlgEd25519, signer.PublicKey())
			assert.ErrorIs(t, err, sign.ErrUnknownAlgorithm, "an entry for the wrong algorithm must be refused")
		})

		t.Run("returns ErrUnknownAlgorithm for an entry that returns no verifier", func(t *testing.T) {
			t.Parallel()
			empty := func([]byte) (sign.Verifier, error) { return nil, nil }
			r := sign.Resolver{crypto.AlgEd25519: empty}
			_, err := r.Verifier(crypto.AlgEd25519, signer.PublicKey())
			assert.ErrorIs(t, err, sign.ErrUnknownAlgorithm, "an entry without a verifier must be refused")
		})
	})
}

// TestResolverAllocs checks that Verifier allocates only what its entry
// allocates. MaxAllocs counts the allocations of the whole process, so
// the test does not run in parallel.
func TestResolverAllocs(t *testing.T) {
	signer, err := ed25519.Generate(seeded.New(rand.Seed(1)))
	assert.NoError(t, err, "ed25519.Generate must succeed")
	r := sign.Resolver{crypto.AlgEd25519: ed25519.Resolve}
	pub := signer.PublicKey()

	t.Run("Verifier", func(t *testing.T) {
		var v sign.Verifier
		expect.MaxAllocs(t, func() { v, _ = r.Verifier(crypto.AlgEd25519, pub) }, resolveAllocs,
			"Verifier must allocate only what ed25519.Resolve allocates")
		assert.NotNil(t, v, "the test must measure a resolution that succeeds")
	})
}

// BenchmarkResolver reports the cost of the resolution of an Ed25519
// key, the lookup and the copy of the key by the constructor, and fails
// when it allocates more than TestResolverAllocs allows.
func BenchmarkResolver(b *testing.B) {
	b.Run("Verifier", func(b *testing.B) {
		signer, err := ed25519.Generate(seeded.New(rand.Seed(1)))
		assert.NoError(b, err, "ed25519.Generate must succeed")
		r := sign.Resolver{crypto.AlgEd25519: ed25519.Resolve}
		pub := signer.PublicKey()
		var v sign.Verifier

		c := bench.Start(b).MaxAllocs(resolveAllocs)
		defer c.End()

		for c.Loop() {
			v, _ = r.Verifier(crypto.AlgEd25519, pub)
		}

		assert.NotNil(b, v, "the benchmark must measure a resolution that succeeds")
	})
}
