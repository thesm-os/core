// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package sstable

import (
	"testing"

	"go.dokimi.dev/assert"
)

// TestRangeDeleteIterReferences checks the references of cursors of
// fragments to the fragment partitions of a LendingCache. An open cursor
// keeps a reference to the partition of its current position, and releases
// it once.
func TestRangeDeleteIterReferences(t *testing.T) {
	t.Parallel()

	t.Run("Reset", func(t *testing.T) {
		t.Parallel()

		t.Run("releases the partition of an open cursor", func(t *testing.T) {
			t.Parallel()
			cache := &refCache{}
			r := refTable(t, cache, 1)
			it := r.RangeDeletes()
			defer it.Close()
			assert.True(t, it.SeekGE(t.Context(), []byte("key 017")), "SeekGE lends the partition of the key")
			assert.NotNil(t, it.lent, "the cursor keeps the partition of the key")
			it.Reset(r)
			assert.Equal(t, cache.refs(), cache.want(), "each partition has the reference of the cache alone")
		})
	})

	t.Run("Close", func(t *testing.T) {
		t.Parallel()

		t.Run("releases the partition of the cursor", func(t *testing.T) {
			t.Parallel()
			cache := &refCache{}
			it := refTable(t, cache, 1).RangeDeletes()
			assert.True(t, it.SeekGE(t.Context(), []byte("key 017")), "SeekGE lends the partition of the key")
			assert.NotNil(t, it.lent, "the cursor keeps the partition of the key")
			it.Close()
			assert.Equal(t, cache.refs(), cache.want(), "each partition has the reference of the cache alone")
		})

		t.Run("releases no partition at a second Close", func(t *testing.T) {
			t.Parallel()
			cache := &refCache{}
			it := refTable(t, cache, 1).RangeDeletes()
			assert.True(t, it.SeekGE(t.Context(), []byte("key 017")), "SeekGE lends the partition of the key")
			it.Close()
			it.Close()
			assert.Equal(t, cache.refs(), cache.want(), "each partition keeps the reference of the cache")
		})
	})
}
