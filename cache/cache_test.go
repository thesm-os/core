// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package cache_test

import (
	"context"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"

	"go.thesmos.sh/core/cache"
	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/errs"
)

// benchCapacity is the capacity of the caches of the allocation checks
// and the benchmarks.
const benchCapacity = 1_000

// The shape of the concurrent workload of a Cache of ints.
const (
	// clients is the number of clients of a concurrent section.
	clients = 4

	// workloadKeys is the number of keys that the calls choose from, twice
	// the capacity, so the cache evicts.
	workloadKeys = 16

	// workloadCapacity is the capacity of the cache of the workload.
	workloadCapacity = 8

	// absent is the value that a call records for a miss. Every value
	// that the workload stores is at least 0.
	absent = -1
)

// The operations of the concurrent history of a Cache.
const (
	opSet    = "set"
	opGet    = "get"
	opPin    = "pin"
	opDelete = "delete"
)

// origin is the time of the fake clock of every case.
var origin = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// keyValues is the state of the spec of a Cache of the workload: the value
// of each key, and absent for a key without an entry.
type keyValues [workloadKeys]int

// recorder records the keys of the entries that a Cache passes to
// Evicted, in the order of the calls. It is safe for concurrent use, so a
// Cache can call it from any goroutine.
type recorder struct {
	mu   sync.Mutex
	keys []string
}

// evicted records k. It is the Evicted of the caches that newCache
// returns.
func (r *recorder) evicted(k string, _ int) {
	r.mu.Lock()
	r.keys = append(r.keys, k)
	r.mu.Unlock()
}

// got returns a copy of the recorded keys, nil when there are none.
func (r *recorder) got() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.Clone(r.keys)
}

// countingClock is a fake clock that counts the calls of Time, and calls
// onTime, when it is set, before each call returns.
type countingClock struct {
	*fake.Clock

	onTime func()
	reads  atomic.Int64
}

// Time counts the call, calls onTime, and returns the time of the fake
// clock.
func (c *countingClock) Time() time.Time {
	c.reads.Add(1)
	if c.onTime != nil {
		c.onTime()
	}

	return c.Clock.Time()
}

