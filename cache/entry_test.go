// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package cache_test

import (
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/cache"
	"go.thesmos.sh/core/clock/fake"
)

// TestEntry checks the hits, the expiry, the pins and the callbacks of the
// entries of a Cache, through the methods of the Cache. A Cache of
// capacity 10 examines its small queue whenever the queue contains an
// entry, because a tenth of 10 is 1.
func TestEntry(t *testing.T) {
	t.Parallel()

	t.Run("touch", func(t *testing.T) {
		t.Parallel()

		t.Run("keeps an entry that a Get hit through the next eviction", func(t *testing.T) {
			t.Parallel()
			c, _, r := newCache(t, 10, nil)
			setAll(c, "a")
			c.Get("a")
			setAll(c, names("k", 10)...)
			_, ok := c.Get("a")
			expect.True(t, ok, "the hit must move the entry to the main queue")
			expect.Equal(t, r.got(), []string{"k0"}, "the next entry without a hit must leave in its place")
		})

		t.Run("keeps an entry that a Pin hit through the next eviction", func(t *testing.T) {
			t.Parallel()
			c, _, r := newCache(t, 10, nil)
			setAll(c, "a")
			p, _ := c.Pin("a")
			p.Unpin()
			setAll(c, names("k", 10)...)
			_, ok := c.Get("a")
			expect.True(t, ok, "a Pin must count as a hit")
			expect.Equal(t, r.got(), []string{"k0"}, "the next entry without a hit must leave in its place")
		})

		t.Run("leaves an entry without hits to the next eviction", func(t *testing.T) {
			t.Parallel()
			c, _, r := newCache(t, 10, nil)
			setAll(c, "a")
			setAll(c, names("k", 10)...)
			assert.Equal(t, r.got(), []string{"a"}, "the oldest entry without a hit must leave")
		})
	})

	t.Run("expired", func(t *testing.T) {
		t.Parallel()

		t.Run("removes an expired entry that was hit before an entry without hits", func(t *testing.T) {
			t.Parallel()
			c, clk, r := newCache(t, 10, nil)
			c.Set("a", 1, origin.Add(time.Second))
			c.Get("a")
			setAll(c, names("k", 9)...)
			clk.Advance(time.Second)
			setAll(c, "last")
			assert.Equal(t, r.got(), []string{"a"}, "an expired entry must leave whatever its hits")
		})
	})

	t.Run("pin", func(t *testing.T) {
		t.Parallel()

		t.Run("keeps a pinned entry through evictions", func(t *testing.T) {
			t.Parallel()
			c, _, r := newCache(t, 10, nil)
			setAll(c, "a")
			p, _ := c.Pin("a")
			defer p.Unpin()
			setAll(c, names("k", 30)...)
			_, ok := c.Get("a")
			expect.True(t, ok, "a pinned entry must remain in the cache")
			expect.Length(t, r.got(), 21, "the entries without pins must leave instead")
		})

		t.Run("passes a pinned entry that left to Evicted at its last Unpin", func(t *testing.T) {
			t.Parallel()
			c, _, r := newCache(t, 10, nil)
			setAll(c, "a")
			first, _ := c.Pin("a")
			second, _ := c.Pin("a")
			c.Delete("a")
			assert.Empty(t, r.got(), "a pinned entry must not reach Evicted at its removal")
			first.Unpin()
			assert.Empty(t, r.got(), "an entry with a pin left must not reach Evicted")
			second.Unpin()
			assert.Equal(t, r.got(), []string{"a"}, "the last Unpin must pass the entry to Evicted")
		})
	})

	t.Run("victims", func(t *testing.T) {
		t.Parallel()

		t.Run("passes more than eight evicted entries to Evicted in the order of eviction", func(t *testing.T) {
			t.Parallel()
			c, _, r := newCache(t, 20, func(v int) int64 { return int64(v) })
			keys := names("k", 20)
			setAll(c, keys...)
			c.Set("big", 20, time.Time{})
			assert.Equal(t, r.got(), keys, "the twenty evicted entries must reach Evicted in order")
		})

		t.Run("evicts without an Evicted", func(t *testing.T) {
			t.Parallel()
			c, err := cache.New(cache.Config[string, int]{Clock: fake.New(origin), Capacity: 1})
			assert.NoError(t, err, "New must accept the configuration")
			setAll(c, "a", "b")
			assert.Equal(t, c.Len(), 1, "a Cache without Evicted must still evict")
		})
	})
}
