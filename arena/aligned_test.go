// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package arena_test

import (
	"reflect"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/arena"
)

// alignedContract is the contract of the property of Aligned.
const alignedContract = "Aligned must return n zero bytes of capacity n at a multiple of align"

// directAlign is a memory alignment of 4 KiB, which a file for direct I/O can
// require.
const directAlign = 4096

// alignments generates the align of a call from two generators. One draws a
// power of two up to 64 KiB, and the other draws any alignment up to 10,000.
var alignments = prop.OneOf(
	prop.Integer(0, 16).Map(func(k int) int { return 1 << k }),
	prop.Integer(1, 10_000),
)

func TestAligned(t *testing.T) {
	t.Parallel()

	t.Run("Aligned", func(t *testing.T) {
		t.Parallel()

		t.Run("returns n zero bytes of capacity n at a multiple of align", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, alignedContract, func(c *prop.Case) {
				n := c.Draw(prop.Integer(1, 1<<16), "n")
				align := c.Draw(alignments, "align")

				got := arena.Aligned(n, align)
				assert.Equal(c, got, make([]byte, n), "the slice has n zero bytes")
				assert.Equal(c, cap(got), n, "the capacity of the slice is n")
				assert.Equal(c, reflect.ValueOf(&got[0]).Pointer()%uintptr(align), 0,
					"the first byte is at a multiple of align")
			})
		})

		tests := []struct {
			name      string
			giveN     int
			giveAlign int
			want      []byte
		}{
			{name: "returns nil for an n of 0", giveN: 0, giveAlign: directAlign},
			{name: "returns nil for a negative n", giveN: -1, giveAlign: directAlign},
			{name: "returns n zero bytes for an align of 0", giveN: 5, giveAlign: 0, want: make([]byte, 5)},
			{name: "returns n zero bytes for a negative align", giveN: 5, giveAlign: -1, want: make([]byte, 5)},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got := arena.Aligned(tt.giveN, tt.giveAlign)
				expect.Equal(t, got, tt.want, "the slice has the bytes that the case expects")
				expect.Equal(t, cap(got), len(tt.want), "the capacity of the slice is its length")
			})
		}
	})
}

// TestAlignedAllocs checks the allocation contract of Aligned. MaxAllocs
// counts the allocations of the whole process, so the test does not run in
// parallel.
func TestAlignedAllocs(t *testing.T) {
	t.Run("Aligned", func(t *testing.T) {
		t.Run("allocates the bytes once", func(t *testing.T) {
			var got []byte
			expect.MaxAllocs(t, func() { got = arena.Aligned(directAlign, directAlign) }, 1,
				"Aligned allocates the bytes once")
			assert.Length(t, got, directAlign, "the test measures a slice of n bytes")
		})

		t.Run("does not allocate for an n of 0", func(t *testing.T) {
			got := []byte{}
			expect.MaxAllocs(t, func() { got = arena.Aligned(0, directAlign) }, 0,
				"Aligned does not allocate for an n of 0")
			assert.Nil(t, got, "the test measures the call for an n of 0")
		})
	})
}

// BenchmarkAligned reports the cost of Aligned for a buffer of direct I/O and
// fails when Aligned allocates more than once or more than the 8,192 bytes of
// the size class of its request.
func BenchmarkAligned(b *testing.B) {
	b.Run("Aligned", func(b *testing.B) {
		var got []byte

		c := bench.Start(b).MaxAllocs(1).MaxBytes(2 * directAlign)
		defer c.End()

		for c.Loop() {
			got = arena.Aligned(directAlign, directAlign)
		}

		assert.Length(b, got, directAlign, "the benchmark measures a slice of n bytes")
	})
}
