// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package cache

import (
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
)

// TestIndexLayout is in package cache because index is unexported. It pins
// the slots that the index uses, which no method of the Cache reports.
func TestIndexLayout(t *testing.T) {
	t.Parallel()

	tomb := new(entry[uint64, int])

	t.Run("full", func(t *testing.T) {
		t.Parallel()

		t.Run("reports false while one more entry fits three quarters of the slots", func(t *testing.T) {
			t.Parallel()
			ix := newIndex[uint64, int](minSlots)
			ix.used = 11
			assert.False(t, ix.full(), "12 of 16 slots must not be full")
		})

		t.Run("reports true when one more entry exceeds three quarters of the slots", func(t *testing.T) {
			t.Parallel()
			ix := newIndex[uint64, int](minSlots)
			ix.used = 12
			assert.True(t, ix.full(), "13 of 16 slots must be full")
		})
	})

	t.Run("insert", func(t *testing.T) {
		t.Parallel()

		t.Run("stores an entry in the slot of its hash", func(t *testing.T) {
			t.Parallel()
			ix := indexOf(t, tomb, 5)
			assert.NotNil(t, ix.slots[5].Load(), "the entry must take the slot of its hash")
			expect.Equal(t, ix.slots[5].Load().key, uint64(5), "the slot must contain the entry")
			expect.Equal(t, ix.used, 1, "the slot must count as used")
			expect.Equal(t, ix.live, 1, "the slot must count as live")
		})

		t.Run("stores an entry in the next slot after a collision", func(t *testing.T) {
			t.Parallel()
			ix := indexOf(t, tomb, 5, 21)
			assert.Equal(t, ix.slots[6].Load().key, uint64(21), "a colliding entry must take the next slot")
		})

		t.Run("stores an entry in slot 0 after a collision in the last slot", func(t *testing.T) {
			t.Parallel()
			ix := indexOf(t, tomb, 15, 31)
			assert.Equal(t, ix.slots[0].Load().key, uint64(31), "the probe must wrap around to slot 0")
		})

		t.Run("stores an entry in the first tomb of its probe", func(t *testing.T) {
			t.Parallel()
			ix := indexOf(t, tomb, 5, 21)
			ix.remove(ix.slots[5].Load(), tomb)
			ix.insert(&entry[uint64, int]{key: 37, hash: 37}, tomb)
			expect.Equal(t, ix.slots[5].Load().key, uint64(37), "the entry must take the tomb")
			expect.Equal(t, ix.used, 2, "a tomb must not count as a new used slot")
			expect.Equal(t, ix.live, 2, "the entry must count as live")
		})
	})

	t.Run("remove", func(t *testing.T) {
		t.Parallel()

		t.Run("replaces the entry with the tomb", func(t *testing.T) {
			t.Parallel()
			ix := indexOf(t, tomb, 5, 21)
			ix.remove(ix.slots[6].Load(), tomb)
			expect.Equal(t, ix.slots[6].Load(), tomb, "the slot must contain the tomb", expect.ByIdentity())
			expect.Equal(t, ix.used, 2, "the tomb must count as used")
			expect.Equal(t, ix.live, 1, "the tomb must not count as live")
		})

		t.Run("leaves the other entries of the probe in their slots", func(t *testing.T) {
			t.Parallel()
			ix := indexOf(t, tomb, 5, 21, 37)
			first, third := ix.slots[5].Load(), ix.slots[7].Load()
			ix.remove(ix.slots[6].Load(), tomb)
			expect.Equal(t, ix.slots[5].Load(), first, "remove must leave the entry before it", expect.ByIdentity())
			expect.Equal(t, ix.slots[7].Load(), third, "remove must leave the entry after it", expect.ByIdentity())
		})
	})

	t.Run("find", func(t *testing.T) {
		t.Parallel()

		t.Run("returns nil after a probe of every slot", func(t *testing.T) {
			t.Parallel()
			ix := newIndex[uint64, int](minSlots)
			for i := range ix.slots {
				ix.slots[i].Store(&entry[uint64, int]{key: uint64(i), hash: uint64(i)})
			}
			assert.Nil(t, ix.find(99, 99, tomb), "a probe of a table without a nil slot must end")
		})

		t.Run("returns the entry past a tomb and an entry of another hash", func(t *testing.T) {
			t.Parallel()
			ix := indexOf(t, tomb, 5, 6, 21)
			ix.remove(ix.slots[5].Load(), tomb)
			e := ix.find(21, 21, tomb)
			assert.NotNil(t, e, "the probe must pass the tomb and the entry of hash 6")
			assert.Equal(t, e.key, uint64(21), "the probe must return the entry of the key")
		})

		t.Run("returns nil for a key of the same hash as an entry", func(t *testing.T) {
			t.Parallel()
			ix := indexOf(t, tomb, 5)
			assert.Nil(t, ix.find(6, 5, tomb), "an entry of another key must not match")
		})

		t.Run("returns nil for the zero key at a tomb of hash 0", func(t *testing.T) {
			t.Parallel()
			ix := indexOf(t, tomb, 16)
			ix.remove(ix.slots[0].Load(), tomb)
			assert.Nil(t, ix.find(0, 0, tomb), "the tomb must match no key, also the zero key of hash 0")
		})
	})

	t.Run("grow", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name  string
			live  int
			slots int
		}{
			{name: "returns 16 slots for no entry", live: 0, slots: 16},
			{name: "returns 16 slots for 7 entries", live: 7, slots: 16},
			{name: "returns 32 slots for 8 entries", live: 8, slots: 32},
			{name: "returns 32 slots for 15 entries", live: 15, slots: 32},
			{name: "returns 64 slots for 16 entries", live: 16, slots: 64},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				ix := newIndex[uint64, int](64)
				for k := range uint64(tt.live) {
					ix.insert(&entry[uint64, int]{key: k, hash: k}, tomb)
				}
				nx := ix.grow(tomb)
				expect.Length(t, nx.slots, tt.slots, "the grown index must have the smallest fitting power of two")
				expect.Equal(t, nx.mask, uint64(tt.slots-1), "the mask must select a slot of the grown index")
				expect.Equal(t, nx.live, tt.live, "the grown index must contain every entry")
			})
		}

		t.Run("drops the tombs", func(t *testing.T) {
			t.Parallel()
			ix := indexOf(t, tomb, 1, 2, 3, 4)
			ix.remove(ix.slots[2].Load(), tomb)
			ix.remove(ix.slots[3].Load(), tomb)
			nx := ix.grow(tomb)
			expect.Equal(t, nx.used, 2, "the grown index must use one slot per entry")
			expect.NotNil(t, nx.find(1, 1, tomb), "the grown index must keep the first live entry")
			expect.NotNil(t, nx.find(4, 4, tomb), "the grown index must keep the second live entry")
		})
	})
}

// indexOf returns an index of minSlots slots that contains an entry of each
// key of keys, inserted in order, with the hash of each entry equal to its
// key, so a case chooses the slot of each entry. It fails tb when the
// index becomes full.
func indexOf(tb assert.TB, tomb *entry[uint64, int], keys ...uint64) *index[uint64, int] {
	tb.Helper()
	ix := newIndex[uint64, int](minSlots)
	for _, k := range keys {
		assert.False(tb, ix.full(), "the index must not be full")
		ix.insert(&entry[uint64, int]{key: k, hash: k}, tomb)
	}

	return ix
}
