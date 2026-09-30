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

	_, err = d.Destroy(t.Context(), unknownKeyID)
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

// The associated data that [AssertAADKeeperContract] binds, and the
// associated data it opens with to show the binding.
var (
	aadBound = []byte("cryptotest/tenant-a")
	aadOther = []byte("cryptotest/tenant-b")
)

// AssertAADKeeperContract asserts the [crypto.AADKeeper] capability:
//
//   - A data key wrapped with an aad unwraps with the same aad.
//   - It fails to unwrap through UnwrapAAD with other or empty aad, and
//     through Unwrap.
//   - Material from Wrap unwraps through UnwrapAAD with empty aad.
//   - Wrapping one data key twice with one aad produces different bytes.
//
// A custodian that ignores the associated data fails the second
// assertion, so a policy that conditions on the data would not bind
// anything.
func AssertAADKeeperContract(t *testing.T, k crypto.AADKeeper) {
	t.Helper()

	dek := bytes.Repeat([]byte{0x5A}, 32)

	wrapped, err := k.WrapAAD(t.Context(), dek, aadBound)
	testkit.NoError(t, err, "WrapAAD must succeed")

	got, err := k.UnwrapAAD(t.Context(), wrapped, aadBound)
	testkit.NoError(t, err, "UnwrapAAD must succeed with the aad the key was wrapped with")
	testkit.True(t, bytes.Equal(got, dek), "UnwrapAAD must return the data key exactly")

	_, err = k.UnwrapAAD(t.Context(), wrapped, aadOther)
	testkit.Error(t, err, "material wrapped with one aad must not unwrap with another")
	_, err = k.UnwrapAAD(t.Context(), wrapped, nil)
	testkit.Error(t, err, "material wrapped with an aad must not unwrap with empty aad")
	_, err = k.Unwrap(t.Context(), wrapped)
	testkit.Error(t, err, "material wrapped with an aad must not unwrap through Unwrap")

	plain, err := k.Wrap(t.Context(), dek)
	testkit.NoError(t, err, "Wrap must succeed")
	got, err = k.UnwrapAAD(t.Context(), plain, nil)
	testkit.NoError(t, err, "material from Wrap must unwrap through UnwrapAAD with empty aad")
	testkit.True(t, bytes.Equal(got, dek), "UnwrapAAD must return the data key exactly")

	again, err := k.WrapAAD(t.Context(), dek, aadBound)
	testkit.NoError(t, err, "WrapAAD must succeed")
	testkit.NotEqual(t, again, wrapped, "wrapping one data key twice with one aad must not produce identical bytes")
}

// unknownKeyID is a key ID that no custodian under test has.
const unknownKeyID = "cryptotest/a-key-the-custodian-does-not-have"

// AssertKeyCreatorContract asserts the [crypto.KeyCreator] contract:
//
//   - A created Keeper passes every assertion of
//     [KeeperContractAssertions].
//   - Created key IDs differ from each other and from the creator's.
//   - Material wrapped under one created key does not unwrap under
//     another.
//   - OpenKey of a created key ID returns a Keeper with that key ID,
//     which unwraps the created key's material.
//   - OpenKey of an unknown key ID returns [crypto.ErrKeyID].
//   - Created and opened Keepers implement every optional capability of
//     the creator.
//   - If the creator is a [crypto.Destroyer], it destroys a created key by
//     key ID. Wrap and Unwrap through every opened Keeper then return
//     [crypto.ErrKeyDestroyed].
func AssertKeyCreatorContract(t *testing.T, c crypto.KeyCreator) {
	t.Helper()

	first, err := c.CreateKey(t.Context())
	testkit.NoError(t, err, "CreateKey must succeed")
	second, err := c.CreateKey(t.Context())
	testkit.NoError(t, err, "CreateKey must succeed")

	for _, opt := range KeeperContractAssertions() {
		t.Run(opt.name, func(t *testing.T) { opt.fn(t, first) })
	}

	testkit.NotEqual(t, first.KeyID(), c.KeyID(), "a created key must not reuse the creator's name")
	testkit.NotEqual(t, second.KeyID(), first.KeyID(), "two created keys must not share a name")

	dek := bytes.Repeat([]byte{0x5A}, 32)
	wrapped, err := first.Wrap(t.Context(), dek)
	testkit.NoError(t, err, "Wrap must succeed")

	_, err = second.Unwrap(t.Context(), wrapped)
	testkit.Error(t, err, "material wrapped under one created key must not unwrap under another")

	opened, err := c.OpenKey(t.Context(), first.KeyID())
	testkit.NoError(t, err, "OpenKey must open a created key")
	testkit.Equal(t, opened.KeyID(), first.KeyID(), "the opened Keeper must name the created key")

	got, err := opened.Unwrap(t.Context(), wrapped)
	testkit.NoError(t, err, "the opened Keeper must unwrap what the created key wrapped")
	testkit.True(t, bytes.Equal(got, dek), "the opened Keeper must return the data key exactly")

	_, err = c.OpenKey(t.Context(), unknownKeyID)
	testkit.ErrorIs(t, err, crypto.ErrKeyID, "OpenKey of an unknown key must return ErrKeyID")

	assertCapabilitiesOf(t, c, first)
	assertCapabilitiesOf(t, c, opened)

	d, ok := crypto.AsDestroyer(c)
	if !ok {
		return
	}

	_, err = d.Destroy(t.Context(), first.KeyID())
	testkit.NoError(t, err, "the creator must destroy a created key by its name")

	reopened, err := c.OpenKey(t.Context(), first.KeyID())
	testkit.NoError(t, err, "OpenKey must open a destroyed key")

	for _, k := range []crypto.Keeper{opened, reopened} {
		_, err = k.Unwrap(t.Context(), wrapped)
		testkit.ErrorIs(t, err, crypto.ErrKeyDestroyed, "a destroyed key must not unwrap")
		_, err = k.Wrap(t.Context(), dek)
		testkit.ErrorIs(t, err, crypto.ErrKeyDestroyed, "a destroyed key must not wrap")
	}
}

// assertCapabilitiesOf asserts that k implements every optional
// capability of its creator c, including KeyCreator.
func assertCapabilitiesOf(t *testing.T, c crypto.KeyCreator, k crypto.Keeper) {
	t.Helper()

	if _, ok := crypto.AsDestroyer(c); ok {
		_, has := crypto.AsDestroyer(k)
		testkit.True(t, has, "a Keeper of a Destroyer's KeyCreator must be a Destroyer")
	}

	if _, ok := crypto.AsKeyGenerator(c); ok {
		_, has := crypto.AsKeyGenerator(k)
		testkit.True(t, has, "a Keeper of a KeyGenerator's KeyCreator must be a KeyGenerator")
	}

	if _, ok := crypto.AsAADKeeper(c); ok {
		_, has := crypto.AsAADKeeper(k)
		testkit.True(t, has, "a Keeper of an AADKeeper's KeyCreator must be an AADKeeper")
	}

	_, has := crypto.AsKeyCreator(k)
	testkit.True(t, has, "a Keeper that a KeyCreator returns must be a KeyCreator")
}
