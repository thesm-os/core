// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package cache_test

import (
	"testing"

	"go.thesmos.sh/testkit"
)

// TestIndex checks the hash index of a Cache through Get, Set and Delete:
// lookups across the growth of the index, and across the tombs that Delete
// leaves in the probes of other keys.
func TestIndex(t *testing.T) {
	t.Parallel()

	t.Run("find", func(t *testing.T) {
		t.Parallel()

		t.Run("finds every key of an index that grew from 16 slots to 16,384", func(t *testing.T) {
			t.Parallel()
			keys := names("k", 10_000)
			c, _, _ := newCache(t, int64(len(keys)), nil)
			setAll(c, keys...)

			for _, k := range keys {
				_, ok := c.Get(k)
				testkit.True(t, ok, "Get must find "+k)
			}

			_, ok := c.Get("absent")
			testkit.False(t, ok, "Get must miss a key that no Set stored")
		})

		t.Run("passes the tombs of deleted keys", func(t *testing.T) {
			t.Parallel()
			keys := names("k", 100)
			c, _, _ := newCache(t, int64(len(keys)), nil)
			setAll(c, keys...)

			for i, k := range keys {
				if i%2 == 0 {
					c.Delete(k)
				}
			}

			for i, k := range keys {
				_, ok := c.Get(k)
				testkit.Equal(t, ok, i%2 == 1, "Get must find exactly the keys that were not deleted: "+k)
			}
		})
	})

	t.Run("insert", func(t *testing.T) {
		t.Parallel()

		t.Run("stores keys again in the slots of deleted keys", func(t *testing.T) {
			t.Parallel()
			keys := names("k", 1_000)
			c, _, _ := newCache(t, int64(len(keys)), nil)

			for range 10 {
				setAll(c, keys...)
				for _, k := range keys {
					c.Delete(k)
				}
			}
			setAll(c, keys...)

			testkit.Equal(t, c.Len(), len(keys), "the cache must hold every key once")
			for _, k := range keys {
				_, ok := c.Get(k)
				testkit.True(t, ok, "Get must find "+k)
			}
		})
	})
}
