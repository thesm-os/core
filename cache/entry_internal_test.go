// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package cache

import (
	"testing"

	"go.thesmos.sh/testkit"
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
			testkit.False(t, e.pin(), "a pin must fail after the removal")
			testkit.False(t, e.pinned(), "a failed pin must not count")
		})

		t.Run("counts each pin", func(t *testing.T) {
			t.Parallel()
			e := &entry[string, int]{}
			first := e.pin()
			second := e.pin()
			testkit.True(t, first && second, "two pins must succeed")
			testkit.False(t, e.unpin(), "the first unpin must leave a pin")
			testkit.True(t, e.pinned(), "one pin must remain")
		})
	})

	t.Run("evict", func(t *testing.T) {
		t.Parallel()

		t.Run("reports false for a pinned entry", func(t *testing.T) {
			t.Parallel()
			e := &entry[string, int]{}
			e.pin()
			testkit.False(t, e.evict(), "the evictor must not remove a pinned entry")
			testkit.True(t, e.pin(), "a failed eviction must not mark the entry as removed")
		})

		t.Run("reports true for an entry without pins and blocks later pins", func(t *testing.T) {
			t.Parallel()
			e := &entry[string, int]{}
			testkit.True(t, e.evict(), "the evictor must remove an entry without pins")
			testkit.False(t, e.pin(), "no pin may follow the eviction")
		})
	})

	t.Run("leave", func(t *testing.T) {
		t.Parallel()

		t.Run("reports false for a pinned entry and leaves the callback to its last unpin", func(t *testing.T) {
			t.Parallel()
			e := &entry[string, int]{}
			e.pin()
			testkit.False(t, e.leave(), "the remover must not call back a pinned entry")
			testkit.True(t, e.unpin(), "the last unpin must report the callback")
		})

		t.Run("reports true for an entry without pins", func(t *testing.T) {
			t.Parallel()
			e := &entry[string, int]{}
			testkit.True(t, e.leave(), "the remover must call back an entry without pins")
		})
	})
}