func TestCache(t *testing.T) {
	t.Parallel()

	t.Run("New", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give cache.Config[string, int]
		}{
			{name: "returns ErrConfig for a nil Clock", give: cache.Config[string, int]{Capacity: 1}},
			{
				name: "returns ErrConfig for a Capacity of zero",
				give: cache.Config[string, int]{Clock: fake.New(origin)},
			},
			{
				name: "returns ErrConfig for a negative Capacity",
				give: cache.Config[string, int]{Clock: fake.New(origin), Capacity: -1},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				c, err := cache.New(tt.give)
				assert.ErrorIs(t, err, cache.ErrConfig, "New must refuse the configuration")
				assert.Equal(t, errs.Classify(err), errs.Invalid, "ErrConfig must classify as Invalid")
				assert.Nil(t, c, "a refused configuration must return no Cache")
			})
		}

		t.Run("returns an empty Cache for a Capacity of 1", func(t *testing.T) {
			t.Parallel()
			c, _, _ := newCache(t, 1, nil)
			expect.Equal(t, c.Len(), 0, "a new Cache must contain no entry")
			expect.Equal(t, c.Cost(), int64(0), "a new Cache must cost nothing")
			_, ok := c.Get("k")
			expect.False(t, ok, "a new Cache must miss every key")
		})
	})

	t.Run("Get", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the value that Set stored", func(t *testing.T) {
			t.Parallel()
			c, _, _ := newCache(t, 10, nil)
			c.Set("k", 7, time.Time{})
			v, ok := c.Get("k")
			assert.True(t, ok, "Get must find the stored key")
			assert.Equal(t, v, 7, "Get must return the stored value")
		})

		t.Run("reports false for a key that Set never stored", func(t *testing.T) {
			t.Parallel()
			c, _, _ := newCache(t, 10, nil)
			c.Set("k", 7, time.Time{})
			v, ok := c.Get("other")
			expect.False(t, ok, "Get must miss a key that was never stored")
			expect.Equal(t, v, 0, "a miss must return the zero value")
		})

		t.Run("returns the value one nanosecond before its expiry time", func(t *testing.T) {
			t.Parallel()
			c, clk, _ := newCache(t, 10, nil)
			c.Set("k", 7, origin.Add(time.Second))
			clk.Advance(time.Second - time.Nanosecond)
			v, ok := c.Get("k")
			assert.True(t, ok, "an entry before its expiry time must be a hit")
			assert.Equal(t, v, 7, "Get must return the stored value")
		})

		t.Run("reports false at the expiry time", func(t *testing.T) {
			t.Parallel()
			c, clk, _ := newCache(t, 10, nil)
			c.Set("k", 7, origin.Add(time.Second))
			clk.Advance(time.Second)
			_, ok := c.Get("k")
			assert.False(t, ok, "an entry at its expiry time must be a miss")
		})

		t.Run("returns the value of an entry without an expiry time at any time", func(t *testing.T) {
			t.Parallel()
			c, clk, _ := newCache(t, 10, nil)
			c.Set("k", 7, time.Time{})
			clk.Advance(1000 * time.Hour)
			_, ok := c.Get("k")
			assert.True(t, ok, "the zero expiry time must never expire")
		})

		t.Run("reads no clock for an entry without an expiry time", func(t *testing.T) {
			t.Parallel()
			clk := &countingClock{Clock: fake.New(origin)}
			c, err := cache.New(cache.Config[string, int]{Clock: clk, Capacity: 10})
			assert.NoError(t, err, "New must accept the configuration")
			c.Set("k", 7, time.Time{})
			var ok bool
			assert.Pure(t, clk.reads.Load, func() { _, ok = c.Get("k") }, "Get must not read the clock")
			assert.True(t, ok, "the test must measure a hit")
		})

		t.Run("removes an expired entry", func(t *testing.T) {
			t.Parallel()
			c, clk, _ := newCache(t, 10, nil)
			c.Set("k", 7, origin.Add(time.Second))
			c.Set("other", 1, time.Time{})
			clk.Advance(time.Second)
			c.Get("k")
			assert.Equal(t, c.Len(), 1, "the expired entry must leave the cache")
		})

		t.Run("passes an expired entry to Evicted once", func(t *testing.T) {
			t.Parallel()
			c, clk, r := newCache(t, 10, nil)
			c.Set("k", 7, origin.Add(time.Second))
			clk.Advance(time.Second)
			c.Get("k")
			c.Get("k")
			assert.Equal(t, r.got(), []string{"k"}, "the expired entry must reach Evicted once")
		})

		t.Run("passes a pinned expired entry to Evicted once at its Unpin", func(t *testing.T) {
			t.Parallel()
			c, clk, r := newCache(t, 10, nil)
			c.Set("k", 7, origin.Add(time.Second))
			p, _ := c.Pin("k")
			clk.Advance(time.Second)
			c.Get("k")
			assert.Empty(t, r.got(), "a pinned entry must wait for its Unpin")
			p.Unpin()
			assert.Equal(t, r.got(), []string{"k"}, "the Unpin must pass the entry to Evicted once")
		})

		t.Run("counts an expired entry once when a Delete removes it during the Get", func(t *testing.T) {
			t.Parallel()
			clk := &countingClock{Clock: fake.New(origin)}
			r := &recorder{}
			c, err := cache.New(cache.Config[string, int]{Clock: clk, Capacity: 10, Evicted: r.evicted})
			assert.NoError(t, err, "New must accept the configuration")
			c.Set("k", 7, origin.Add(time.Second))
			clk.Advance(time.Second)
			clk.onTime = func() {
				clk.onTime = nil
				c.Delete("k")
			}
			_, ok := c.Get("k")
			expect.False(t, ok, "Get must miss the expired entry")
			expect.Equal(t, c.Len(), 0, "the cache must count the entry once")
			expect.Equal(t, r.got(), []string{"k"}, "the entry must reach Evicted once")
		})

		t.Run("unlocks the cache after it removes an expired entry", func(t *testing.T) {
			t.Parallel()
			c, clk, _ := newCache(t, 10, nil)
			c.Set("k", 7, origin.Add(time.Second))
			clk.Advance(time.Second)
			c.Get("k")
			assert.CompletesWithin(t, time.Second, func(context.Context) error {
				c.Set("k", 8, time.Time{})

				return nil
			}, "a Set after the removal must not wait for the lock")
		})
	})

	t.Run("Pin", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the entry that Set stored", func(t *testing.T) {
			t.Parallel()
			c, _, _ := newCache(t, 10, nil)
			c.Set("k", 7, time.Time{})
			p, ok := c.Pin("k")
			assert.True(t, ok, "Pin must find the stored key")
			assert.Equal(t, p.Value(), 7, "the Pinned must contain the stored value")
			p.Unpin()
		})

		t.Run("reports false for a key that Set never stored", func(t *testing.T) {
			t.Parallel()
			c, _, _ := newCache(t, 10, nil)
			p, ok := c.Pin("k")
			expect.False(t, ok, "Pin must miss a key that was never stored")
			expect.Equal(t, p, cache.Pinned[string, int]{}, "a miss must return the zero Pinned")
		})

		t.Run("reports false for an expired entry", func(t *testing.T) {
			t.Parallel()
			c, clk, _ := newCache(t, 10, nil)
			c.Set("k", 7, origin.Add(time.Second))
			clk.Advance(time.Second)
			_, ok := c.Pin("k")
			assert.False(t, ok, "Pin must miss an expired entry")
		})

		t.Run("removes an expired entry", func(t *testing.T) {
			t.Parallel()
			c, clk, r := newCache(t, 10, nil)
			c.Set("k", 7, origin.Add(time.Second))
			clk.Advance(time.Second)
			c.Pin("k")
			expect.Equal(t, c.Len(), 0, "the expired entry must leave the cache")
			expect.Equal(t, r.got(), []string{"k"}, "the expired entry must reach Evicted")
		})

		t.Run("reports false for an entry that a Delete removed", func(t *testing.T) {
			t.Parallel()
			c, _, _ := newCache(t, 10, nil)
			c.Set("k", 7, time.Time{})
			c.Delete("k")
			_, ok := c.Pin("k")
			assert.False(t, ok, "Pin must miss a deleted entry")
		})
	})

	t.Run("Set", func(t *testing.T) {
		t.Parallel()

		t.Run("replaces the value of a key", func(t *testing.T) {
			t.Parallel()
			c, _, _ := newCache(t, 10, nil)
			c.Set("k", 1, time.Time{})
			c.Set("k", 2, time.Time{})
			v, _ := c.Get("k")
			expect.Equal(t, v, 2, "Get must return the second value")
			expect.Equal(t, c.Len(), 1, "a replaced key must have one entry")
		})

		t.Run("passes the replaced entry to Evicted", func(t *testing.T) {
			t.Parallel()
			c, _, r := newCache(t, 10, nil)
			c.Set("k", 1, time.Time{})
			c.Set("k", 2, time.Time{})
			assert.Equal(t, r.got(), []string{"k"}, "the replaced value must reach Evicted")
		})

		t.Run("evicts the oldest entries without hits until the cost fits the capacity", func(t *testing.T) {
			t.Parallel()
			c, _, r := newCache(t, 3, nil)
			setAll(c, "a", "b", "c", "d", "e")
			expect.Equal(t, c.Len(), 3, "the cache must keep Capacity entries of cost 1")
			expect.Equal(t, c.Cost(), int64(3), "the cost must fit the capacity")
			expect.Equal(t, r.got(), []string{"a", "b"}, "the oldest entries must leave first")
		})

		t.Run("bounds the sum of the costs that Cost returns", func(t *testing.T) {
			t.Parallel()
			c, _, r := newCache(t, 10, func(v int) int64 { return int64(v) })
			c.Set("a", 4, time.Time{})
			c.Set("b", 4, time.Time{})
			c.Set("c", 4, time.Time{})
			expect.Equal(t, c.Cost(), int64(8), "the cache must evict until the costs fit 10")
			expect.Equal(t, r.got(), []string{"a"}, "one entry of cost 4 must leave")
		})

		t.Run("counts a negative cost as zero", func(t *testing.T) {
			t.Parallel()
			c, _, _ := newCache(t, 10, func(v int) int64 { return int64(v) })
			c.Set("k", -5, time.Time{})
			expect.Equal(t, c.Cost(), int64(0), "a negative cost must count as 0")
			expect.Equal(t, c.Len(), 1, "an entry of negative cost must be stored")
		})

		t.Run("stores a value whose cost equals the capacity", func(t *testing.T) {
			t.Parallel()
			c, _, _ := newCache(t, 10, func(v int) int64 { return int64(v) })
			c.Set("k", 10, time.Time{})
			_, ok := c.Get("k")
			assert.True(t, ok, "a value of the capacity's cost must fit")
		})

		t.Run("passes a value whose cost exceeds the capacity to Evicted", func(t *testing.T) {
			t.Parallel()
			c, _, r := newCache(t, 10, func(v int) int64 { return int64(v) })
			c.Set("small", 1, time.Time{})
			c.Set("k", 1, time.Time{})
			c.Set("k", 11, time.Time{})
			_, ok := c.Get("k")
			expect.False(t, ok, "a value above the capacity must not be stored")
			expect.Equal(t, r.got(), []string{"k", "k"}, "the replaced value and the refused value must reach Evicted")
			expect.Equal(t, c.Cost(), int64(1), "the refused value must evict nothing else")
		})

		t.Run("keeps its entry past the capacity while pinned entries fill the cache", func(t *testing.T) {
			t.Parallel()
			c, _, r := newCache(t, 2, nil)
			setAll(c, "a", "b")
			a, _ := c.Pin("a")
			defer a.Unpin()
			b, _ := c.Pin("b")
			defer b.Unpin()
			c.Set("c", 1, time.Time{})
			expect.Equal(t, c.Len(), 3, "a Set into a pinned cache must keep its entry")
			expect.Empty(t, r.got(), "no pinned entry may leave")
		})

		t.Run("evicts down to the capacity once the pins are gone", func(t *testing.T) {
			t.Parallel()
			c, _, _ := newCache(t, 2, nil)
			setAll(c, "a", "b")
			a, _ := c.Pin("a")
			b, _ := c.Pin("b")
			c.Set("c", 1, time.Time{})
			a.Unpin()
			b.Unpin()
			c.Set("d", 1, time.Time{})
			assert.Equal(t, c.Len(), 2, "the next Set must evict down to the capacity")
		})

		t.Run("stores an entry whose expiry time has passed as a miss", func(t *testing.T) {
			t.Parallel()
			c, _, _ := newCache(t, 10, nil)
			c.Set("k", 7, origin.Add(-time.Second))
			_, ok := c.Get("k")
			assert.False(t, ok, "an entry stored after its expiry time must be a miss")
		})

		t.Run("passes the replaced entry to Evicted before the entries that it evicts", func(t *testing.T) {
			t.Parallel()
			c, _, r := newCache(t, 3, func(v int) int64 { return int64(v) })
			c.Set("a", 1, time.Time{})
			c.Set("b", 1, time.Time{})
			c.Set("c", 1, time.Time{})
			c.Set("c", 2, time.Time{})
			assert.Equal(t, r.got(), []string{"c", "a"}, "the replaced value must reach Evicted first")
		})

		t.Run("calls Evicted after it releases its lock", func(t *testing.T) {
			t.Parallel()
			var c *cache.Cache[string, int]
			var deleted atomic.Bool
			c, err := cache.New(cache.Config[string, int]{
				Clock:    fake.New(origin),
				Capacity: 1,
				Evicted:  func(string, int) { deleted.Store(!c.Delete("other")) },
			})
			assert.NoError(t, err, "New must accept the configuration")
			assert.CompletesWithin(t, time.Second, func(context.Context) error {
				setAll(c, "a", "b")

				return nil
			}, "a Delete inside Evicted must not wait for the lock of the Set")
			assert.True(t, deleted.Load(), "the Delete inside Evicted must run")
		})
	})

	t.Run("Delete", func(t *testing.T) {
		t.Parallel()

		t.Run("reports true for a key that the cache contains", func(t *testing.T) {
			t.Parallel()
			c, _, _ := newCache(t, 10, nil)
			c.Set("k", 7, time.Time{})
			assert.True(t, c.Delete("k"), "Delete must report the removed entry")
		})

		t.Run("removes the entry", func(t *testing.T) {
			t.Parallel()
			c, _, r := newCache(t, 10, nil)
			c.Set("k", 7, time.Time{})
			c.Delete("k")
			_, ok := c.Get("k")
			expect.False(t, ok, "a deleted key must miss")
			expect.Equal(t, c.Len(), 0, "the deleted entry must leave the cache")
			expect.Equal(t, r.got(), []string{"k"}, "the deleted entry must reach Evicted")
		})

		t.Run("reports false for a key that the cache does not contain", func(t *testing.T) {
			t.Parallel()
			c, _, r := newCache(t, 10, nil)
			expect.False(t, c.Delete("k"), "Delete of an absent key must report false")
			expect.Empty(t, r.got(), "Delete of an absent key must not call Evicted")
		})

		t.Run("reports true for an expired entry", func(t *testing.T) {
			t.Parallel()
			c, clk, r := newCache(t, 10, nil)
			c.Set("k", 7, origin.Add(time.Second))
			clk.Advance(2 * time.Second)
			expect.True(t, c.Delete("k"), "an expired entry that no Get removed must count as present")
			expect.Equal(t, r.got(), []string{"k"}, "the deleted entry must reach Evicted")
		})

		t.Run("calls no Evicted when the Config sets none", func(t *testing.T) {
			t.Parallel()
			c, err := cache.New(cache.Config[string, int]{Clock: fake.New(origin), Capacity: 10})
			assert.NoError(t, err, "New must accept the configuration")
			c.Set("k", 7, time.Time{})
			assert.NotPanics(t, func() { c.Delete("k") }, "Delete must skip a nil Evicted")
		})
	})

	t.Run("Len", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the number of entries", func(t *testing.T) {
			t.Parallel()
			c, _, _ := newCache(t, 10, func(v int) int64 { return int64(v) })
			c.Set("a", 3, time.Time{})
			c.Set("b", 4, time.Time{})
			assert.Equal(t, c.Len(), 2, "Len must count the entries, not their costs")
		})

		t.Run("excludes a pinned entry that left", func(t *testing.T) {
			t.Parallel()
			c, _, _ := newCache(t, 10, nil)
			c.Set("k", 7, time.Time{})
			p, _ := c.Pin("k")
			defer p.Unpin()
			c.Delete("k")
			assert.Equal(t, c.Len(), 0, "an entry that left must not count while pinned")
		})
	})

	t.Run("Cost", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the sum of the costs of the entries", func(t *testing.T) {
			t.Parallel()
			c, _, _ := newCache(t, 10, func(v int) int64 { return int64(v) })
			c.Set("a", 3, time.Time{})
			c.Set("b", 4, time.Time{})
			assert.Equal(t, c.Cost(), int64(7), "Cost must sum the costs")
			c.Delete("a")
			assert.Equal(t, c.Cost(), int64(4), "Cost must drop by the cost of a removed entry")
		})
	})

	t.Run("orders concurrent calls as one sequence of calls", func(t *testing.T) {
		t.Parallel()
		prop.ForAll(t, "concurrent calls must linearize against the last Set of each key", func(pc *prop.Case) {
			c, err := cache.New(cache.Config[int, int]{Clock: fake.New(origin), Capacity: workloadCapacity})
			assert.NoError(pc, err, "New must accept the configuration")
			var sets atomic.Int64
			stateful.Steps(pc, stateful.Machine[keyValues]{
				Spec: history.Spec[keyValues]{
					Initial: func() keyValues {
						var state keyValues
						for k := range state {
							state[k] = absent
						}

						return state
					},
					Next: stepCache,
				},
				Actions: cacheActions(c, &sets),
			}, stateful.Clients(clients-1))
		})
	})

	t.Run("passes each value that leaves the cache to Evicted once", func(t *testing.T) {
		t.Parallel()
		prop.ForAll(t, "each stored value must be in the cache or have reached Evicted once", func(pc *prop.Case) {
			var evicted, sets atomic.Int64
			c, err := cache.New(cache.Config[int, int]{
				Clock:    fake.New(origin),
				Capacity: workloadCapacity,
				Evicted:  func(int, int) { evicted.Add(1) },
			})
			assert.NoError(pc, err, "New must accept the configuration")
			stateful.Steps(pc, stateful.Machine[keyValues]{
				Actions: cacheActions(c, &sets),
				Settle: func(pc *prop.Case, _ keyValues) {
					assert.Equal(pc, evicted.Load()+int64(c.Len()), sets.Load(),
						"each stored value must be in the cache or have reached Evicted once")
					assert.InRange(pc, c.Cost(), 0, workloadCapacity, "without pins the cost must fit the capacity")
				},
			}, stateful.Clients(clients-1))
		})
	})
}

