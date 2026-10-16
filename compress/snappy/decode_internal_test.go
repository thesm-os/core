// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package snappy

import (
	"encoding/binary"
	"testing"

	"go.dokimi.dev/assert"
)

// TestDecodeLimits is in package snappy, because it calls decodedLen with
// the largest int of a 32-bit platform as the limit. On a 64-bit platform,
// every length of a valid preamble is below the limit. The test checks the
// refusal of a length above the largest int of 32 bits.
func TestDecodeLimits(t *testing.T) {
	t.Parallel()

	t.Run("decodedLen", func(t *testing.T) {
		t.Parallel()

		t.Run("returns ErrTooLarge for a length above the largest int", func(t *testing.T) {
			t.Parallel()

			_, _, err := decodedLen(binary.AppendUvarint(nil, 1<<31), 1<<31-1)
			assert.ErrorIs(t, err, ErrTooLarge, "decodedLen refuses a length above the largest int of 32 bits")
		})

		t.Run("returns a length equal to the largest int", func(t *testing.T) {
			t.Parallel()

			n, _, err := decodedLen(binary.AppendUvarint(nil, 1<<31-1), 1<<31-1)
			assert.NoError(t, err, "decodedLen accepts the largest int of 32 bits")
			assert.Equal(t, n, 1<<31-1, "decodedLen returns the length")
		})
	})
}
