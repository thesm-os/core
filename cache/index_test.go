// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package cache_test

import (
	"fmt"
	"testing"

	"go.dokimi.dev/assert"
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
			assert.Total(t, func(k string) error {
				if _, ok := c.Get(k); !ok {
					return fmt.Errorf("key %s: Get missed", k)
				}

				return nil
			}, keys, "Get must find every stored key")
			_, ok := c.Get("absent")
			assert.False(t, ok, "Get must miss a key that no Set stored")
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
			got, want := make([]bool, 0, len(keys)), make([]bool, 0, len(keys))
			for i, k := range keys {
				_, ok := c.Get(k)
				got, want = append(got, ok), append(want, i%2 == 1)
			}
			assert.Equal(t, got, want, "Get must find exactly the keys that were not deleted")
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
			assert.Equal(t, c.Len(), len(keys), "the cache must contain every key once")
			assert.Total(t, func(k string) error {
				if _, ok := c.Get(k); !ok {
					return fmt.Errorf("key %s: Get missed", k)
				}

				return nil
			}, keys, "Get must find every stored key")
		})
	})
}
