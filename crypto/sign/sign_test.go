// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package sign_test

import (
	"encoding/hex"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/crypto/sign"
)

func TestSign(t *testing.T) {
	t.Parallel()

	t.Run("KeyID", func(t *testing.T) {
		t.Parallel()

		t.Run("has KeyIDSize bytes", func(t *testing.T) {
			t.Parallel()
			assert.Length(t, sign.KeyID{}, 16, "a KeyID must have 16 bytes")
			assert.Equal(t, sign.KeyIDSize, 16, "KeyIDSize must be 16")
		})

		t.Run("String", func(t *testing.T) {
			t.Parallel()

			t.Run("returns the bytes in lowercase hexadecimal", func(t *testing.T) {
				t.Parallel()
				k := sign.KeyID{0xab, 0xcd, 14: 0x12, 15: 0x34}
				assert.Equal(t, k.String(), "abcd0000000000000000000000001234", "String must encode the bytes in order")
			})

			t.Run("returns a string that decodes to the KeyID", func(t *testing.T) {
				t.Parallel()
				prop.RoundTrip(t, func(k sign.KeyID) (string, error) {
					return k.String(), nil
				}, func(s string) (sign.KeyID, error) {
					var k sign.KeyID
					_, err := hex.Decode(k[:], []byte(s))

					return k, err
				}, "String must encode every byte of the KeyID")
			})
		})
	})
}
