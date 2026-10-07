// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package cache

import (
	"strconv"
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/clock/fake"
)

// origin is the time of the fake clock of every internal case.
var origin = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// policyCache returns an empty Cache of capacity over a fake clock at
// origin, without Cost and Evicted, and fails tb when New refuses it.
func policyCache(tb testing.TB, capacity int64) *Cache[string, int] {
	tb.Helper()

	c, err := New(Config[string, int]{Clock: fake.New(origin), Capacity: capacity})
	testkit.NoError(tb, err, "New must accept the configuration")

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

// keysOf returns the keys of the entries of gone, in the order of add.
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

// TestQueue is in package cache because queue is unexported.
func TestQueue(t *testing.T) {
	t.Parallel()

	t.Run("push", func(t *testing.T) {
		t.Parallel()

		t.Run("links each entry to the older one and counts its cost", func(t *testing.T) {
			t.Parallel()
			q := queue[string, int]{id: inMain}
			a := &entry[string, int]{key: "a", cost: 2}
			b := &entry[string, int]{key: "b", cost: 3}
			q.push(a)
			q.push(b)

			testkit.True(t, q.tail == a && q.head == b, "the first entry must be the tail")
			testkit.True(t, b.prev == a && a.next == b, "the entries must link to each other")
			testkit.Equal(t, q.cost, int64(5), "the queue must count the costs")
			testkit.Equal(t, q.count, 2, "the queue must count the entries")
			testkit.Equal(t, b.queue, inMain, "push must write the queue's id into the entry")
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
				testkit.Equal(t, mainKeys(c), tt.wantKeys, "the other entries must stay linked")
				testkit.Equal(t, c.main.cost, int64(2), "remove must subtract the cost")
				testkit.Equal(t, c.main.count, 2, "remove must subtract the entry")
				testkit.True(t, es[tt.remove].prev == nil && es[tt.remove].next == nil,
					"a removed entry must have no links")
			})
		}

		t.Run("empties a queue of one entry", func(t *testing.T) {
			t.Parallel()
			q := queue[string, int]{}
			a := &entry[string, int]{key: "a", cost: 1}
			q.push(a)
			q.remove(a)
			testkit.True(t, q.head == nil && q.tail == nil, "an empty queue must have no head and no tail")
		})
	})
}

// TestGhost is in package cache because ghost is unexported.
func TestGhost(t *testing.T) {
	t.Parallel()

	newGhost := func(capacity int64) *ghost {
		return &ghost{count: map[uint64]int32{}, capacity: capacity}
	}

	t.Run("add", func(t *testing.T) {
		t.Parallel()

		t.Run("remembers a hash", func(t *testing.T) {
			t.Parallel()
			g := newGhost(10)
			g.add(7, 1)
			testkit.True(t, g.contains(7), "the ghost must remember the hash")
			testkit.False(t, g.contains(8), "the ghost must not remember another hash")
		})

		t.Run("remembers a hash whose cost equals the capacity", func(t *testing.T) {
			t.Parallel()
			g := newGhost(10)
			g.add(7, 10)
			testkit.True(t, g.contains(7), "a cost of the capacity must fit")
		})

		t.Run("forgets nothing for a hash whose cost exceeds the capacity", func(t *testing.T) {
			t.Parallel()
			g := newGhost(10)
			g.add(1, 5)
			g.add(7, 11)
			testkit.False(t, g.contains(7), "a cost above the capacity must not be remembered")
			testkit.True(t, g.contains(1), "the ghost must keep what it remembered")
		})

		t.Run("forgets the oldest hashes until the costs fit the capacity", func(t *testing.T) {
			t.Parallel()
			g := newGhost(10)
			g.add(1, 4)
			g.add(2, 4)
			g.add(3, 4)

			testkit.False(t, g.contains(1), "the oldest hash must be forgotten")
			testkit.True(t, g.contains(2) && g.contains(3), "the newer hashes must be remembered")
			testkit.Equal(t, g.cost, int64(8), "the costs must fit the capacity")
		})

		t.Run("keeps the order of the hashes when the ring grows past its end", func(t *testing.T) {
			t.Parallel()
			g := newGhost(20)
			for h := range uint64(20) {
				g.add(h, 1)
			}

			for h := uint64(20); h < 40; h++ {
				g.add(h, 1)
			}

			for h := range uint64(40) {
				testkit.Equal(t, g.contains(h), h >= 20, "the ghost must remember exactly the newest 20 hashes")
			}
		})
	})

	t.Run("forget", func(t *testing.T) {
		t.Parallel()

		t.Run("keeps a hash that the ghost remembers twice", func(t *testing.T) {
			t.Parallel()
			g := newGhost(2)
			g.add(7, 1)
			g.add(7, 1)
			g.add(8, 1)

			testkit.True(t, g.contains(7), "the second memory of the hash must remain")

			g.add(9, 1)
			testkit.False(t, g.contains(7), "the hash must be forgotten with its last memory")
		})
	})
}

