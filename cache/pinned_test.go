// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package cache_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/cache"
	"go.thesmos.sh/core/clock/fake"
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
			expect.Equal(t, p.Value(), 1, "the Pinned must keep the value that it pinned")
			v, _ := c.Get("k")
			expect.Equal(t, v, 2, "the cache must return the new value")
		})

		t.Run("returns the zero value for the zero Pinned", func(t *testing.T) {
			t.Parallel()
			var p cache.Pinned[string, int]
			assert.Equal(t, p.Value(), 0, "the zero Pinned must have the zero value")
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
			assert.Empty(t, r.got(), "a replaced pinned entry must wait for its Unpin")
			p.Unpin()
			assert.Equal(t, r.got(), []string{"k"}, "the Unpin must pass the replaced entry to Evicted")
		})

		t.Run("evicts down to the capacity at the last Unpin past it", func(t *testing.T) {
			t.Parallel()
			c, _, r := newCache(t, 2, nil)
			pins := []cache.Pinned[string, int]{
				c.SetPinned("a", 1, time.Time{}),
				c.SetPinned("b", 1, time.Time{}),
				c.SetPinned("c", 1, time.Time{}),
			}
			for _, p := range pins {
				p.Unpin()
			}
			expect.Equal(t, c.Cost(), int64(2), "the last Unpin must evict down to the capacity")
			expect.Equal(t, c.Len(), 2, "the cache must keep the entries that fit the capacity")
			expect.Length(t, r.got(), 1, "Evicted must receive the entry that the Unpin evicted")
		})

		t.Run("evicts down to the capacity at the last Unpin in a Cache without a Clock", func(t *testing.T) {
			t.Parallel()
			c, err := cache.New(cache.Config[string, int]{Capacity: 1})
			assert.NoError(t, err, "New must accept a Config without a Clock")
			p := c.SetPinned("a", 1, time.Time{})
			c.Set("b", 1, time.Time{})
			p.Unpin()
			assert.Equal(t, c.Len(), 1, "the last Unpin must evict without reading a clock")
		})

		t.Run("evicts an expired entry at the last Unpin whatever its hits", func(t *testing.T) {
			t.Parallel()
			c, clk, r := newCache(t, 2, nil)
			first := c.SetPinned("first", 1, time.Time{})
			second := c.SetPinned("second", 1, time.Time{})
			defer second.Unpin()
			c.Set("expiring", 1, origin.Add(time.Second))
			for range 3 {
				c.Get("first")
				c.Get("expiring")
			}
			clk.Advance(time.Second)
			first.Unpin()
			assert.Equal(t, r.got(), []string{"expiring"},
				"the last Unpin must evict the expired entry before the one with hits")
		})

		t.Run("keeps the cost past the capacity at an Unpin that leaves a pin", func(t *testing.T) {
			t.Parallel()
			c, _, r := newCache(t, 2, nil)
			first := c.SetPinned("a", 1, time.Time{})
			second, _ := c.Pin("a")
			defer second.Unpin()
			b := c.SetPinned("b", 1, time.Time{})
			defer b.Unpin()
			c.Set("c", 1, time.Time{})
			first.Unpin()
			expect.Equal(t, c.Cost(), int64(3), "an Unpin that leaves a pin must evict nothing")
			expect.Empty(t, r.got(), "no entry may leave at an Unpin that leaves a pin")
		})

		t.Run("passes the entries that it evicts to Evicted after it releases the lock", func(t *testing.T) {
			t.Parallel()
			var c *cache.Cache[string, int]
			var deleted atomic.Bool
			c, err := cache.New(cache.Config[string, int]{
				Clock:    fake.New(origin),
				Capacity: 1,
				Evicted:  func(string, int) { deleted.Store(!c.Delete("other")) },
			})
			assert.NoError(t, err, "New must accept the configuration")
			p := c.SetPinned("a", 1, time.Time{})
			c.Set("b", 1, time.Time{})
			assert.CompletesWithin(t, time.Second, func(context.Context) error {
				p.Unpin()

				return nil
			}, "a Delete inside Evicted must not wait for the lock of the Unpin")
			assert.True(t, deleted.Load(), "the Delete inside Evicted must run")
		})

		t.Run("passes nothing to Evicted for an entry in the cache", func(t *testing.T) {
			t.Parallel()
			c, _, r := newCache(t, 10, nil)
			c.Set("k", 1, time.Time{})
			p, _ := c.Pin("k")
			p.Unpin()
			assert.Empty(t, r.got(), "Evicted must not receive an entry that is still in the cache")
		})

		t.Run("does nothing for the zero Pinned", func(t *testing.T) {
			t.Parallel()
			var p cache.Pinned[string, int]
			assert.NotPanics(t, p.Unpin, "Unpin of the zero Pinned must do nothing")
		})
	})
}

// TestPinnedAllocs checks the allocation contract of Unpin. MaxAllocs
// counts the allocations of the whole process, so the test does not run in
// parallel.
func TestPinnedAllocs(t *testing.T) {
	t.Run("Unpin", func(t *testing.T) {
		// Each call unpins the entry that keeps a Cache of capacity 1 past
		// its capacity, so the Unpin evicts one entry.
		type overflow struct {
			c *cache.Cache[string, int]
			p cache.Pinned[string, int]
		}
		var left int
		expect.MaxAllocsWithSetup(t, func() overflow {
			c, err := cache.New(cache.Config[string, int]{Capacity: 1})
			assert.NoError(t, err, "New must accept the configuration")
			p := c.SetPinned("a", 1, time.Time{})
			c.Set("b", 1, time.Time{})

			return overflow{c: c, p: p}
		}, func(o overflow) {
			o.p.Unpin()
			left = o.c.Len()
		}, 0, "an Unpin that evicts one entry must not allocate")
		assert.Equal(t, left, 1, "the test must measure an Unpin that evicts")
	})
}
