// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package sstable_test

import (
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/sstable"
)

// The bounds of the size classes of the pooled blocks.
const (
	// smallestClass is the footprint of a block of at most smallestClass
	// bytes.
	smallestClass = 1 << 10

	// largestClass is the footprint of the largest pooled block.
	largestClass = 1 << 24

	// pooledBytes is the size of the blocks of the allocation checks and
	// the benchmarks.
	pooledBytes = 4096
)

func TestBlock(t *testing.T) {
	t.Parallel()

	t.Run("NewBlock", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a block of n bytes in a buffer at most an eighth larger", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "NewBlock must return n bytes in a buffer of its class", func(pc *prop.Case) {
				n := pc.Draw(prop.Integer(smallestClass+1, largestClass), "bytes")
				b := sstable.NewBlock(n)
				defer b.Release()
				assert.Length(pc, b.Bytes(), n, "the block must have n bytes")
				assert.InRange(pc, b.Footprint()-n, 0, float64(b.Footprint())/8,
					"the buffer must exceed the block by at most an eighth of its bytes")
			})
		})

		t.Run("returns a buffer of the smallest class for a block of at most its bytes", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "NewBlock must return the smallest class for a small block", func(pc *prop.Case) {
				n := pc.Draw(prop.Integer(0, smallestClass), "bytes")
				b := sstable.NewBlock(n)
				defer b.Release()
				assert.Length(pc, b.Bytes(), n, "the block must have n bytes")
				assert.Equal(pc, b.Footprint(), smallestClass, "the buffer must have the smallest class")
			})
		})

		t.Run("returns a block of no bytes for a negative n", func(t *testing.T) {
			t.Parallel()
			b := sstable.NewBlock(-1)
			defer b.Release()
			assert.Empty(t, b.Bytes(), "a negative n must count as 0")
		})

		t.Run("returns a buffer of its own size for a block larger than the largest class", func(t *testing.T) {
			t.Parallel()
			b := sstable.NewBlock(largestClass + 1)
			defer b.Release()
			assert.Equal(t, b.Footprint(), largestClass+1, "the buffer must have the bytes of the block")
		})
	})

	t.Run("Bytes", func(t *testing.T) {
		t.Parallel()

		t.Run("returns bytes whose capacity is their length", func(t *testing.T) {
			t.Parallel()
			b := sstable.NewBlock(smallestClass + 1)
			defer b.Release()
			assert.Equal(t, cap(b.Bytes()), smallestClass+1, "an append to the bytes must not write into the buffer")
		})
	})

	t.Run("Retain", func(t *testing.T) {
		t.Parallel()

		t.Run("keeps the buffer out of the pool until the last Release", func(t *testing.T) {
			t.Parallel()
			b := sstable.NewBlock(pooledBytes)
			b.Retain()
			b.Release()
			other := sstable.NewBlock(pooledBytes)
			expect.NotEqual(t, other, b, "the pool must not lend a buffer that a reference keeps",
				assert.ByIdentity())
			other.Release()
			b.Release()
		})
	})
}

// TestBlockAllocs checks the allocation contract of a Block. MaxAllocs
// counts the allocations of the whole process, so the test does not run in
// parallel.
func TestBlockAllocs(t *testing.T) {
	t.Run("NewBlock", func(t *testing.T) {
		n := 0
		expect.MaxAllocs(t, func() {
			b := sstable.NewBlock(pooledBytes)
			n = len(b.Bytes())
			b.Release()
		}, 0, "a NewBlock after the last Release of a block of its class must not allocate")
		expect.Equal(t, n, pooledBytes, "the test must measure blocks of pooledBytes")
	})

	t.Run("Retain", func(t *testing.T) {
		b := sstable.NewBlock(pooledBytes)
		defer b.Release()
		expect.MaxAllocs(t, func() {
			b.Retain()
			b.Release()
		}, 0, "a Retain and its Release must not allocate")
	})
}

// BenchmarkBlock reports the cost of a Block from the pool, and fails when
// it allocates.
func BenchmarkBlock(b *testing.B) {
	b.Run("NewBlock", func(b *testing.B) {
		n := 0

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			blk := sstable.NewBlock(pooledBytes)
			n = len(blk.Bytes())
			blk.Release()
		}

		assert.Equal(b, n, pooledBytes, "the benchmark must measure blocks of pooledBytes")
	})
}
