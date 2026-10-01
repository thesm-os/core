// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package note_test

import (
	"encoding/binary"
	"strings"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/note"
)

func TestKeyID(t *testing.T) {
	t.Parallel()

	t.Run("KeyID", func(t *testing.T) {
		t.Parallel()

		// The KeyID of a note key is a persisted encoding. sha256sum of the
		// two names gives the 12 bytes after each key ID.
		tests := []struct {
			name     string
			giveName note.Name
			want     string
			giveID   uint32
		}{
			{
				name:     "returns the key ID and 12 bytes of SHA-256 of the name of signed-note's example",
				giveName: exampleName,
				giveID:   0x530d903a,
				want:     "530d903a76f699faa11e8fbe9a70d933",
			},
			{
				name:     "returns the key ID and 12 bytes of SHA-256 of the name of golang.org/x/mod's key",
				giveName: "PeterNeumann",
				giveID:   0xc74f20a3,
				want:     "c74f20a3fc4098e916ce95ee96255f25",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				testkit.Equal(t, note.KeyID(tt.giveName, tt.giveID).String(), tt.want,
					"KeyID must match its recorded value")
			})
		}

		t.Run("starts with the key ID in big-endian order", func(t *testing.T) {
			t.Parallel()
			r := testkit.SeededRand(t)
			for range 500 {
				id := r.Uint32()
				kid := note.KeyID(randomName(r), id)
				testkit.Equal(t, binary.BigEndian.Uint32(kid[:]), id, "the first four bytes must be the key ID")
			}
		})

		t.Run("differs for two names of one key ID", func(t *testing.T) {
			t.Parallel()
			testkit.NotEqual(t, note.KeyID("a", 1), note.KeyID("b", 1), "the name must change the KeyID")
		})

		t.Run("differs for two key IDs of one name", func(t *testing.T) {
			t.Parallel()
			testkit.NotEqual(t, note.KeyID("a", 1), note.KeyID("a", 2), "the key ID must change the KeyID")
		})
	})
}

func BenchmarkKeyID(b *testing.B) {
	b.Run("KeyID", func(b *testing.B) {
		benchZeroAlloc(b, func() { sinkKeyID = note.KeyID(exampleName, 0x530d903a) })
	})

	b.Run("KeyID of a name of 200 bytes", func(b *testing.B) {
		name := note.Name(strings.Repeat("a", 200))
		benchZeroAlloc(b, func() { sinkKeyID = note.KeyID(name, 0x530d903a) })
	})
}
