// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package note_test

import (
	"crypto/sha256"
	"encoding/binary"
	"strings"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/crypto/sign"
	"go.thesmos.sh/core/note"
)

// The fixture values of the cases of KeyID.
const (
	// exampleKeyID is the key ID of exampleKey.
	exampleKeyID = 0x530d903a

	// nameHashBytes is the number of bytes of SHA-256 of the name that a
	// KeyID ends with.
	nameHashBytes = 12
)

// longName is a key name of 200 bytes, longer than the buffer through which
// KeyID copies a name to the hash.
var longName = note.Name(strings.Repeat("a", 200))

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
				name:     "returns the KeyID of the key of signed-note's example",
				giveName: exampleName,
				giveID:   exampleKeyID,
				want:     "530d903a76f699faa11e8fbe9a70d933",
			},
			{
				name:     "returns the KeyID of the key of golang.org/x/mod's tests",
				giveName: "PeterNeumann",
				giveID:   0xc74f20a3,
				want:     "c74f20a3fc4098e916ce95ee96255f25",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, note.KeyID(tt.giveName, tt.giveID).String(), tt.want,
					"KeyID must return its recorded value")
			})
		}

		t.Run("starts with the key ID in big-endian order", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "KeyID must start with the key ID in big-endian order", func(c *prop.Case) {
				id := c.Draw(prop.Of[uint32](), "key ID")
				kid := note.KeyID(c.Draw(names, "name"), id)
				assert.Equal(c, binary.BigEndian.Uint32(kid[:keyIDBytes]), id, "KeyID must start with the key ID")
			})
		})

		t.Run("ends with 12 bytes of SHA-256 of the name", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "KeyID must end with 12 bytes of SHA-256 of the name", func(c *prop.Case) {
				name := c.Draw(prop.OneOf(names, prop.Just(longName)), "name")
				kid := note.KeyID(name, c.Draw(prop.Of[uint32](), "key ID"))
				sum := sha256.Sum256([]byte(name))
				assert.Equal(c, kid[keyIDBytes:], sum[:nameHashBytes], "KeyID must end with the hash of the name")
			})
		})
	})
}

// TestKeyIDAllocs checks the allocation contract of KeyID. MaxAllocs counts
// the allocations of the whole process, so the test does not run in
// parallel.
func TestKeyIDAllocs(t *testing.T) {
	t.Run("KeyID", func(t *testing.T) {
		t.Run("of a name shorter than the buffer of the hash", func(t *testing.T) {
			var got sign.KeyID
			expect.MaxAllocs(t, func() { got = note.KeyID(exampleName, exampleKeyID) }, 0, "KeyID must not allocate")
			assert.NotEqual(t, got, sign.KeyID{}, "the test must measure a KeyID")
		})

		t.Run("of a name longer than the buffer of the hash", func(t *testing.T) {
			var got sign.KeyID
			expect.MaxAllocs(t, func() { got = note.KeyID(longName, exampleKeyID) }, 0, "KeyID must not allocate")
			assert.NotEqual(t, got, sign.KeyID{}, "the test must measure a KeyID")
		})
	})
}

// BenchmarkKeyID reports the cost of KeyID, and fails above the allocations
// that its contract states.
func BenchmarkKeyID(b *testing.B) {
	b.Run("KeyID", func(b *testing.B) {
		b.Run("of a name shorter than the buffer of the hash", func(b *testing.B) {
			var got sign.KeyID

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				got = note.KeyID(exampleName, exampleKeyID)
			}

			assert.NotEqual(b, got, sign.KeyID{}, "the benchmark must measure a KeyID")
		})

		b.Run("of a name longer than the buffer of the hash", func(b *testing.B) {
			var got sign.KeyID

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				got = note.KeyID(longName, exampleKeyID)
			}

			assert.NotEqual(b, got, sign.KeyID{}, "the benchmark must measure a KeyID")
		})
	})
}
