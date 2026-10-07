// Copyright Thesmos 2026
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

		t.Run("reports false while a pin remains", func(t *testing.T) {
			t.Parallel()
			e := &entry[string, int]{}
			e.pin()
			e.pin()
			expect.False(t, e.unpin(), "the first unpin must leave a pin")
			expect.Equal(t, e.state.Load(), int64(1), "one pin must remain")
		})

		t.Run("reports true at the last unpin of an entry that left", func(t *testing.T) {
			t.Parallel()
			e := &entry[string, int]{}
			e.pin()
			e.leave()
			assert.True(t, e.unpin(), "the last unpin must report the callback")
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
	})
}
