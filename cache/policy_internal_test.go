// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package cache

import (
	"strconv"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/clock/fake"
)

// origin is the time of the fake clock of every internal case.
var origin = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// TestQueue is in package cache because queue is unexported.
func TestQueue(t *testing.T) {
	t.Parallel()

	t.Run("push", func(t *testing.T) {
		t.Parallel()

		t.Run("links each entry to the older one", func(t *testing.T) {
			t.Parallel()
			q := queue[string, int]{id: inMain}
			a, b := &entry[string, int]{key: "a"}, &entry[string, int]{key: "b"}
			q.push(a)
			q.push(b)
			expect.Equal(t, q.tail, a, "the first entry must be the tail", expect.ByIdentity())
			expect.Equal(t, q.head, b, "the second entry must be the head", expect.ByIdentity())
			expect.Equal(t, b.prev, a, "the second entry must link to the first", expect.ByIdentity())
			expect.Equal(t, a.next, b, "the first entry must link to the second", expect.ByIdentity())
		})

		t.Run("adds the cost of each entry", func(t *testing.T) {
			t.Parallel()
			q := queue[string, int]{id: inMain}
			q.push(&entry[string, int]{key: "a", cost: 2})
			q.push(&entry[string, int]{key: "b", cost: 3})
			expect.Equal(t, q.cost, int64(5), "the queue must sum the costs")
			expect.Equal(t, q.count, 2, "the queue must count the entries")
		})

		t.Run("writes the id of the queue into the entry", func(t *testing.T) {
			t.Parallel()
			q := queue[string, int]{id: inMain}
			a := &entry[string, int]{key: "a"}
			q.push(a)
			assert.Equal(t, a.queue, inMain, "push must write the queue's id into the entry")
		})
	})

	t.Run("remove", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name     string
			remove   int
			wantKeys []string
		}{
			{name: "takes out the tail", remove: 0, wantKeys: []string{"b", "c"}},
			{name: "takes out an entry in the middle", remove: 1, wantKeys: []string{"a", "c"}},
			{name: "takes out the head", remove: 2, wantKeys: []string{"a", "b"}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				c := policyCache(t, 10)
				es := attachNew(c, "a", "b", "c")
				toMain(c, es...)
				c.main.remove(es[tt.remove])
				expect.Equal(t, mainKeys(c), tt.wantKeys, "the other entries must remain linked")
				expect.Equal(t, c.main.cost, int64(2), "remove must subtract the cost")
				expect.Equal(t, c.main.count, 2, "remove must subtract the entry")
				expect.Nil(t, es[tt.remove].prev, "a removed entry must have no link to an older one")
				expect.Nil(t, es[tt.remove].next, "a removed entry must have no link to a newer one")
			})
		}

		t.Run("empties a queue of one entry", func(t *testing.T) {
			t.Parallel()
			q := queue[string, int]{}
			a := &entry[string, int]{key: "a", cost: 1}
			q.push(a)
			q.remove(a)
			expect.Nil(t, q.head, "an empty queue must have no head")
			expect.Nil(t, q.tail, "an empty queue must have no tail")
		})
	})
}

