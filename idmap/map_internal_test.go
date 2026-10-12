// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package idmap

import (
	"fmt"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/id"
)

// TestMapTable checks the table of a Map: the slots that New allocates, the
// limit at which Set doubles them, and the runs that Delete leaves.
func TestMapTable(t *testing.T) {
	t.Parallel()

	t.Run("New", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give int
			want int
		}{
			{name: "allocates 8 slots for a capacity below 1", give: 0, want: 8},
			{name: "allocates 8 slots for a capacity of 5", give: 5, want: 8},
			{name: "allocates 16 slots for a capacity of 6", give: 6, want: 16},
			{name: "allocates 16 slots for a capacity of 10", give: 10, want: 16},
			{name: "allocates 32 slots for a capacity of 11", give: 11, want: 32},
			{name: "allocates 2^21 slots for a capacity of 1,000,000", give: 1_000_000, want: 1 << 21},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				m := New[id.ID128, int](tt.give)
				assert.Length(t, m.tags, tt.want, "New must allocate the least table with room for the capacity")
			})
		}
	})

	t.Run("Set", func(t *testing.T) {
		t.Parallel()

		t.Run("doubles the slots for the first new key past five eighths of the slots", func(t *testing.T) {
			t.Parallel()
			m := New[id.ID128, int](5)
			for i := range 5 {
				m.Set(keyAt[id.ID128](i), i)
			}
			assert.Length(t, m.tags, 8, "a table of 8 slots must take 5 keys")
			m.Set(keyAt[id.ID128](5), 5)
			assert.Length(t, m.tags, 16, "the sixth key must double the slots")
		})
	})

	t.Run("Delete", func(t *testing.T) {
		t.Parallel()

		t.Run("keeps every key reachable from its home slot without an empty slot on the way", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "every key must be reachable from its home slot after any writes", func(pc *prop.Case) {
				ops := pc.Draw(prop.List(prop.Integer(-24, 23), prop.MaxSize(200)), "ops")
				m := New[id.ID128, int](6)
				for _, op := range ops {
					if op < 0 {
						m.Delete(keyAt[id.ID128](-op - 1))
					} else {
						m.Set(keyAt[id.ID128](op), op)
					}
				}

				full := make([]uint64, 0, len(m.tags))
				for j, tag := range m.tags {
					if tag != 0 {
						full = append(full, uint64(j))
					}
				}
				assert.Total(pc, func(j uint64) error {
					h, _ := m.hash(m.seed, m.keys[j])
					if m.tags[j] != uint8(h>>tagShift)|fullTag {
						return fmt.Errorf("slot %d: tag %#x for a key of hash %#x", j, m.tags[j], h)
					}
					for i := h & m.mask; i != j; i = (i + 1) & m.mask {
						if m.tags[i] == 0 {
							return fmt.Errorf("slot %d: the walk from home slot %d passes the empty slot %d",
								j, h&m.mask, i)
						}
					}

					return nil
				}, full, "every full slot must have the tag of its key, and a walk without an empty slot from its home slot")
			})
		})
	})
}

// keyAt returns the key number i of an internal test: i in its first 8
// bytes, little-endian.
func keyAt[K Key](i int) K {
	var k K
	for b := range 8 {
		k[b] = byte(i >> (8 * b))
	}

	return k
}
