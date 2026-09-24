// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package cryptotest

import (
	"bytes"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/crypto"
)

// KeeperOption is one conformance subtest over a [crypto.Keeper]. The
// suite is hand-written because the testkit suite generator reads
// Unwrap as a keyed store lookup, and its []byte key type does not
// satisfy comparable.
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
// [crypto.Keeper] must satisfy. Envelope encryption is sound only when
// a Keeper satisfies all of them:
//
//   - A data key survives the round trip exactly.
//   - Wrapping is non-deterministic. Deterministic wrapping would let a
//     holder of two wrapped keys tell whether the underlying data keys
//     are equal without unwrapping either.
//   - A flipped bit in any position fails to unwrap. A custodian that
//     authenticates only part of its output would pass a check at one
//     position.
//   - Truncated material fails to unwrap.
//   - KeyID is non-empty and stable. It is persisted with every wrapped
//     key, so material wrapped before a rotation can be unwrapped after
//     one.
//   - An empty data key round-trips. The seam does not treat empty
//     input as absent input.
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
			testkit.NotEqual(t, k.KeyID(), "", "KeyID must name the wrapping key")
			testkit.Equal(t, k.KeyID(), k.KeyID(), "KeyID must be stable")
		}),

		KeeperCustom("an empty data key round-trips", func(t *testing.T, k crypto.Keeper) {
			wrapped, err := k.Wrap(t.Context(), nil)
			testkit.NoError(t, err, "Wrap must accept an empty data key")

			got, err := k.Unwrap(t.Context(), wrapped)
			testkit.NoError(t, err, "Unwrap must succeed")
			testkit.Equal(t, len(got), 0, "an empty data key must round-trip to empty")
		}),
	}
}

// KeeperCrossInstanceAssertion asserts material wrapped by one
// instance unwraps under another over the same wrapping key. It fails
// a custodian that caches per-instance state. In production the two
// instances are separate processes.
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
// different wrapping key returns an error and never a wrong key.
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
//
//   - After Destroy returns, previously wrapped material fails to
//     unwrap with [crypto.ErrKeyDestroyed], and new material does not
//     wrap.
//   - A second Destroy returns the first call's time. A custodian whose
//     first call returned the zero time, because it did not yet know
//     the time, may return the time on the second.
//   - Destroy of a key the custodian does not have returns
//     [crypto.ErrKeyID].
//
// That is the primitive underneath erasure of encrypted-at-rest data:
// the data remains in place and becomes unreadable.
func AssertDestroyerContract(t *testing.T, d crypto.Destroyer) {
	t.Helper()

	wrapped, err := d.Wrap(t.Context(), bytes.Repeat([]byte{0x5A}, 32))
	testkit.NoError(t, err, "Wrap must succeed")

	first, err := d.Destroy(t.Context(), d.KeyID())
	testkit.NoError(t, err, "Destroy must succeed")

	_, err = d.Unwrap(t.Context(), wrapped)
	testkit.ErrorIs(t, err, crypto.ErrKeyDestroyed,
		"material must not unwrap after its wrapping key is destroyed")

	_, err = d.Wrap(t.Context(), bytes.Repeat([]byte{0x5A}, 32))
	testkit.Error(t, err, "a destroyed Keeper must not wrap new material")

	again, err := d.Destroy(t.Context(), d.KeyID())
	testkit.NoError(t, err, "a second Destroy must succeed")
	if !first.IsZero() {
		testkit.Equal(t, again, first, "a second Destroy must return the first call's time")
	}

	_, err = d.Destroy(t.Context(), "cryptotest/a-key-the-custodian-does-not-have")
	testkit.ErrorIs(t, err, crypto.ErrKeyID, "Destroy of an unknown key must return ErrKeyID")
}

// AssertKeyGeneratorContract asserts the [crypto.KeyGenerator] capability:
// a data key the custodian generates unwraps to the plaintext returned
// alongside it, and successive calls differ.
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