// TestGhost is in package cache because ghost is unexported.
func TestGhost(t *testing.T) {
	t.Parallel()

	t.Run("add", func(t *testing.T) {
		t.Parallel()

		t.Run("remembers a hash", func(t *testing.T) {
			t.Parallel()
			g := &ghost{count: map[uint64]int32{}, capacity: 10}
			g.add(7, 1)
			expect.True(t, g.contains(7), "the ghost must remember the hash")
			expect.False(t, g.contains(8), "the ghost must not remember another hash")
		})

		t.Run("remembers a hash whose cost equals the capacity", func(t *testing.T) {
			t.Parallel()
			g := &ghost{count: map[uint64]int32{}, capacity: 10}
			g.add(7, 10)
			assert.True(t, g.contains(7), "a cost of the capacity must fit")
		})

		t.Run("forgets nothing for a hash whose cost exceeds the capacity", func(t *testing.T) {
			t.Parallel()
			g := &ghost{count: map[uint64]int32{}, capacity: 10}
			g.add(1, 5)
			g.add(7, 11)
			expect.False(t, g.contains(7), "a cost above the capacity must not be remembered")
			expect.True(t, g.contains(1), "the ghost must keep what it remembered")
		})

		t.Run("forgets the oldest hashes until the costs fit the capacity", func(t *testing.T) {
			t.Parallel()
			g := &ghost{count: map[uint64]int32{}, capacity: 10}
			g.add(1, 4)
			g.add(2, 4)
			g.add(3, 4)
			expect.False(t, g.contains(1), "the oldest hash must be forgotten")
			expect.True(t, g.contains(2), "the second hash must be remembered")
			expect.True(t, g.contains(3), "the newest hash must be remembered")
			expect.Equal(t, g.cost, int64(8), "the costs must fit the capacity")
		})

		t.Run("keeps the order of the hashes when the ring grows past its end", func(t *testing.T) {
			t.Parallel()
			g := &ghost{count: map[uint64]int32{}, capacity: 20}
			for h := range uint64(40) {
				g.add(h, 1)
			}
			got, want := make([]bool, 0, 40), make([]bool, 0, 40)
			for h := range uint64(40) {
				got, want = append(got, g.contains(h)), append(want, h >= 20)
			}
			assert.Equal(t, got, want, "the ghost must remember exactly the newest 20 hashes")
		})
	})

	t.Run("forget", func(t *testing.T) {
		t.Parallel()

		t.Run("keeps a hash that the ghost remembers twice", func(t *testing.T) {
			t.Parallel()
			g := &ghost{count: map[uint64]int32{}, capacity: 2}
			g.add(7, 1)
			g.add(7, 1)
			g.add(8, 1)
			assert.True(t, g.contains(7), "the second memory of the hash must remain")
		})

		t.Run("forgets a hash with its last memory", func(t *testing.T) {
			t.Parallel()
			g := &ghost{count: map[uint64]int32{}, capacity: 2}
			g.add(7, 1)
			g.add(7, 1)
			g.add(8, 1)
			g.add(9, 1)
			assert.False(t, g.contains(7), "the hash must be forgotten with its last memory")
		})

		t.Run("drops a forgotten hash from the counts", func(t *testing.T) {
			t.Parallel()
			g := &ghost{count: map[uint64]int32{}, capacity: 2}
			g.add(7, 1)
			g.add(8, 1)
			g.add(9, 1)
			assert.Equal(t, g.count, map[uint64]int32{8: 1, 9: 1}, "the counts must contain only the remembered hashes")
		})
	})
}