// TestExamine is in package cache because examine and evict are
// unexported. Each case builds the queues by hand and examines them.
func TestExamine(t *testing.T) {
	t.Parallel()

	t.Run("examine", func(t *testing.T) {
		t.Parallel()

		t.Run("examines the small queue while it holds a tenth of the capacity", func(t *testing.T) {
			t.Parallel()
			c := policyCache(t, 10)
			es := attachNew(c, "a", "b")
			toMain(c, es[1])

			var gone victims[string, int]
			c.examine(origin, nil, &gone, false)
			testkit.Equal(t, keysOf(&gone), []string{"a"}, "the tail of the small queue must leave")
			testkit.True(t, c.ghost.contains(es[0].hash), "the ghost must remember a key of the small queue")
		})

		t.Run("examines the main queue while the small queue holds less", func(t *testing.T) {
			t.Parallel()
			c := policyCache(t, 20)
			es := attachNew(c, "a", "b")
			toMain(c, es[1])

			var gone victims[string, int]
			c.examine(origin, nil, &gone, false)
			testkit.Equal(t, keysOf(&gone), []string{"b"}, "the tail of the main queue must leave")
			testkit.False(t, c.ghost.contains(es[1].hash), "the ghost must not remember a key of the main queue")
		})

		t.Run("examines the small queue while the main queue is empty", func(t *testing.T) {
			t.Parallel()
			c := policyCache(t, 20)
			attachNew(c, "a")

			var gone victims[string, int]
			c.examine(origin, nil, &gone, false)
			testkit.Equal(t, keysOf(&gone), []string{"a"}, "the small queue must be examined")
		})

		t.Run("moves a hit entry of the small queue to the main queue without hits", func(t *testing.T) {
			t.Parallel()
			c := policyCache(t, 10)
			es := attachNew(c, "a")
			es[0].freq.Store(2)

			var gone victims[string, int]
			c.examine(origin, nil, &gone, false)
			testkit.Equal(t, mainKeys(c), []string{"a"}, "the hit entry must move to the main queue")
			testkit.Equal(t, es[0].freq.Load(), int32(0), "the entry must start the main queue without hits")
			testkit.Equal(t, keysOf(&gone), []string(nil), "nothing may leave")
		})

		t.Run("moves a hit entry of the main queue to its head with a hit less", func(t *testing.T) {
			t.Parallel()
			c := policyCache(t, 20)
			es := attachNew(c, "a", "b")
			toMain(c, es...)
			es[0].freq.Store(2)

			var gone victims[string, int]
			c.examine(origin, nil, &gone, false)
			testkit.Equal(t, mainKeys(c), []string{"b", "a"}, "the hit entry must move to the head")
			testkit.Equal(t, es[0].freq.Load(), int32(1), "the entry must lose one hit")
			testkit.Equal(t, keysOf(&gone), []string(nil), "nothing may leave")
		})

		t.Run("removes a hit entry when it forces", func(t *testing.T) {
			t.Parallel()
			c := policyCache(t, 20)
			es := attachNew(c, "a")
			toMain(c, es...)
			es[0].freq.Store(3)

			var gone victims[string, int]
			c.examine(origin, nil, &gone, true)
			testkit.Equal(t, keysOf(&gone), []string{"a"}, "a forced examination must remove a hit entry")
		})

		t.Run("removes an expired entry that was hit", func(t *testing.T) {
			t.Parallel()
			c := policyCache(t, 10)
			es := attachNew(c, "a")
			es[0].expires = origin
			es[0].freq.Store(3)

			var gone victims[string, int]
			c.examine(origin, nil, &gone, false)
			testkit.Equal(t, keysOf(&gone), []string{"a"}, "an expired entry must leave whatever its hits")
		})

		t.Run("keeps the entry that the Set added when it forces", func(t *testing.T) {
			t.Parallel()
			c := policyCache(t, 10)
			es := attachNew(c, "a")

			var gone victims[string, int]
			c.examine(origin, es[0], &gone, true)
			testkit.Equal(t, mainKeys(c), []string{"a"}, "the new entry must move to the main queue")
			testkit.Equal(t, keysOf(&gone), []string(nil), "the new entry must not leave")
		})

		t.Run("keeps a pinned entry when it forces", func(t *testing.T) {
			t.Parallel()
			c := policyCache(t, 10)
			es := attachNew(c, "a")
			es[0].pin()

			var gone victims[string, int]
			c.examine(origin, nil, &gone, true)
			testkit.Equal(t, mainKeys(c), []string{"a"}, "the pinned entry must move to the main queue")
			testkit.Equal(t, keysOf(&gone), []string(nil), "the pinned entry must not leave")
		})

		t.Run("resets the hits of a pinned entry of the small queue when it forces", func(t *testing.T) {
			t.Parallel()
			c := policyCache(t, 10)
			es := attachNew(c, "a")
			es[0].pin()
			es[0].freq.Store(3)

			var gone victims[string, int]
			c.examine(origin, nil, &gone, true)
			testkit.Equal(t, es[0].freq.Load(), int32(0), "the entry must start the main queue without hits")
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
			testkit.Equal(t, es[0].freq.Load(), int32(2), "the entry must lose one hit")
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
			testkit.Equal(t, keysOf(&gone), []string{"e1"},
				"the first entry to run out of hits after two rounds and a step must leave")
		})

		t.Run("removes the entries that the costs exceed and no more", func(t *testing.T) {
			t.Parallel()
			c := policyCache(t, 2)
			attachNew(c, "a", "b", "c", "d")

			var gone victims[string, int]
			c.evict(origin, nil, &gone)
			testkit.Equal(t, keysOf(&gone), []string{"a", "b"}, "evict must stop at the capacity")
		})
	})
}