// TestCacheAllocs checks the allocation contract of the methods of a
// Cache of benchCapacity keys. MaxAllocs counts the allocations of the
// whole process, so the test does not run in parallel.
func TestCacheAllocs(t *testing.T) {
	keys := names("k", 2*benchCapacity)

	t.Run("Get", func(t *testing.T) {
		c := benchCache(t, keys[:benchCapacity])
		c.Set("expiring", 1, origin.Add(time.Hour))
		var hit, miss, expiring bool
		expect.MaxAllocs(t, func() { _, hit = c.Get("k500") }, 0, "a Get of a hit must not allocate")
		expect.MaxAllocs(t, func() { _, miss = c.Get("absent") }, 0, "a Get of a miss must not allocate")
		expect.MaxAllocs(t, func() { _, expiring = c.Get("expiring") }, 0,
			"a Get of a hit with an expiry time must not allocate")
		expect.True(t, hit, "the test must measure a hit")
		expect.False(t, miss, "the test must measure a miss")
		expect.True(t, expiring, "the test must measure a hit with an expiry time")
	})

	t.Run("Pin", func(t *testing.T) {
		c := benchCache(t, keys[:benchCapacity])
		var ok bool
		expect.MaxAllocs(t, func() {
			var p cache.Pinned[string, int]
			p, ok = c.Pin("k500")
			p.Unpin()
		}, 0, "a Pin and its Unpin must not allocate")
		assert.True(t, ok, "the test must measure a hit")
	})

	t.Run("Set", func(t *testing.T) {
		full := benchCache(t, keys[:benchCapacity])
		expect.MaxAllocs(t, func() { full.Set("k500", 1, time.Time{}) }, 1,
			"a Set that replaces an entry must allocate the entry")
		assert.Equal(t, full.Len(), benchCapacity, "the test must measure Sets that replace")

		evicting := benchCache(t, keys)
		i := 0
		expect.MaxAllocs(t, func() {
			evicting.Set(keys[i%len(keys)], i, time.Time{})
			i++
		}, 1, "a Set of a new key that evicts an entry must allocate the entry")
		assert.Equal(t, evicting.Len(), benchCapacity, "the test must measure Sets that evict")
	})

	t.Run("Delete", func(t *testing.T) {
		c := benchCache(t, keys[:benchCapacity])
		var absentDeleted, presentDeleted bool
		expect.MaxAllocs(t, func() { absentDeleted = c.Delete("absent") }, 0,
			"a Delete of an absent key must not allocate")
		expect.MaxAllocsWithSetup(t, func() string {
			c.Set("d", 1, time.Time{})

			return "d"
		}, func(k string) { presentDeleted = c.Delete(k) }, 0, "a Delete of a present key must not allocate")
		expect.False(t, absentDeleted, "the test must measure an absent key")
		expect.True(t, presentDeleted, "the test must measure a present key")
	})

	t.Run("Len", func(t *testing.T) {
		c := benchCache(t, keys[:benchCapacity])
		var n int
		expect.MaxAllocs(t, func() { n = c.Len() }, 0, "Len must not allocate")
		assert.Equal(t, n, benchCapacity, "the test must measure a full cache")
	})

	t.Run("Cost", func(t *testing.T) {
		c := benchCache(t, keys[:benchCapacity])
		var cost int64
		expect.MaxAllocs(t, func() { cost = c.Cost() }, 0, "Cost must not allocate")
		assert.Equal(t, cost, int64(benchCapacity), "the test must measure a full cache")
	})
}