// TestExamine is in package cache because examine and evict are
// unexported. Each case builds the queues by hand and examines them.
func TestExamine(t *testing.T) {
	t.Parallel()

	t.Run("examine", func(t *testing.T) {
		t.Parallel()

		t.Run("examines the small queue while it costs a tenth of the capacity", func(t *testing.T) {
			t.Parallel()
			c := policyCache(t, 10)
			es := attachNew(c, "a", "b")
			toMain(c, es[1])
			var gone victims[string, int]
			c.examine(origin, nil, &gone, false)
			assert.Equal(t, keysOf(&gone), []string{"a"}, "the tail of the small queue must leave")
		})

		t.Run("remembers the key of an entry that it removes from the small queue", func(t *testing.T) {
			t.Parallel()
			c := policyCache(t, 10)
			es := attachNew(c, "a")
			var gone victims[string, int]
			c.examine(origin, nil, &gone, false)
			assert.True(t, c.ghost.contains(es[0].hash), "the ghost must remember a key of the small queue")
		})

		t.Run("examines the main queue while the small queue costs less", func(t *testing.T) {
			t.Parallel()
			c := policyCache(t, 20)
			es := attachNew(c, "a", "b")
			toMain(c, es[1])
			var gone victims[string, int]
			c.examine(origin, nil, &gone, false)
			assert.Equal(t, keysOf(&gone), []string{"b"}, "the tail of the main queue must leave")
		})

		t.Run("remembers no key of an entry that it removes from the main queue", func(t *testing.T) {
			t.Parallel()
			c := policyCache(t, 20)
			es := attachNew(c, "a", "b")
			toMain(c, es[1])
			var gone victims[string, int]
			c.examine(origin, nil, &gone, false)
			assert.False(t, c.ghost.contains(es[1].hash), "the ghost must not remember a key of the main queue")
		})

		t.Run("examines the small queue while the main queue is empty", func(t *testing.T) {
			t.Parallel()
			c := policyCache(t, 20)
			attachNew(c, "a")
			var gone victims[string, int]
			c.examine(origin, nil, &gone, false)
			assert.Equal(t, keysOf(&gone), []string{"a"}, "the small queue must be examined")
		})

		t.Run("marks an entry that it removes as having left", func(t *testing.T) {
			t.Parallel()
			c := policyCache(t, 10)
			es := attachNew(c, "a")
			var gone victims[string, int]
			c.examine(origin, nil, &gone, false)
			assert.Equal(t, es[0].state.Load(), int64(removed), "the removed entry must refuse every later pin")
		})

		t.Run("moves a hit entry of the small queue to the main queue without hits", func(t *testing.T) {
			t.Parallel()
			c := policyCache(t, 10)
			es := attachNew(c, "a")
			es[0].freq.Store(2)
			var gone victims[string, int]
			c.examine(origin, nil, &gone, false)
			expect.Equal(t, mainKeys(c), []string{"a"}, "the hit entry must move to the main queue")
			expect.Equal(t, es[0].freq.Load(), int32(0), "the entry must start the main queue without hits")
			expect.Empty(t, keysOf(&gone), "nothing may leave")
		})

		t.Run("moves a hit entry of the main queue to its head with a hit less", func(t *testing.T) {
			t.Parallel()
			c := policyCache(t, 20)
			es := attachNew(c, "a", "b")
			toMain(c, es...)
			es[0].freq.Store(2)
			var gone victims[string, int]
			c.examine(origin, nil, &gone, false)
			expect.Equal(t, mainKeys(c), []string{"b", "a"}, "the hit entry must move to the head")
			expect.Equal(t, es[0].freq.Load(), int32(1), "the entry must lose one hit")
			expect.Empty(t, keysOf(&gone), "nothing may leave")
		})

		t.Run("moves a pinned entry of the small queue to the main queue without hits", func(t *testing.T) {
			t.Parallel()
			c := policyCache(t, 10)
			es := attachNew(c, "a")
			es[0].pin()
			es[0].freq.Store(3)
			var gone victims[string, int]
			c.examine(origin, nil, &gone, true)
			expect.Equal(t, mainKeys(c), []string{"a"}, "the pinned entry must move to the main queue")
			expect.Equal(t, es[0].freq.Load(), int32(0), "the entry must start the main queue without hits")
			expect.Empty(t, keysOf(&gone), "the pinned entry must not leave")
		})

		t.Run("takes one hit from a pinned entry of the main queue when it forces", func(t *testing.T) {
			t.Parallel()
			c := policyCache(t, 20)
			es := attachNew(c, "a", "b")
			toMain(c, es...)
			es[0].pin()
			es[0].freq.Store(3)
			var gone victims[string, int]
			c.examine(origin, nil, &gone, true)
			expect.Equal(t, mainKeys(c), []string{"b", "a"}, "the pinned entry must move to the head")
			expect.Equal(t, es[0].freq.Load(), int32(2), "the entry must lose one hit")
		})

		t.Run("takes no hit from a pinned entry of the main queue without hits", func(t *testing.T) {
			t.Parallel()
			c := policyCache(t, 20)
			es := attachNew(c, "a", "b")
			toMain(c, es...)
			es[0].pin()
			var gone victims[string, int]
			c.examine(origin, nil, &gone, false)
			expect.Equal(t, mainKeys(c), []string{"b", "a"}, "the pinned entry must move to the head")
			expect.Equal(t, es[0].freq.Load(), int32(0), "an entry without hits must not lose one")
		})

		t.Run("removes a hit entry when it forces", func(t *testing.T) {
			t.Parallel()
			c := policyCache(t, 20)
			es := attachNew(c, "a")
			toMain(c, es...)
			es[0].freq.Store(3)
			var gone victims[string, int]
			c.examine(origin, nil, &gone, true)
			assert.Equal(t, keysOf(&gone), []string{"a"}, "a forced examination must remove a hit entry")
		})

		t.Run("removes an expired entry that was hit", func(t *testing.T) {
			t.Parallel()
			c := policyCache(t, 10)
			es := attachNew(c, "a")
			es[0].expires = origin
			es[0].freq.Store(3)
			var gone victims[string, int]
			c.examine(origin, nil, &gone, false)
			assert.Equal(t, keysOf(&gone), []string{"a"}, "an expired entry must leave whatever its hits")
		})

		t.Run("keeps the entry that the Set added when it forces", func(t *testing.T) {
			t.Parallel()
			c := policyCache(t, 10)
			es := attachNew(c, "a")
			var gone victims[string, int]
			c.examine(origin, es[0], &gone, true)
			expect.Equal(t, mainKeys(c), []string{"a"}, "the new entry must move to the main queue")
			expect.Empty(t, keysOf(&gone), "the new entry must not leave")
		})

		t.Run("keeps a pinned entry when it forces", func(t *testing.T) {
			t.Parallel()
			c := policyCache(t, 10)
			es := attachNew(c, "a")
			es[0].pin()
			var gone victims[string, int]
			c.examine(origin, nil, &gone, true)
			expect.Equal(t, mainKeys(c), []string{"a"}, "the pinned entry must move to the main queue")
			expect.Empty(t, keysOf(&gone), "the pinned entry must not leave")
		})
	})

	t.Run("evict", func(t *testing.T) {
		t.Parallel()

		t.Run("takes the hits of the main queue before it removes an entry", func(t *testing.T) {
			t.Parallel()
			c := policyCache(t, 9)
			keys := make([]string, 10)
			for i := range keys {
				keys[i] = "e" + strconv.Itoa(i)
			}
			es := attachNew(c, keys...)
			toMain(c, es...)
			es[0].freq.Store(3)
			for _, e := range es[1:] {
				e.freq.Store(2)
			}
			var gone victims[string, int]
			c.evict(origin, nil, &gone)
			assert.Equal(t, keysOf(&gone), []string{"e1"},
				"the first entry to run out of hits after two rounds and a step must leave")
		})

		t.Run("removes the entries that the costs exceed and no more", func(t *testing.T) {
			t.Parallel()
			c := policyCache(t, 2)
			attachNew(c, "a", "b", "c", "d")
			var gone victims[string, int]
			c.evict(origin, nil, &gone)
			assert.Equal(t, keysOf(&gone), []string{"a", "b"}, "evict must stop at the capacity")
		})
	})
}

