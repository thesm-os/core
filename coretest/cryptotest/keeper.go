// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package cryptotest

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/conform"

	"go.thesmos.sh/core/crypto"
	randcrypto "go.thesmos.sh/core/rand/crypto"
)

// keeperLargeKey is the size of the largest data key of
// KeeperRoundTripsADataKey.
const keeperLargeKey = 64

// keeperSoundKeyID is the key ID of the custodian of the standard library
// that the planted defects of the suite of Keeper change.
const keeperSoundKeyID = "cryptotest/keeper"

// unknownKeyID is a key ID that no custodian under test has.
const unknownKeyID = "cryptotest/a-key-the-custodian-does-not-have"

// keeperDataKey is the data key that the checks of a Keeper wrap.
var keeperDataKey = bytes.Repeat([]byte{0x5A}, 32)

// The associated data that [AssertAADKeeperContract] binds, and the associated
// data that it opens with to show the binding.
var (
	aadBound = []byte("cryptotest/tenant-a")
	aadOther = []byte("cryptotest/tenant-b")
)

// KeeperRoundTripsADataKey checks that Unwrap returns the data key of 16, 32
// and 64 bytes that Wrap wrapped.
//
//dokimi:check Keeper.Unwrap defect=keeperChangesALargeKey
func KeeperRoundTripsADataKey(h *KeeperHandle) {
	k := h.Subject()
	for i, size := range []int{16, 32, keeperLargeKey} {
		assert.RoundTrip(h, func(dek []byte) ([]byte, error) { return k.Wrap(h.Context(), dek) },
			func(wrapped []byte) ([]byte, error) { return k.Unwrap(h.Context(), wrapped) },
			bytes.Repeat([]byte{byte('a' + i)}, size), h.Statement())
	}
}

// KeeperWrapsNonDeterministically checks that two Wraps of one data key return
// different bytes.
//
//dokimi:check Keeper.Wrap defect=keeperWrapsDeterministically
func KeeperWrapsNonDeterministically(h *KeeperHandle) {
	k := h.Subject()
	assert.NotEqual(h, wrapDataKey(h, k, keeperDataKey), wrapDataKey(h, k, keeperDataKey), h.Statement())
}

// KeeperRefusesCorruptedMaterial checks that Unwrap returns an error for the
// wrapped material with any one bit changed.
//
//dokimi:check Keeper.Unwrap defect=keeperRestoresTheLastByte
func KeeperRefusesCorruptedMaterial(h *KeeperHandle) {
	k := h.Subject()
	wrapped := wrapDataKey(h, k, keeperDataKey)
	for i := range wrapped {
		corrupt := bytes.Clone(wrapped)
		corrupt[i] ^= 0x01
		_, err := k.Unwrap(h.Context(), corrupt)
		assert.HasError(h, err, h.Statement())
	}
}

// KeeperRefusesTruncatedMaterial checks that Unwrap returns an error for a
// prefix of 0, 1, half and all but one of the bytes of the wrapped material.
//
//dokimi:check Keeper.Unwrap defect=keeperUnwrapsNoMaterial
func KeeperRefusesTruncatedMaterial(h *KeeperHandle) {
	k := h.Subject()
	wrapped := wrapDataKey(h, k, keeperDataKey)
	for _, n := range []int{0, 1, len(wrapped) / 2, len(wrapped) - 1} {
		_, err := k.Unwrap(h.Context(), wrapped[:n])
		assert.HasError(h, err, h.Statement())
	}
}

// KeeperNamesItsKey checks that KeyID returns a name that is not empty, and
// the same name on every call.
//
//dokimi:check Keeper.KeyID defect=keeperHasNoKeyID
func KeeperNamesItsKey(h *KeeperHandle) {
	k := h.Subject()
	assert.NotEqual(h, k.KeyID(), "", h.Statement())
	assert.Deterministic(h, func(k crypto.Keeper) (string, error) { return k.KeyID(), nil }, k, h.Statement())
}

// KeeperRoundTripsAnEmptyKey checks that Unwrap returns an empty data key for
// the Wrap of an empty data key.
//
//dokimi:check Keeper.Wrap defect=keeperRefusesAnEmptyKey
func KeeperRoundTripsAnEmptyKey(h *KeeperHandle) {
	k := h.Subject()
	wrapped, err := k.Wrap(h.Context(), nil)
	assert.NoError(h, err, h.Statement())
	got, err := k.Unwrap(h.Context(), wrapped)
	assert.NoError(h, err, h.Statement())
	assert.Empty(h, got, h.Statement())
}

