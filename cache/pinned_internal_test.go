// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package cache

import (
	"context"
	"testing"
	"time"

	"go.dokimi.dev/assert"
)

// TestPinnedLock is in package cache because the mutex of a Cache is
// unexported. It locks the mutex while an Unpin runs, so an Unpin that takes
// the lock does not complete.
func TestPinnedLock(t *testing.T) {
	t.Parallel()

	t.Run("Unpin", func(t *testing.T) {
		t.Parallel()

		t.Run("takes no lock while the cost equals the capacity", func(t *testing.T) {
			t.Parallel()
			c, err := New(Config[string, int]{Capacity: 2})
			assert.NoError(t, err, "New must accept the configuration")
			c.Set("a", 1, time.Time{})
			p := c.SetPinned("b", 1, time.Time{})
			c.mu.Lock()
			defer c.mu.Unlock()
			assert.CompletesWithin(t, time.Second, func(context.Context) error {
				p.Unpin()

				return nil
			}, "an Unpin at the capacity must not wait for the lock")
		})

		t.Run("takes no lock for an entry that the evictor set aside at the capacity", func(t *testing.T) {
			t.Parallel()
			c, err := New(Config[string, int]{Capacity: 2})
			assert.NoError(t, err, "New must accept the configuration")
			p := c.SetPinned("a", 1, time.Time{})
			c.SetPinned("b", 1, time.Time{})
			c.Set("c", 1, time.Time{})
			c.Delete("c")
			assert.Equal(t, p.e.queue, setAside, "the test must measure an entry that the evictor set aside")
			c.mu.Lock()
			defer c.mu.Unlock()
			assert.CompletesWithin(t, time.Second, func(context.Context) error {
				p.Unpin()

				return nil
			}, "the last Unpin of an entry that the evictor set aside must not wait for the lock")
		})
	})
}

// TestPinnedAside is in package cache because the queue of an entry is
// unexported. It checks which Unpin hands an entry that the evictor set
// aside back to the cache.
func TestPinnedAside(t *testing.T) {
	t.Parallel()

	t.Run("Unpin", func(t *testing.T) {
		t.Parallel()

		t.Run("keeps an entry aside at an Unpin that leaves a pin", func(t *testing.T) {
			t.Parallel()
			c, err := New(Config[string, int]{Capacity: 2})
			assert.NoError(t, err, "New must accept the configuration")
			first := c.SetPinned("a", 1, time.Time{})
			second, _ := c.Pin("a")
			defer second.Unpin()
			c.Set("b", 1, time.Time{})
			c.Set("c", 1, time.Time{})
			assert.Equal(t, first.e.queue, setAside, "the test must measure an entry that the evictor set aside")
			first.Unpin()
			c.Set("d", 1, time.Time{})
			assert.Equal(t, first.e.queue, setAside, "an Unpin that leaves a pin must not hand the entry back")
		})
	})
}