// policyCache returns an empty Cache of capacity over a fake clock at
// origin, without Cost and Evicted, and fails tb when New refuses it.
func policyCache(tb assert.TB, capacity int64) *Cache[string, int] {
	tb.Helper()
	c, err := New(Config[string, int]{Clock: fake.New(origin), Capacity: capacity})
	assert.NoError(tb, err, "New must accept the configuration")

	return c
}

// attachNew adds an entry of cost 1 for each key to c, in order, through
// attach, which evicts nothing, and returns the entries.
func attachNew(c *Cache[string, int], keys ...string) []*entry[string, int] {
	es := make([]*entry[string, int], len(keys))
	for i, k := range keys {
		es[i] = &entry[string, int]{key: k, hash: uint64(i), cost: 1}
		c.attach(es[i])
	}

	return es
}

// toMain moves each entry from the small queue to the head of the main
// queue, in order, so the first one ends at the tail.
func toMain(c *Cache[string, int], es ...*entry[string, int]) {
	for _, e := range es {
		c.small.remove(e)
		c.main.push(e)
	}
}

// keysOf returns the keys of the entries of gone, in the order of add, and
// nil when gone has none.
func keysOf(gone *victims[string, int]) []string {
	if gone.n == 0 {
		return nil
	}
	keys := make([]string, 0, gone.n+len(gone.more))
	for _, e := range gone.few[:gone.n] {
		keys = append(keys, e.key)
	}
	for _, e := range gone.more {
		keys = append(keys, e.key)
	}

	return keys
}

// mainKeys returns the keys of the main queue from its tail to its head.
func mainKeys(c *Cache[string, int]) []string {
	var keys []string
	for e := c.main.tail; e != nil; e = e.next {
		keys = append(keys, e.key)
	}

	return keys
}