// KeeperUnwrapsUnderASecondInstance checks that a second instance over the same
// wrapping key unwraps the material of the first, and has its key ID.
//
//dokimi:check Keeper.Unwrap defect=keeperOfAFreshKey second=keeperOfAFreshKey
func KeeperUnwrapsUnderASecondInstance(h *KeeperHandle) {
	k, second := h.Subject(), h.Second()
	assert.RoundTrip(h, func(dek []byte) ([]byte, error) { return k.Wrap(h.Context(), dek) },
		func(wrapped []byte) ([]byte, error) { return second.Unwrap(h.Context(), wrapped) }, keeperDataKey,
		h.Statement())
	assert.Equal(h, second.KeyID(), k.KeyID(), h.Statement())
}

// AssertDestroyerContract asserts the [crypto.Destroyer] capability:
//
//   - After Destroy returns, previously wrapped material fails to unwrap with
//     [crypto.ErrKeyDestroyed], and new material does not wrap.
//   - A second Destroy returns the time of the first call. A custodian whose
//     first call returned the zero time may return the time on the second.
//   - Destroy of a key that the custodian does not have returns
//     [crypto.ErrKeyID].
//
// The calls take the context of tb.
func AssertDestroyerContract(tb assert.TB, d crypto.Destroyer) {
	tb.Helper()

	ctx := assert.Context(tb)
	wrapped, err := d.Wrap(ctx, keeperDataKey)
	assert.NoError(tb, err, "Wrap wraps the data key")

	first, err := d.Destroy(ctx, d.KeyID())
	assert.NoError(tb, err, "Destroy destroys the wrapping key")

	_, err = d.Unwrap(ctx, wrapped)
	assert.ErrorIs(tb, err, crypto.ErrKeyDestroyed, "material does not unwrap after Destroy of its wrapping key")

	_, err = d.Wrap(ctx, keeperDataKey)
	assert.HasError(tb, err, "a destroyed Keeper does not wrap new material")

	again, err := d.Destroy(ctx, d.KeyID())
	assert.NoError(tb, err, "a second Destroy succeeds")
	if !first.IsZero() {
		assert.Equal(tb, again, first, "a second Destroy returns the time of the first call")
	}

	_, err = d.Destroy(ctx, unknownKeyID)
	assert.ErrorIs(tb, err, crypto.ErrKeyID, "Destroy of an unknown key returns ErrKeyID")
}

// AssertKeyGeneratorContract asserts the [crypto.KeyGenerator] capability: two
// calls of GenerateKey return different data keys, and a data key that the
// custodian generates unwraps to the plaintext that GenerateKey returns with
// it. The calls take the context of tb.
func AssertKeyGeneratorContract(tb assert.TB, g crypto.KeyGenerator) {
	tb.Helper()

	ctx := assert.Context(tb)
	plaintext, wrapped, err := g.GenerateKey(ctx, 32)
	assert.NoError(tb, err, "GenerateKey generates a data key")
	other, _, err := g.GenerateKey(ctx, 32)
	assert.NoError(tb, err, "GenerateKey generates a second data key")
	assert.NotEqual(tb, other, plaintext, "two calls of GenerateKey return different data keys")
	assert.Length(tb, plaintext, 32, "GenerateKey returns a data key of the requested size")

	got, err := g.Unwrap(ctx, wrapped)
	assert.NoError(tb, err, "the wrapped form unwraps")
	assert.Equal(tb, got, plaintext, "the wrapped form unwraps to the plaintext of GenerateKey")
}

