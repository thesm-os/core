// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package cryptotest

import (
	"bytes"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/crypto"
)

// KeeperOption is one conformance subtest over a
// [crypto.Keeper]. Hand-written rather than generated for the same
// reason as the AEAD suite — see the AEAD section of doc.go.
type KeeperOption struct {
	fn   func(*testing.T, crypto.Keeper)
	name string
}

// KeeperCustom names a conformance subtest over a Keeper produced by
// the factory.
func KeeperCustom(name string, fn func(*testing.T, crypto.Keeper)) KeeperOption {
	return KeeperOption{name: name, fn: fn}
}

// AssertKeeperContract runs opts against Keepers produced by factory,
// each in its own parallel subtest with a fresh instance.
//
// factory must return a Keeper over the same wrapping key each time:
// several assertions wrap with one instance and unwrap with another.
func AssertKeeperContract(t *testing.T, factory func() crypto.Keeper, opts ...KeeperOption) {
	t.Helper()

	for _, opt := range opts {
		t.Run(opt.name, func(t *testing.T) {
			t.Parallel()
			opt.fn(t, factory())
		})
	}
}

// KeeperContractAssertions returns the assertions every
// [crypto.Keeper] must satisfy.
//
// The properties are what make envelope encryption sound: a data key
// survives the round trip exactly, wrapping is non-deterministic so
// equal data keys are not detectable from their wrapped forms, and
// anything tampered with fails rather than returning wrong key
// material.
func KeeperContractAssertions() []KeeperOption {
	return []KeeperOption{
		KeeperCustom("a data key survives the round trip", func(t *testing.T, k crypto.Keeper) {
			for i, size := range []int{16, 32, 64} {
				dek := bytes.Repeat([]byte{byte('a' + i)}, size)

				wrapped, err := k.Wrap(t.Context(), dek)
				testkit.NoError(t, err, "Wrap must succeed")

				got, err := k.Unwrap(t.Context(), wrapped)
				testkit.NoError(t, err, "Unwrap must succeed on Wrap's own output")
				testkit.True(t, bytes.Equal(got, dek), "Unwrap must return the data key exactly")
			}
		}),

		KeeperCustom("wrapping is non-deterministic", func(t *testing.T, k crypto.Keeper) {
			// Deterministic wrapping would let a holder of two
			// wrapped keys tell whether the underlying data keys are
			// equal, without unwrapping either.
			dek := bytes.Repeat([]byte{0x5A}, 32)

			first, err := k.Wrap(t.Context(), dek)
			testkit.NoError(t, err, "Wrap must succeed")
			second, err := k.Wrap(t.Context(), dek)
			testkit.NoError(t, err, "Wrap must succeed")

			testkit.NotEqual(t, first, second,
				"wrapping one data key twice must not produce identical bytes")
		}),

		KeeperCustom("corrupted material does not unwrap", func(t *testing.T, k crypto.Keeper) {
			wrapped, err := k.Wrap(t.Context(), bytes.Repeat([]byte{0x5A}, 32))
			testkit.NoError(t, err, "Wrap must succeed")

			// Every byte position matters: a custodian that
			// authenticates only part of its output would pass a
			// single-position check.
			for i := range wrapped {
				corrupt := bytes.Clone(wrapped)
				corrupt[i] ^= 0x01

				_, err := k.Unwrap(t.Context(), corrupt)
				testkit.Error(t, err, "a flipped bit must not unwrap to key material")
			}
		}),

		KeeperCustom("truncated material does not unwrap", func(t *testing.T, k crypto.Keeper) {
			wrapped, err := k.Wrap(t.Context(), bytes.Repeat([]byte{0x5A}, 32))
			testkit.NoError(t, err, "Wrap must succeed")

			for _, n := range []int{0, 1, len(wrapped) / 2, len(wrapped) - 1} {
				_, err := k.Unwrap(t.Context(), wrapped[:n])
				testkit.Error(t, err, "truncated material must not unwrap")
			}
		}),

		KeeperCustom("KeyID is non-empty and stable", func(t *testing.T, k crypto.Keeper) {
			// KeyID is persisted with every wrapped key so material
			// wrapped before a rotation can still be unwrapped after
			// one. An unstable value would strand it.
			testkit.NotEqual(t, k.KeyID(), "", "KeyID must name the wrapping key")
			testkit.Equal(t, k.KeyID(), k.KeyID(), "KeyID must be stable")
		}),

		KeeperCustom("an empty data key round-trips", func(t *testing.T, k crypto.Keeper) {
			// Not a useful data key, but the seam must not treat
			// empty input as a sentinel for absent input.
			wrapped, err := k.Wrap(t.Context(), nil)
			testkit.NoError(t, err, "Wrap must accept an empty data key")

			got, err := k.Unwrap(t.Context(), wrapped)
			testkit.NoError(t, err, "Unwrap must succeed")
			testkit.Equal(t, len(got), 0, "an empty data key must round-trip to empty")
		}),
	}
}

