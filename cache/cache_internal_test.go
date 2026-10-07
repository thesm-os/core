// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package cache

import (
	"testing"

	"go.dokimi.dev/assert/expect"
)

// TestCacheQueues is in package cache because the queues of a Cache are
// unexported. It checks that the counts of the queues agree with Len and
// Cost, which no method of the Cache reports apart.
func TestCacheQueues(t *testing.T) {
	t.Parallel()

	t.Run("detach", func(t *testing.T) {
		t.Parallel()

		t.Run("subtracts an entry of the small queue from the small queue", func(t *testing.T) {
			t.Parallel()
			c := policyCache(t, 20)
			es := attachNew(c, "a", "b", "c")
			toMain(c, es[2])
			c.mu.Lock()
			c.detach(es[0])
			c.mu.Unlock()
			expect.Equal(t, c.small.count, 1, "the small queue must lose the entry")
			expect.Equal(t, c.small.cost, int64(1), "the small queue must lose the cost of the entry")
			expect.Equal(t, c.main.count, 1, "the main queue must keep its entry")
			expect.Equal(t, c.main.cost, int64(1), "the main queue must keep its cost")
			expect.Equal(t, c.Len(), 2, "Len must count the entries of both queues")
			expect.Equal(t, c.Cost(), int64(2), "Cost must sum the costs of both queues")
		})

		t.Run("subtracts an entry of the main queue from the main queue", func(t *testing.T) {
			t.Parallel()
			c := policyCache(t, 20)
			es := attachNew(c, "a", "b", "c")
			toMain(c, es[1], es[2])
			c.mu.Lock()
			c.detach(es[1])
			c.mu.Unlock()
			expect.Equal(t, c.small.count, 1, "the small queue must keep its entry")
			expect.Equal(t, c.small.cost, int64(1), "the small queue must keep its cost")
			expect.Equal(t, c.main.count, 1, "the main queue must lose the entry")
			expect.Equal(t, c.main.cost, int64(1), "the main queue must lose the cost of the entry")
			expect.Equal(t, mainKeys(c), []string{"c"}, "the main queue must keep its other entry")
		})
	})
}