// AssertAADKeeperContract asserts the [crypto.AADKeeper] capability:
//
//   - Two calls of WrapAAD with one data key and one aad return different
//     bytes.
//   - A data key that WrapAAD wrapped with an aad unwraps with the same aad.
//   - It fails to unwrap through UnwrapAAD with other aad or empty aad, and
//     through Unwrap.
//   - Material of Wrap unwraps through UnwrapAAD with empty aad.
//
// The calls take the context of tb.
func AssertAADKeeperContract(tb assert.TB, k crypto.AADKeeper) {
	tb.Helper()

	ctx := assert.Context(tb)
	wrapped, err := k.WrapAAD(ctx, keeperDataKey, aadBound)
	assert.NoError(tb, err, "WrapAAD wraps the data key")
	again, err := k.WrapAAD(ctx, keeperDataKey, aadBound)
	assert.NoError(tb, err, "WrapAAD wraps the data key a second time")
	assert.NotEqual(tb, again, wrapped, "two calls of WrapAAD with one data key and one aad return different bytes")

	assert.RoundTrip(tb, func(dek []byte) ([]byte, error) { return k.WrapAAD(ctx, dek, aadBound) },
		func(w []byte) ([]byte, error) { return k.UnwrapAAD(ctx, w, aadBound) }, keeperDataKey,
		"UnwrapAAD returns the data key that WrapAAD wrapped with the same aad")

	_, err = k.UnwrapAAD(ctx, wrapped, aadOther)
	assert.HasError(tb, err, "material that WrapAAD bound to one aad does not unwrap with another")
	_, err = k.UnwrapAAD(ctx, wrapped, nil)
	assert.HasError(tb, err, "material that WrapAAD bound to an aad does not unwrap with empty aad")
	_, err = k.Unwrap(ctx, wrapped)
	assert.HasError(tb, err, "material that WrapAAD bound to an aad does not unwrap through Unwrap")

	assert.RoundTrip(tb, func(dek []byte) ([]byte, error) { return k.Wrap(ctx, dek) },
		func(w []byte) ([]byte, error) { return k.UnwrapAAD(ctx, w, nil) }, keeperDataKey,
		"UnwrapAAD with empty aad returns the data key that Wrap wrapped")
}

// AssertKeyCreatorContract asserts the [crypto.KeyCreator] contract:
//
//   - A created Keeper passes every check of the suite of Keeper. A Keeper
//     that OpenKey returns for its key ID is its second instance.
//   - Created key IDs differ from each other and from the key ID of the
//     creator.
//   - Material that one created key wrapped does not unwrap under another.
//   - OpenKey of a created key ID returns a Keeper with that key ID, which
//     unwraps the material of the created key.
//   - OpenKey of an unknown key ID returns [crypto.ErrKeyID].
//   - Created and opened Keepers implement every optional capability of the
//     creator.
//   - When the creator is a [crypto.Destroyer], it destroys a created key by
//     key ID. Wrap and Unwrap through every opened Keeper then return
//     [crypto.ErrKeyDestroyed].
func AssertKeyCreatorContract(t *testing.T, c crypto.KeyCreator) {
	t.Helper()

	first, err := c.CreateKey(t.Context())
	assert.NoError(t, err, "CreateKey creates a key")
	second, err := c.CreateKey(t.Context())
	assert.NoError(t, err, "CreateKey creates a second key")

	// The checks of the suite run in parallel subtests, which end before the
	// subtest that runs them returns, so they end before the creator destroys
	// the key.
	t.Run("Keeper", func(t *testing.T) {
		RunKeeperSuite(t, func() crypto.Keeper { return first }, conform.Second(func(h *KeeperHandle) crypto.Keeper {
			opened, openErr := c.OpenKey(h.Context(), first.KeyID())
			assert.NoError(h, openErr, "OpenKey opens the created key")

			return opened
		}))
	})

	assert.NotEqual(t, first.KeyID(), c.KeyID(), "a created key does not take the key ID of the creator")
	assert.NotEqual(t, second.KeyID(), first.KeyID(), "two created keys do not share a key ID")

	wrapped, err := first.Wrap(t.Context(), keeperDataKey)
	assert.NoError(t, err, "the created key wraps the data key")

	_, err = second.Unwrap(t.Context(), wrapped)
	assert.HasError(t, err, "material that one created key wrapped does not unwrap under another")

	opened, err := c.OpenKey(t.Context(), first.KeyID())
	assert.NoError(t, err, "OpenKey opens a created key")
	assert.Equal(t, opened.KeyID(), first.KeyID(), "the opened Keeper has the key ID of the created key")

	got, err := opened.Unwrap(t.Context(), wrapped)
	assert.NoError(t, err, "the opened Keeper unwraps the material of the created key")
	assert.Equal(t, got, keeperDataKey, "the opened Keeper returns the data key")

	_, err = c.OpenKey(t.Context(), unknownKeyID)
	assert.ErrorIs(t, err, crypto.ErrKeyID, "OpenKey of an unknown key returns ErrKeyID")

	assertCapabilitiesOf(t, c, first)
	assertCapabilitiesOf(t, c, opened)

	d, ok := crypto.AsDestroyer(c)
	if !ok {
		return
	}

	_, err = d.Destroy(t.Context(), first.KeyID())
	assert.NoError(t, err, "the creator destroys a created key by its key ID")

	reopened, err := c.OpenKey(t.Context(), first.KeyID())
	assert.NoError(t, err, "OpenKey opens a destroyed key")

	for _, k := range []crypto.Keeper{opened, reopened} {
		_, err = k.Unwrap(t.Context(), wrapped)
		assert.ErrorIs(t, err, crypto.ErrKeyDestroyed, "a destroyed key does not unwrap")
		_, err = k.Wrap(t.Context(), keeperDataKey)
		assert.ErrorIs(t, err, crypto.ErrKeyDestroyed, "a destroyed key does not wrap")
	}
}