// BenchmarkCache reports the cost of the methods of a Cache of
// benchCapacity keys, and fails when a method allocates more than the
// allocation contract allows.
func BenchmarkCache(b *testing.B) {
	keys := names("k", 2*benchCapacity)

	b.Run("Get", func(b *testing.B) {
		b.Run("of a hit", func(b *testing.B) {
			c := benchCache(b, keys[:benchCapacity])
			var ok bool

			bc := bench.Start(b).MaxAllocs(0)
			defer bc.End()

			for bc.Loop() {
				_, ok = c.Get("k500")
			}

			assert.True(b, ok, "the benchmark must measure a hit")
		})

		b.Run("of a miss", func(b *testing.B) {
			c := benchCache(b, keys[:benchCapacity])
			ok := true

			bc := bench.Start(b).MaxAllocs(0)
			defer bc.End()

			for bc.Loop() {
				_, ok = c.Get("absent")
			}

			assert.False(b, ok, "the benchmark must measure a miss")
		})

		b.Run("of a hit with an expiry time", func(b *testing.B) {
			c := benchCache(b, nil)
			c.Set("k", 1, origin.Add(time.Hour))
			var ok bool

			bc := bench.Start(b).MaxAllocs(0)
			defer bc.End()

			for bc.Loop() {
				_, ok = c.Get("k")
			}

			assert.True(b, ok, "the benchmark must measure a hit")
		})

		b.Run("of a hit by goroutines in parallel", func(b *testing.B) {
			c := benchCache(b, keys[:benchCapacity])
			var hits atomic.Int64

			bc := bench.Start(b).MaxAllocs(0)
			defer bc.End()

			bc.RunParallel(func(pb *bench.PB) {
				i, n := 0, int64(0)
				for pb.Next() {
					if _, ok := c.Get(keys[i%benchCapacity]); ok {
						n++
					}
					i++
				}
				hits.Add(n)
			})

			assert.NotEqual(b, hits.Load(), int64(0), "the benchmark must measure hits")
		})
	})

	b.Run("Pin", func(b *testing.B) {
		b.Run("of a hit followed by its Unpin", func(b *testing.B) {
			c := benchCache(b, keys[:benchCapacity])
			var ok bool

			bc := bench.Start(b).MaxAllocs(0)
			defer bc.End()

			for bc.Loop() {
				var p cache.Pinned[string, int]
				p, ok = c.Pin("k500")
				p.Unpin()
			}

			assert.True(b, ok, "the benchmark must measure a hit")
		})
	})

	b.Run("Set", func(b *testing.B) {
		b.Run("of a key that replaces its entry", func(b *testing.B) {
			c := benchCache(b, keys[:benchCapacity])

			bc := bench.Start(b).MaxAllocs(1)
			defer bc.End()

			for bc.Loop() {
				c.Set("k500", 1, time.Time{})
			}

			assert.Equal(b, c.Len(), benchCapacity, "the benchmark must measure a full cache")
		})

		b.Run("of a new key that evicts an entry", func(b *testing.B) {
			c := benchCache(b, keys)
			i := 0

			bc := bench.Start(b).MaxAllocs(1)
			defer bc.End()

			for bc.Loop() {
				c.Set(keys[i%len(keys)], i, time.Time{})
				i++
			}

			assert.Equal(b, c.Len(), benchCapacity, "the benchmark must measure Sets that evict")
		})
	})

	b.Run("Delete", func(b *testing.B) {
		b.Run("of an absent key", func(b *testing.B) {
			c := benchCache(b, keys[:benchCapacity])
			deleted := true

			bc := bench.Start(b).MaxAllocs(0)
			defer bc.End()

			for bc.Loop() {
				deleted = c.Delete("absent")
			}

			assert.False(b, deleted, "the benchmark must measure an absent key")
		})
	})
}

