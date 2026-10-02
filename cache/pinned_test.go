// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package cache_test

import (
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/cache"
)

func TestPinned(t *testing.T) {
	t.Parallel()

	t.Run("Value", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the value at the time of Pin after a Set replaced it", func(t *testing.T) {
			t.Parallel()
			c, _, _ := newCache(t, 10, nil)
			c.Set("k", 1, time.Time{})
			p, _ := c.Pin("k")
			defer p.Unpin()

			c.Set("k", 2, time.Time{})
			testkit.Equal(t, p.Value(), 1, "the Pinned must keep the value that it pinned")

			v, _ := c.Get("k")
			testkit.Equal(t, v, 2, "the cache must return the new value")
		})

		t.Run("returns the zero value for the zero Pinned", func(t *testing.T) {
			t.Parallel()
			var p cache.Pinned[string, int]
			testkit.Equal(t, p.Value(), 0, "the zero Pinned must have the zero value")
		})
	})

	t.Run("Unpin", func(t *testing.T) {
		t.Parallel()

		t.Run("passes a replaced entry to Evicted", func(t *testing.T) {
			t.Parallel()
			c, _, r := newCache(t, 10, nil)
			c.Set("k", 1, time.Time{})
			p, _ := c.Pin("k")

			c.Set("k", 2, time.Time{})
			testkit.Equal(t, r.got(), []string(nil), "a replaced pinned entry must wait for its Unpin")

			p.Unpin()
			testkit.Equal(t, r.got(), []string{"k"}, "the Unpin must pass the replaced entry to Evicted")
		})

		t.Run("lets the cache evict the entry", func(t *testing.T) {
			t.Parallel()
			c, _, r := newCache(t, 1, nil)
			setAll(c, "a")
			p, _ := c.Pin("a")

			setAll(c, "b")
			testkit.Equal(t, c.Len(), 2, "the pinned entry must stay while the new one is kept")

			p.Unpin()
			setAll(c, "c")
			testkit.Equal(t, c.Len(), 1, "the unpinned entry must leave like any other")
			testkit.Equal(t, len(r.got()), 2, "two entries must leave for the third")
		})

		t.Run("passes nothing to Evicted for an entry in the cache", func(t *testing.T) {
			t.Parallel()
			c, _, r := newCache(t, 10, nil)
			c.Set("k", 1, time.Time{})
			p, _ := c.Pin("k")

			p.Unpin()
			testkit.Equal(t, r.got(), []string(nil), "an entry that is still in the cache must not reach Evicted")
		})

		t.Run("does nothing for the zero Pinned", func(t *testing.T) {
			t.Parallel()
			var p cache.Pinned[string, int]
			p.Unpin()
		})
	})
}