// assertCapabilitiesOf asserts that k implements every optional capability of
// its creator c, KeyCreator included.
func assertCapabilitiesOf(t *testing.T, c crypto.KeyCreator, k crypto.Keeper) {
	t.Helper()

	if _, ok := crypto.AsDestroyer(c); ok {
		_, has := crypto.AsDestroyer(k)
		assert.True(t, has, "a Keeper of the KeyCreator of a Destroyer is a Destroyer")
	}

	if _, ok := crypto.AsKeyGenerator(c); ok {
		_, has := crypto.AsKeyGenerator(k)
		assert.True(t, has, "a Keeper of the KeyCreator of a KeyGenerator is a KeyGenerator")
	}

	if _, ok := crypto.AsAADKeeper(c); ok {
		_, has := crypto.AsAADKeeper(k)
		assert.True(t, has, "a Keeper of the KeyCreator of an AADKeeper is an AADKeeper")
	}

	_, has := crypto.AsKeyCreator(k)
	assert.True(t, has, "a Keeper that a KeyCreator returns is a KeyCreator")
}

// wrapDataKey wraps dek under k, and stops the check when Wrap fails.
func wrapDataKey(h *KeeperHandle, k crypto.Keeper, dek []byte) []byte {
	h.Helper()

	wrapped, err := k.Wrap(h.Context(), dek)
	assert.NoError(h, err, "Wrap wraps the data key")

	return wrapped
}

// stdlibKeeper is a custodian of AES-256-GCM of the standard library, which
// draws the nonce of each Wrap. Its wrapped material is the nonce, the
// ciphertext of the data key and the tag. The planted defects of the suite of
// Keeper change one behaviour of it.
type stdlibKeeper struct {
	aead cipher.AEAD
}

// KeyID returns keeperSoundKeyID.
func (stdlibKeeper) KeyID() string { return keeperSoundKeyID }

// Wrap seals dek under a nonce that the AEAD draws.
func (k stdlibKeeper) Wrap(_ context.Context, dek []byte) ([]byte, error) {
	//nolint:gosec // G407: the AEAD of GCM with random nonces draws the nonce of each Seal
	return k.aead.Seal(nil, nil, dek, nil), nil
}

// Unwrap opens the material of Wrap.
func (k stdlibKeeper) Unwrap(_ context.Context, wrapped []byte) ([]byte, error) {
	return k.aead.Open(nil, nil, wrapped, nil)
}

// newStdlibKeeper returns the custodian of key, and stops the check when the
// standard library refuses the key.
func newStdlibKeeper(h assert.TB, key []byte) stdlibKeeper {
	h.Helper()

	block, err := aes.NewCipher(key)
	assert.NoError(h, err, "AES accepts a key of 32 bytes")
	gcm, err := cipher.NewGCMWithRandomNonce(block)
	assert.NoError(h, err, "GCM with random nonces accepts the block cipher of AES")

	return stdlibKeeper{aead: gcm}
}

// unwrapOverSound is the Unwrap of a planted defect, over the sound custodian.
type unwrapOverSound func(ctx context.Context, sound stdlibKeeper, wrapped []byte) ([]byte, error)

// keeperUnwrapDefect returns a Keeper over the sound key whose Unwrap is
// unwrap.
func keeperUnwrapDefect(h *KeeperHandle, unwrap unwrapOverSound) crypto.Keeper {
	sound := newStdlibKeeper(h, aeadSoundKey)

	return NewKeeperStub(h, KeeperStubDelegateTo(sound),
		WithKeeperUnwrap(func(ctx context.Context, wrapped []byte) ([]byte, error) {
			return unwrap(ctx, sound, wrapped)
		}))
}