// newCache returns an empty Cache of capacity over a fake clock at origin,
// whose Evicted records into the returned recorder, and whose Cost is cost.
// It fails tb when New refuses the configuration.
func newCache(
	tb testing.TB, capacity int64, cost func(int) int64,
) (*cache.Cache[string, int], *fake.Clock, *recorder) {
	tb.Helper()
	r := &recorder{}
	clk := fake.New(origin)
	c, err := cache.New(cache.Config[string, int]{Clock: clk, Capacity: capacity, Cost: cost, Evicted: r.evicted})
	assert.NoError(tb, err, "New must accept the configuration")

	return c, clk, r
}

// benchCache returns a Cache of benchCapacity without Cost and without
// Evicted, so that a measurement covers the cache alone, after a Set of 1
// under each key of keys in order. A Set of 10 times as many keys first
// puts the cache in the steady state of its eviction.
func benchCache(tb testing.TB, keys []string) *cache.Cache[string, int] {
	tb.Helper()
	c, err := cache.New(cache.Config[string, int]{Clock: fake.New(origin), Capacity: benchCapacity})
	assert.NoError(tb, err, "New must accept the configuration")
	for range 10 {
		setAll(c, keys...)
	}

	return c
}

// setAll stores 1 under each key, in order, without an expiry time.
func setAll(c *cache.Cache[string, int], keys ...string) {
	for _, k := range keys {
		c.Set(k, 1, time.Time{})
	}
}

