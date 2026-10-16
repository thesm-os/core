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
	})
}
