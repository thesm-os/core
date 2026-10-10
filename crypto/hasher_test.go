// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package crypto_test

import (
	"encoding/hex"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/crypto"
)

// streamResetContract is the contract of streamResets, which the test
// and the fuzz target of Reset share.
const streamResetContract = "a Stream after Reset must return the digest that a new Stream returns"

func TestHasher(t *testing.T) {
	t.Parallel()

	t.Run("ID", func(t *testing.T) {
		t.Parallel()

		t.Run("has IDSize bytes", func(t *testing.T) {
			t.Parallel()
			assert.Length(t, crypto.ID{}, 16, "an ID must have 16 bytes")
			assert.Equal(t, crypto.IDSize, 16, "IDSize must be 16")
		})

		t.Run("String", func(t *testing.T) {
			t.Parallel()

			t.Run("returns the bytes in lowercase hexadecimal", func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, crypto.ID{0xde, 0xad, 0xbe, 0xef}.String(), "deadbeef000000000000000000000000",
					"String must encode the bytes in order")
			})

			t.Run("returns a string that decodes to the ID", func(t *testing.T) {
				t.Parallel()
				prop.RoundTrip(t, func(id crypto.ID) (string, error) {
					return id.String(), nil
				}, func(s string) (crypto.ID, error) {
					var id crypto.ID
					_, err := hex.Decode(id[:], []byte(s))

					return id, err
				}, "String must encode every byte of the ID")
			})
		})
	})

	t.Run("Stream", func(t *testing.T) {
		t.Parallel()

		t.Run("Reset", func(t *testing.T) {
			t.Parallel()

			t.Run("restores the state of a new Stream", func(t *testing.T) {
				t.Parallel()
				prop.ForAll(t, streamResetContract, streamResets)
			})
		})
	})
}

// FuzzStreamReset checks Reset of a Stream on the inputs that a fuzzer
// finds.
func FuzzStreamReset(f *testing.F) {
	prop.Fuzz(f, streamResetContract, streamResets)
}

// streamResets checks that a Stream that hashed drawn bytes, after Reset,
// returns the digest of the next drawn bytes alone.
func streamResets(c *prop.Case) {
	before := c.Draw(prop.Bytes(prop.MaxSize(4096)), "before")
	data := c.Draw(prop.Bytes(prop.MaxSize(4096)), "data")
	s := hasher.NewStream()
	_, _ = s.Write(before)
	_ = s.Sum()
	s.Reset()
	_, _ = s.Write(data)
	assert.Equal(c, s.Sum(), hasher.Hash(data), "Reset must restore the state of a new Stream")
}
