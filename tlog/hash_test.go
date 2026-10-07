// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tlog_test

import (
	"crypto/sha256"
	"crypto/sha512"
	"slices"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/crypto"
	coresha256 "go.thesmos.sh/core/crypto/sha256"
	coresha512 "go.thesmos.sh/core/crypto/sha512"
	"go.thesmos.sh/core/tlog"
)

// The prefixes of RFC 9162 that the tests pin: one byte before the data
// of a leaf, and one before the two children of a node.
const (
	leafPrefix = 0x00
	nodePrefix = 0x01
)

func TestHash(t *testing.T) {
	t.Parallel()

	h := coresha256.New()

	t.Run("LeafHash", func(t *testing.T) {
		t.Parallel()

		t.Run("returns SHA-256(0x00 ‖ data)", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, func(data []byte) []byte { return tlog.LeafHash(h, data).Bytes() }, func(data []byte) []byte {
				sum := sha256.Sum256(append([]byte{leafPrefix}, data...))

				return sum[:]
			}, "LeafHash must return the leaf hash of RFC 6962")
		})

		t.Run("returns SHA-384(0x00 ‖ data) over SHA-384", func(t *testing.T) {
			t.Parallel()
			data := []byte("an entry")
			want := sha512.Sum384(append([]byte{leafPrefix}, data...))
			assert.Equal(t, tlog.LeafHash(coresha512.New384(), data).Bytes(), want[:],
				"the prefix must not depend on the hash")
		})
	})

	t.Run("NodeHash", func(t *testing.T) {
		t.Parallel()

		t.Run("returns SHA-256(0x01 ‖ left ‖ right)", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "NodeHash must return the node hash of RFC 6962", func(c *prop.Case) {
				left, right := h.Hash(c.Draw(prop.Bytes(), "left")), h.Hash(c.Draw(prop.Bytes(), "right"))
				want := sha256.Sum256(slices.Concat([]byte{nodePrefix}, left.Bytes(), right.Bytes()))
				assert.Equal(c, tlog.NodeHash(h, left, right).Bytes(), want[:], "NodeHash must hash both children")
			})
		})

		t.Run("returns SHA-512(0x01 ‖ left ‖ right) over SHA-512", func(t *testing.T) {
			t.Parallel()
			h512 := coresha512.New512()
			left, right := h512.Hash([]byte("left")), h512.Hash([]byte("right"))
			want := sha512.Sum512(slices.Concat([]byte{nodePrefix}, left.Bytes(), right.Bytes()))
			assert.Equal(t, tlog.NodeHash(h512, left, right).Bytes(), want[:],
				"the node hash must cover both 64-byte children")
		})
	})
}

// TestHashAllocs checks the allocation contract of each function. MaxAllocs
// counts the allocations of the whole process, so the test does not run in
// parallel.
//
//nolint:paralleltest // see above
func TestHashAllocs(t *testing.T) {
	h := coresha256.New()
	data := make([]byte, 128)
	left, right := h.Hash([]byte("left")), h.Hash([]byte("right"))

	t.Run("LeafHash", func(t *testing.T) {
		var got crypto.Digest
		expect.MaxAllocs(t, func() { got = tlog.LeafHash(h, data) }, 0, "LeafHash must not allocate")
		assert.NotEqual(t, got, crypto.Digest{}, "the test must measure a hash")
	})

	t.Run("NodeHash", func(t *testing.T) {
		var got crypto.Digest
		expect.MaxAllocs(t, func() { got = tlog.NodeHash(h, left, right) }, 0, "NodeHash must not allocate")
		assert.NotEqual(t, got, crypto.Digest{}, "the test must measure a hash")
	})
}

// BenchmarkHash reports the cost of each function, and fails above the
// allocations that their contracts state.
func BenchmarkHash(b *testing.B) {
	h := coresha256.New()
	data := make([]byte, 128)
	left, right := h.Hash([]byte("left")), h.Hash([]byte("right"))

	b.Run("LeafHash", func(b *testing.B) {
		var got crypto.Digest

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = tlog.LeafHash(h, data)
		}

		assert.NotEqual(b, got, crypto.Digest{}, "the benchmark must measure a hash")
	})

	b.Run("NodeHash", func(b *testing.B) {
		var got crypto.Digest

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = tlog.NodeHash(h, left, right)
		}

		assert.NotEqual(b, got, crypto.Digest{}, "the benchmark must measure a hash")
	})
}
