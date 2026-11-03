// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package cache

import (
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
)

// TestEntryState is in package cache because entry is unexported. It
// checks the transitions of an entry's pins and removed bit, including the
// orders that only a race between a lookup and a writer produces.
func TestEntryState(t *testing.T) {
	t.Parallel()

	t.Run("pin", func(t *testing.T) {
		t.Parallel()

		t.Run("reports false once the entry has left", func(t *testing.T) {
			t.Parallel()
			e := &entry[string, int]{}
			e.leave()
			expect.False(t, e.pin(), "a pin must fail after the removal")
			expect.Equal(t, e.state.Load(), int64(removed), "a failed pin must not count")
		})

		t.Run("counts each pin", func(t *testing.T) {
			t.Parallel()
			e := &entry[string, int]{}
			assert.True(t, e.pin(), "the first pin must succeed")
			assert.True(t, e.pin(), "the second pin must succeed")
			assert.Equal(t, e.state.Load(), int64(2), "the state must count both pins")
		})
	})

	t.Run("unpin", func(t *testing.T) {
		t.Parallel()

		t.Run("reports no last pin while a pin remains", func(t *testing.T) {
			t.Parallel()
			e := &entry[string, int]{}
			e.pin()
			e.pin()
			last, left, _ := e.unpin()
			expect.False(t, last, "the first unpin must leave a pin")
			expect.False(t, left, "the entry must still be in the cache")
			expect.Equal(t, e.state.Load(), int64(1), "one pin must remain")
		})

		t.Run("reports the last pin of an entry in the cache", func(t *testing.T) {
			t.Parallel()
			e := &entry[string, int]{}
			e.pin()
			last, left, _ := e.unpin()
			expect.True(t, last, "the unpin must remove the last pin")
			expect.False(t, left, "the entry must still be in the cache")
		})

		t.Run("reports the last pin of an entry that left", func(t *testing.T) {
			t.Parallel()
			e := &entry[string, int]{}
			e.pin()
			e.leave()
			last, left, _ := e.unpin()
			expect.True(t, last, "the unpin must remove the last pin")
			expect.True(t, left, "the entry must have left the cache")
		})

		t.Run("reports an entry that left while a pin remains", func(t *testing.T) {
			t.Parallel()
			e := &entry[string, int]{}
			e.pin()
			e.pin()
			e.leave()
			last, left, _ := e.unpin()
			expect.False(t, last, "the first unpin must leave a pin")
			expect.True(t, left, "the entry must have left the cache")
		})

		t.Run("reports the mark of an entry that the evictor set aside at its last pin", func(t *testing.T) {
			t.Parallel()
			e := &entry[string, int]{}
			e.pin()
			e.markAside()
			last, left, marked := e.unpin()
			expect.True(t, last, "the unpin must remove the last pin")
			expect.False(t, left, "the entry must still be in the cache")
			expect.True(t, marked, "the last unpin must report the mark of the evictor")
		})

		t.Run("reports no mark while a pin remains", func(t *testing.T) {
			t.Parallel()
			e := &entry[string, int]{}
			e.pin()
			e.pin()
			e.markAside()
			_, _, marked := e.unpin()
			assert.False(t, marked, "an unpin that leaves a pin must not report the mark")
		})

		t.Run("reports no mark of an entry that the evictor did not set aside", func(t *testing.T) {
			t.Parallel()
			e := &entry[string, int]{}
			e.pin()
			_, _, marked := e.unpin()
			assert.False(t, marked, "the last unpin of an entry without the mark must not report one")
		})
	})

	t.Run("clearAside", func(t *testing.T) {
		t.Parallel()

		t.Run("reports true for an entry that the evictor set aside", func(t *testing.T) {
			t.Parallel()
			e := &entry[string, int]{}
			e.pin()
			e.markAside()
			expect.True(t, e.clearAside(), "clearAside must clear the mark of the evictor")
			expect.Equal(t, e.state.Load(), int64(1), "clearAside must keep the pins")
		})

		t.Run("reports false for an entry without the mark", func(t *testing.T) {
			t.Parallel()
			e := &entry[string, int]{}
			e.pin()
			expect.False(t, e.clearAside(), "clearAside must report an entry without the mark")
			expect.Equal(t, e.state.Load(), int64(1), "clearAside must keep the pins")
		})
	})

	t.Run("evict", func(t *testing.T) {
		t.Parallel()

		t.Run("reports false for a pinned entry", func(t *testing.T) {
			t.Parallel()
			e := &entry[string, int]{}
			e.pin()
			expect.False(t, e.evict(), "the evictor must not remove a pinned entry")
			expect.True(t, e.pin(), "a failed eviction must not mark the entry as removed")
		})

		t.Run("reports true for an entry without pins", func(t *testing.T) {
			t.Parallel()
			e := &entry[string, int]{}
			assert.True(t, e.evict(), "the evictor must remove an entry without pins")
		})

		t.Run("refuses every pin after the eviction", func(t *testing.T) {
			t.Parallel()
			e := &entry[string, int]{}
			e.evict()
			assert.False(t, e.pin(), "no pin may follow the eviction")
		})
	})

	t.Run("markAside", func(t *testing.T) {
		t.Parallel()

		t.Run("marks a pinned entry", func(t *testing.T) {
			t.Parallel()
			e := &entry[string, int]{}
			e.pin()
			expect.True(t, e.markAside(), "markAside must mark a pinned entry")
			expect.Equal(t, e.state.Load(), int64(1|aside), "the state must keep the pin beside the mark")
		})

		t.Run("reports false for an entry without pins", func(t *testing.T) {
			t.Parallel()
			e := &entry[string, int]{}
			expect.False(t, e.markAside(), "markAside must refuse an entry that the evictor can remove")
			expect.Equal(t, e.state.Load(), int64(0), "a refused mark must not change the state")
		})
	})

	t.Run("leave", func(t *testing.T) {
		t.Parallel()

		t.Run("reports false for a pinned entry", func(t *testing.T) {
			t.Parallel()
			e := &entry[string, int]{}
			e.pin()
			assert.False(t, e.leave(), "the remover must not call back a pinned entry")
		})

		t.Run("reports true for an entry without pins", func(t *testing.T) {
			t.Parallel()
			e := &entry[string, int]{}
			assert.True(t, e.leave(), "the remover must call back an entry without pins")
		})

		t.Run("reports false for an entry that left before", func(t *testing.T) {
			t.Parallel()
			e := &entry[string, int]{}
			e.leave()
			assert.False(t, e.leave(), "leave must report an entry once")
		})

		t.Run("reports true for an entry without pins that the evictor set aside", func(t *testing.T) {
			t.Parallel()
			e := &entry[string, int]{}
			e.pin()
			e.markAside()
			e.unpin()
			assert.True(t, e.leave(), "the remover must call back an entry whose last pin ended while the mark remains")
		})

		t.Run("reports false for a pinned entry that the evictor set aside", func(t *testing.T) {
			t.Parallel()
			e := &entry[string, int]{}
			e.pin()
			e.markAside()
			assert.False(t, e.leave(), "the remover must not call back a pinned entry")
		})
	})
}