// names returns the keys prefix0 to prefix<n-1>.
func names(prefix string, n int) []string {
	keys := make([]string, n)
	for i := range keys {
		keys[i] = prefix + strconv.Itoa(i)
	}

	return keys
}

// cacheActions returns the actions of a machine over c, each on a key
// below workloadKeys: a Set of a value that no Set before it stored, a
// Get, a Pin with its Unpin, and a Delete. Each records its call in the
// history of its case, with the key as its first argument and a miss as
// absent, and sets counts the Sets.
func cacheActions(c *cache.Cache[int, int], sets *atomic.Int64) []stateful.Action[keyValues] {
	written := 0
	key := func(pc *prop.Case, _ keyValues) any { return pc.Draw(prop.Integer(0, workloadKeys-1), "key") }

	return []stateful.Action[keyValues]{
		{
			Name: opSet,
			Input: func(pc *prop.Case, _ keyValues) any {
				written++

				return [2]int{pc.Draw(prop.Integer(0, workloadKeys-1), "key"), written}
			},
			Run: func(pc *prop.Case, client int, in any) {
				kv := in.([2]int)
				call := pc.History().Invoke(client, opSet, []any{kv[0], kv[1]})
				c.Set(kv[0], kv[1], time.Time{})
				call.OK(nil)
				sets.Add(1)
			},
		},
		{
			Name:  opGet,
			Input: key,
			Run: func(pc *prop.Case, client int, in any) {
				call := pc.History().Invoke(client, opGet, []any{in})
				v, ok := c.Get(in.(int))
				if !ok {
					v = absent
				}
				call.OK(v)
			},
		},
		{
			Name:  opPin,
			Input: key,
			Run: func(pc *prop.Case, client int, in any) {
				call := pc.History().Invoke(client, opPin, []any{in})
				p, ok := c.Pin(in.(int))
				v := p.Value()
				if !ok {
					v = absent
				}
				p.Unpin()
				call.OK(v)
			},
		},
		{
			Name:  opDelete,
			Input: key,
			Run: func(pc *prop.Case, client int, in any) {
				call := pc.History().Invoke(client, opDelete, []any{in})
				call.OK(c.Delete(in.(int)))
			},
		},
	}
}

// stepCache returns the state of a Cache of the workload after op. The
// state of a key is its value, and absent for a key without an entry. An
// eviction removes an entry without a call, so a miss and a Delete that
// reports false are allowed in every state.
func stepCache(state keyValues, op history.Operation) []keyValues {
	k := op.Args[0].(int)
	if op.Name == opSet {
		state[k] = op.Args[1].(int)

		return []keyValues{state}
	}
	if op.Name == opDelete {
		if op.Known && op.Output == true && state[k] == absent {
			return nil
		}
		state[k] = absent

		return []keyValues{state}
	}
	if op.Returned(absent) || op.Returned(state[k]) {
		return []keyValues{state}
	}

	return nil
}
