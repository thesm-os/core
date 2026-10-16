// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package radix

import (
	"bytes"
	"encoding/binary"
	"strconv"
	"strings"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
)

// refusedRef is a Ref of a slab that a map without slabs does not have, so
// its slabs refuse the region.
const refusedRef = 99 << 32

// TestLeaf is in package radix because the records of the leaves and the
// slabs of the regions are unexported.
func TestLeaf(t *testing.T) {
	t.Parallel()

	t.Run("widthOf", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give int
			want width
		}{
			{name: "returns width16 for a negative length", give: -1, want: width16},
			{name: "returns width16 for 0", give: 0, want: width16},
			{name: "returns width16 for 12", give: 12, want: width16},
			{name: "returns width32 for 13", give: 13, want: width32},
			{name: "returns width32 for 28", give: 28, want: width32},
			{name: "returns width64 for 29", give: 29, want: width64},
			{name: "returns width64 for 60", give: 60, want: width64},
			{name: "returns width64 for a length above 60", give: 61, want: width64},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, widthOf(tt.give), tt.want, "widthOf rounds the inline length up to a width")
			})
		}
	})

	t.Run("newLeaf", func(t *testing.T) {
		t.Parallel()

		for _, inline := range []int{inline16, inline32, inline64} {
			t.Run("stores a key of "+strconv.Itoa(inline)+" bytes in its record", func(t *testing.T) {
				t.Parallel()
				m := New[string, int](Config{InlineKeyLen: inline})
				key := strings.Repeat("k", inline)
				h := m.newLeaf(key, 1)
				rec := m.record(h)
				expect.Length(t, rec, inline+lenBytes, "the record has the width of the map")
				expect.Equal(t, rec[:inline], []byte(key), "the record contains the key")
				expect.Equal(t, m.keys.InUse(), 0, "the key takes no region")
			})

			t.Run("stores a key of "+strconv.Itoa(inline+1)+" bytes in a region", func(t *testing.T) {
				t.Parallel()
				m := New[string, int](Config{InlineKeyLen: inline})
				key := strings.Repeat("k", inline+1)
				h := m.newLeaf(key, 1)
				expect.Equal(t, m.key(h), []byte(key), "the leaf returns the key from its region")
				expect.NotEqual(t, m.keys.InUse(), 0, "the key takes a region")
			})
		}

		t.Run("reuses the record that freeLeaf freed", func(t *testing.T) {
			t.Parallel()
			var m Map[string, int]
			h := m.newLeaf("first", 1)
			m.freeLeaf(h)
			reused := m.newLeaf("second", 2)
			expect.Equal(t, reused, h, "newLeaf returns the handle of the freed record")
			expect.Equal(t, m.values.Len(), 1, "newLeaf appends no record")
			expect.Equal(t, m.key(reused), []byte("second"), "the record contains the new key")
			expect.Equal(t, m.values.At(int(reused>>kindBits)), 2, "the record has the new value")
		})
	})

	t.Run("freeLeaf", func(t *testing.T) {
		t.Parallel()

		t.Run("takes the region of a long key back into the slabs", func(t *testing.T) {
			t.Parallel()
			var m Map[string, int]
			h := m.newLeaf(strings.Repeat("k", 40), 1)
			m.freeLeaf(h)
			expect.Equal(t, m.keys.InUse(), 0, "the slabs take the region back")
			expect.Equal(t, bytes.Count(m.record(h), []byte{0}), len(m.record(h)), "the record is zero")
			expect.Equal(t, m.spare[kindLeaf], []uint32{h >> kindBits}, "the record is free")
		})

		t.Run("keeps a record whose region the slabs refuse off the stack", func(t *testing.T) {
			t.Parallel()
			var m Map[string, int]
			h := m.newLeaf(strings.Repeat("k", 40), 1)
			binary.LittleEndian.PutUint64(m.record(h), refusedRef)
			m.freeLeaf(h)
			expect.Empty(t, m.spare[kindLeaf], "the record is not free")
			expect.Equal(t, binary.LittleEndian.Uint64(m.record(h)), uint64(refusedRef), "the record keeps its region")
		})
	})

	t.Run("freeRegions", func(t *testing.T) {
		t.Parallel()

		t.Run("takes the region of every long key back into the slabs", func(t *testing.T) {
			t.Parallel()
			var m Map[string, int]
			for i := range 10 {
				_, _, err := m.Set(strings.Repeat("k", 20+i), i)
				assert.NoError(t, err, "Set stores the key")
			}
			m.freeRegions()
			expect.Equal(t, m.keys.InUse(), 0, "the slabs take every region back")
			expect.Equal(t, slabCount(&m), 1, "the map keeps its slab")
		})

		t.Run("replaces slabs that refuse a region", func(t *testing.T) {
			t.Parallel()
			var m Map[string, int]
			for i := range 10 {
				_, _, err := m.Set(strings.Repeat("k", 20+i), i)
				assert.NoError(t, err, "Set stores the key")
			}
			binary.LittleEndian.PutUint64(m.record(m.minLeaf(m.root)), refusedRef)
			m.freeRegions()
			expect.Equal(t, m.keys.InUse(), 0, "the map has empty slabs")
			expect.Equal(t, slabCount(&m), 0, "the map drops the slabs that refused a region")
		})
	})
}

// slabCount returns the number of slabs of the regions of m.
func slabCount(m *Map[string, int]) int {
	n := 0
	for range m.keys.All() {
		n++
	}

	return n
}
