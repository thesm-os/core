// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package snappy

import (
	"testing"

	"go.dokimi.dev/assert"
)

// TestEncodeLimits is in package snappy, because it calls maxEncodedLen with
// the largest int of a 32-bit platform as the limit, and emitCopy at the
// limits of the forms of a copy. On a 64-bit platform, the bound of every
// length up to MaxBlockLen is below the limit.
func TestEncodeLimits(t *testing.T) {
	t.Parallel()

	t.Run("maxEncodedLen", func(t *testing.T) {
		t.Parallel()

		t.Run("returns ErrTooLarge for a bound above the largest int", func(t *testing.T) {
			t.Parallel()

			_, err := maxEncodedLen(1<<31-1, 1<<31-1)
			assert.ErrorIs(t, err, ErrTooLarge, "maxEncodedLen refuses a bound above the largest int of 32 bits")
		})

		t.Run("returns a bound equal to the limit", func(t *testing.T) {
			t.Parallel()

			got, err := maxEncodedLen(600, 32+600+100)
			assert.NoError(t, err, "maxEncodedLen accepts a bound equal to the limit")
			assert.Equal(t, got, 732, "maxEncodedLen returns 32 + n + n/6")
		})
	})

	t.Run("emitCopy", func(t *testing.T) {
		t.Parallel()

		// The bytes follow the format of Snappy. A copy with a 1-byte offset
		// has the tag offset>>8<<5 | (length-4)<<2 | 1 and the low byte of the
		// offset. A copy with a 2-byte offset has the tag (length-1)<<2 | 2 and
		// the offset in 2 bytes, little-endian.
		tests := []struct {
			name       string
			giveOffset int
			giveLength int
			want       []byte
		}{
			{
				name:       "writes a 1-byte offset for an offset of 2047",
				giveOffset: 2047, giveLength: 8, want: []byte{0xf1, 0xff},
			},
			{
				name:       "writes a 2-byte offset for an offset of 2048",
				giveOffset: 2048, giveLength: 8, want: []byte{0x1e, 0x00, 0x08},
			},
			{
				name:       "writes one copy of 64 bytes",
				giveOffset: 1, giveLength: 64, want: []byte{0xfe, 0x01, 0x00},
			},
			{
				name:       "writes a copy of 60 bytes and one of 5 for 65 bytes",
				giveOffset: 1, giveLength: 65, want: []byte{0xee, 0x01, 0x00, 0x05, 0x01},
			},
			{
				name:       "writes a copy of 64 bytes and one of 4 for 68 bytes",
				giveOffset: 1, giveLength: 68, want: []byte{0xfe, 0x01, 0x00, 0x01, 0x01},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				dst := make([]byte, 16)
				n := emitCopy(dst, tt.giveOffset, tt.giveLength)
				assert.Equal(t, dst[:n], tt.want, "emitCopy writes the elements of the format")
			})
		}
	})
}