// keeperChangesALargeKey returns the planted defect of KeeperRoundTripsADataKey:
// a Keeper whose Unwrap changes the first byte of a data key of 64 bytes.
func keeperChangesALargeKey(h *KeeperHandle) crypto.Keeper {
	return keeperUnwrapDefect(h, func(ctx context.Context, sound stdlibKeeper, wrapped []byte) ([]byte, error) {
		dek, err := sound.Unwrap(ctx, wrapped)
		if err == nil && len(dek) == keeperLargeKey {
			dek[0] ^= 0x01
		}

		return dek, err
	})
}

// keeperWrapsDeterministically returns the planted defect of
// KeeperWrapsNonDeterministically: a Keeper whose Wrap returns the material of
// its first Wrap of a data key for every later Wrap of that data key.
func keeperWrapsDeterministically(h *KeeperHandle) crypto.Keeper {
	sound := newStdlibKeeper(h, aeadSoundKey)
	wrapped := make(map[string][]byte)

	return NewKeeperStub(h, KeeperStubDelegateTo(sound),
		WithKeeperWrap(func(ctx context.Context, dek []byte) ([]byte, error) {
			if w, ok := wrapped[string(dek)]; ok {
				return bytes.Clone(w), nil
			}
			w, err := sound.Wrap(ctx, dek)
			if err != nil {
				return nil, err
			}
			wrapped[string(dek)] = w

			return bytes.Clone(w), nil
		}))
}

// keeperRestoresTheLastByte returns the planted defect of
// KeeperRefusesCorruptedMaterial: a Keeper whose Unwrap unwraps material again
// with its last byte changed, when the material fails to unwrap.
func keeperRestoresTheLastByte(h *KeeperHandle) crypto.Keeper {
	return keeperUnwrapDefect(h, func(ctx context.Context, sound stdlibKeeper, wrapped []byte) ([]byte, error) {
		dek, err := sound.Unwrap(ctx, wrapped)
		if err == nil || len(wrapped) == 0 {
			return dek, err
		}
		restored := bytes.Clone(wrapped)
		restored[len(restored)-1] ^= 0x01

		return sound.Unwrap(ctx, restored)
	})
}

// keeperUnwrapsNoMaterial returns the planted defect of
// KeeperRefusesTruncatedMaterial: a Keeper whose Unwrap of no bytes returns an
// empty data key.
func keeperUnwrapsNoMaterial(h *KeeperHandle) crypto.Keeper {
	return keeperUnwrapDefect(h, func(ctx context.Context, sound stdlibKeeper, wrapped []byte) ([]byte, error) {
		if len(wrapped) == 0 {
			return []byte{}, nil
		}

		return sound.Unwrap(ctx, wrapped)
	})
}

// keeperHasNoKeyID returns the planted defect of KeeperNamesItsKey: a Keeper
// whose KeyID returns the empty name.
func keeperHasNoKeyID(h *KeeperHandle) crypto.Keeper {
	return NewKeeperStub(h, KeeperStubDelegateTo(newStdlibKeeper(h, aeadSoundKey)),
		WithKeeperKeyID(func() string { return "" }))
}

// keeperRefusesAnEmptyKey returns the planted defect of
// KeeperRoundTripsAnEmptyKey: a Keeper whose Wrap of an empty data key returns
// crypto.ErrKeySize.
func keeperRefusesAnEmptyKey(h *KeeperHandle) crypto.Keeper {
	sound := newStdlibKeeper(h, aeadSoundKey)

	return NewKeeperStub(h, KeeperStubDelegateTo(sound),
		WithKeeperWrap(func(ctx context.Context, dek []byte) ([]byte, error) {
			if len(dek) == 0 {
				return nil, crypto.ErrKeySize
			}

			return sound.Wrap(ctx, dek)
		}))
}

// keeperOfAFreshKey returns the planted defect of
// KeeperUnwrapsUnderASecondInstance, and its second instance: a Keeper whose
// every instance draws a wrapping key of its own.
func keeperOfAFreshKey(h *KeeperHandle) crypto.Keeper {
	key := make([]byte, aeadKeySize)
	_, err := randcrypto.New().Read(key)
	assert.NoError(h, err, "the CSPRNG of the operating system fills the key")

	return newStdlibKeeper(h, key)
}