// KeeperCrossInstanceAssertion asserts material wrapped by one
// instance unwraps under another over the same wrapping key. A
// custodian caching per-instance state fails here rather than in
// production, where the two instances are separate processes.
func KeeperCrossInstanceAssertion(other func() crypto.Keeper) KeeperOption {
	return KeeperCustom("material unwraps under a separate instance", func(t *testing.T, k crypto.Keeper) {
		peer := other()
		dek := bytes.Repeat([]byte{0x5A}, 32)

		wrapped, err := k.Wrap(t.Context(), dek)
		testkit.NoError(t, err, "Wrap must succeed")

		got, err := peer.Unwrap(t.Context(), wrapped)
		testkit.NoError(t, err, "a separate instance over the same key must unwrap")
		testkit.True(t, bytes.Equal(got, dek), "the separate instance must return the data key")

		testkit.Equal(t, peer.KeyID(), k.KeyID(), "KeyID must not vary between instances")
	})
}

// KeeperForeignKeyAssertion asserts material wrapped under a
// different wrapping key is an error rather than a wrong answer.
// other must be a Keeper over different key material.
func KeeperForeignKeyAssertion(other func() crypto.Keeper) KeeperOption {
	return KeeperCustom("material from another key does not unwrap", func(t *testing.T, k crypto.Keeper) {
		wrapped, err := other().Wrap(t.Context(), bytes.Repeat([]byte{0x5A}, 32))
		testkit.NoError(t, err, "Wrap must succeed")

		_, err = k.Unwrap(t.Context(), wrapped)
		testkit.Error(t, err, "material wrapped under a different key must not unwrap")
	})
}

// AssertDestroyerContract asserts the [crypto.Destroyer] capability:
// after Destroy, previously wrapped material no longer unwraps.
//
// That is the primitive underneath erasure of encrypted-at-rest data
// — the data stays where it is and becomes unreadable.
func AssertDestroyerContract(t *testing.T, d crypto.Destroyer) {
	t.Helper()

	wrapped, err := d.Wrap(t.Context(), bytes.Repeat([]byte{0x5A}, 32))
	testkit.NoError(t, err, "Wrap must succeed")

	testkit.NoError(t, d.Destroy(t.Context(), d.KeyID()), "Destroy must succeed")

	_, err = d.Unwrap(t.Context(), wrapped)
	testkit.Error(t, err, "material must not unwrap after its wrapping key is destroyed")

	_, err = d.Wrap(t.Context(), bytes.Repeat([]byte{0x5A}, 32))
	testkit.Error(t, err, "a destroyed Keeper must not wrap new material")
}

// AssertKeyGeneratorContract asserts the [crypto.KeyGenerator] capability:
// a data key minted inside the custodian unwraps to the plaintext
// returned alongside it, and successive calls differ.
func AssertKeyGeneratorContract(t *testing.T, g crypto.KeyGenerator) {
	t.Helper()

	plaintext, wrapped, err := g.GenerateKey(t.Context(), 32)
	testkit.NoError(t, err, "GenerateKey must succeed")
	testkit.Equal(t, len(plaintext), 32, "GenerateKey must return the requested size")

	got, err := g.Unwrap(t.Context(), wrapped)
	testkit.NoError(t, err, "the wrapped form must unwrap")
	testkit.True(t, bytes.Equal(got, plaintext),
		"the wrapped form must correspond to the returned plaintext")

	other, _, err := g.GenerateKey(t.Context(), 32)
	testkit.NoError(t, err, "GenerateKey must succeed")
	testkit.NotEqual(t, other, plaintext, "successive data keys must differ")
}
