// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package idmap

import (
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/id"
)

// known is the seed of the known hashes. A computation with Python integers
// gives each known hash: the product of the two factors of each
// multiplication, and the exclusive or of the two 64-bit halves of each
// product.
var known = seed{s0: 0x0123456789abcdef, s1: 0xfedcba9876543210}

func TestKey(t *testing.T) {
	t.Parallel()

	t.Run("newHash", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the hash function of an ID128", func(t *testing.T) {
			t.Parallel()
			hash, _ := newHash[id.ID128]()
			got, _ := hash(known, counting[id.ID128]())
			want, _ := hash128(known, counting[id.ID128]())
			assert.Equal(t, got, want, "newHash must return hash128 for ID128 keys")
		})

		t.Run("returns the hash function of an ID160", func(t *testing.T) {
			t.Parallel()
			hash, _ := newHash[id.ID160]()
			got, _ := hash(known, counting[id.ID160]())
			want, _ := hash160(known, counting[id.ID160]())
			assert.Equal(t, got, want, "newHash must return hash160 for ID160 keys")
		})

		t.Run("returns the hash function of an ID256", func(t *testing.T) {
			t.Parallel()
			hash, _ := newHash[id.ID256]()
			got, _ := hash(known, counting[id.ID256]())
			want, _ := hash256(known, counting[id.ID256]())
			assert.Equal(t, got, want, "newHash must return hash256 for ID256 keys")
		})

		t.Run("returns a new random seed on each call", func(t *testing.T) {
			t.Parallel()
			_, a := newHash[id.ID128]()
			_, b := newHash[id.ID128]()
			expect.NotEqual(t, a.s0, b.s0, "two calls must return different first secret words")
			expect.NotEqual(t, a.s1, b.s1, "two calls must return different second secret words")
			expect.NotEqual(t, a.s0, a.s1, "the two secret words of one seed must differ")
		})
	})

	t.Run("hash128", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the known hash and the words of a key", func(t *testing.T) {
			t.Parallel()
			sum, w := hash128(known, counting[id.ID128]())
			expect.Equal(t, sum, uint64(0x0a800074ac2c2504), "hash128 must return the known hash of the key")
			expect.Equal(t, w, words{w0: 0x0706050403020100, w1: 0x0f0e0d0c0b0a0908},
				"hash128 must return the two words of the key")
		})
	})

	t.Run("hash160", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the known hash and the words of a key", func(t *testing.T) {
			t.Parallel()
			sum, w := hash160(known, counting[id.ID160]())
			expect.Equal(t, sum, uint64(0xfcf5baeb13dcb4e8), "hash160 must return the known hash of the key")
			expect.Equal(t, w, words{w0: 0x0706050403020100, w1: 0x0f0e0d0c0b0a0908, w2: 0x13121110},
				"hash160 must return the three words of the key")
		})
	})

	t.Run("hash256", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the known hash and the words of a key", func(t *testing.T) {
			t.Parallel()
			sum, w := hash256(known, counting[id.ID256]())
			expect.Equal(t, sum, uint64(0x0874f577bf9557fb), "hash256 must return the known hash of the key")
			expect.Equal(t, w, words{
				w0: 0x0706050403020100, w1: 0x0f0e0d0c0b0a0908,
				w2: 0x1716151413121110, w3: 0x1f1e1d1c1b1a1918,
			}, "hash256 must return the four words of the key")
		})
	})
}

// counting returns the key whose byte i is i.
func counting[K Key]() K {
	var k K
	for i := range len(k) {
		k[i] = byte(i)
	}

	return k
}
