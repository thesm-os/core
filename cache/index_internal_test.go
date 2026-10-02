// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package cache

import (
	"testing"

	"go.thesmos.sh/testkit"
)

// indexOf returns an index of minSlots slots that contains an entry of each
// key of keys, inserted in order, with the hash of each entry equal to its
// key, so a case chooses the slot of each entry. It fails tb when the
// index becomes full.
func indexOf(tb testing.TB, tomb *entry[uint64, int], keys ...uint64) *index[uint64, int] {
	tb.Helper()

	ix := newIndex[uint64, int](minSlots)
	for _, k := range keys {
		testkit.False(tb, ix.full(), "the index must not be full")
		ix.insert(&entry[uint64, int]{key: k, hash: k}, tomb)
	}

	return ix
}

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
			testkit.False(t, ix.full(), "12 of 16 slots must not be full")
		})

		t.Run("reports true when one more entry exceeds three quarters of the slots", func(t *testing.T) {
			t.Parallel()
			ix := newIndex[uint64, int](minSlots)
			ix.used = 12
			testkit.True(t, ix.full(), "13 of 16 slots must be full")
		})
	})

	t.Run("insert", func(t *testing.T) {
		t.Parallel()

		t.Run("stores an entry in the slot of its hash", func(t *testing.T) {
			t.Parallel()
			ix := indexOf(t, tomb, 5)
			testkit.Equal(t, ix.slots[5].Load().key, uint64(5), "the entry must take the slot of its hash")
			testkit.Equal(t, ix.used, 1, "a nil slot must count as used")
			testkit.Equal(t, ix.live, 1, "the entry must count as live")
		})

		t.Run("stores an entry in the next slot after a collision", func(t *testing.T) {
			t.Parallel()
			ix := indexOf(t, tomb, 5, 21)
			testkit.Equal(t, ix.slots[6].Load().key, uint64(21), "a colliding entry must take the next slot")
		})

		t.Run("stores an entry in slot 0 after a collision in the last slot", func(t *testing.T) {
			t.Parallel()
			ix := indexOf(t, tomb, 15, 31)
			testkit.Equal(t, ix.slots[0].Load().key, uint64(31), "the probe must wrap around to slot 0")
		})

		t.Run("stores an entry in the first tomb of its probe", func(t *testing.T) {
			t.Parallel()
			ix := indexOf(t, tomb, 5, 21)
			ix.remove(ix.slots[5].Load(), tomb)

			ix.insert(&entry[uint64, int]{key: 37, hash: 37}, tomb)
			testkit.Equal(t, ix.slots[5].Load().key, uint64(37), "the entry must take the tomb")
			testkit.Equal(t, ix.used, 2, "a tomb must not count as a new used slot")
			testkit.Equal(t, ix.live, 2, "the entry must count as live")
		})
	})

	t.Run("remove", func(t *testing.T) {
		t.Parallel()

		t.Run("replaces the entry with the tomb", func(t *testing.T) {
			t.Parallel()
			ix := indexOf(t, tomb, 5, 21)
			ix.remove(ix.slots[6].Load(), tomb)

			testkit.True(t, ix.slots[6].Load() == tomb, "the slot must hold the tomb")
			testkit.Equal(t, ix.live, 1, "the entry must no longer count as live")
			testkit.Equal(t, ix.used, 2, "the tomb must still count as used")
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

			testkit.True(t, ix.find(99, 99, tomb) == nil, "a probe of a table without a nil slot must end")
		})

		t.Run("returns the entry past a tomb and an entry of another hash", func(t *testing.T) {
			t.Parallel()
			ix := indexOf(t, tomb, 5, 6, 21)
			ix.remove(ix.slots[5].Load(), tomb)

			e := ix.find(21, 21, tomb)
			testkit.True(t, e != nil && e.key == 21, "the probe must pass the tomb and the entry of hash 6")
		})

		t.Run("returns nil for a key of the same hash as an entry", func(t *testing.T) {
			t.Parallel()
			ix := indexOf(t, tomb, 5)
			testkit.True(t, ix.find(6, 5, tomb) == nil, "an entry of another key must not match")
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
				testkit.Len(t, nx.slots, tt.slots, "the grown index must have the smallest fitting power of two")
				testkit.Equal(t, nx.mask, uint64(tt.slots-1), "the mask must select a slot of the grown index")
				testkit.Equal(t, nx.live, tt.live, "the grown index must hold every entry")
			})
		}

		t.Run("drops the tombs", func(t *testing.T) {
			t.Parallel()
			ix := indexOf(t, tomb, 1, 2, 3, 4)
			ix.remove(ix.slots[2].Load(), tomb)
			ix.remove(ix.slots[3].Load(), tomb)

			nx := ix.grow(tomb)
			testkit.Equal(t, nx.used, 2, "the grown index must use one slot per entry")
			testkit.True(t, nx.find(1, 1, tomb) != nil && nx.find(4, 4, tomb) != nil,
				"the grown index must keep the live entries")
		})
	})
}
