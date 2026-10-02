// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package cache_test

import (
	"math/rand/v2"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/cache"
	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/errs"
)

const (
	// benchRuns is the number of calls over which a benchmark averages the
	// allocations that it checks.
	benchRuns = 100

	// benchCapacity is the capacity of the caches of the benchmarks.
	benchCapacity = 1_000
)

// origin is the time of the fake clock of every case.
var origin = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// Sinks keep the results of the benchmarks alive.
var (
	sinkValue int
	sinkOK    bool
)

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
	testkit.NoError(tb, err, "New must accept the configuration")

	return c, clk, r
}

// valueCost is a Cost that costs each value its own amount.
func valueCost(v int) int64 { return int64(v) }

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
				testkit.ErrorIs(t, err, cache.ErrConfig, "New must refuse the configuration")
				testkit.Equal(t, errs.Classify(err), errs.Invalid, "ErrConfig must classify as Invalid")
				testkit.True(t, c == nil, "a refused configuration must return no Cache")
			})
		}

		t.Run("returns an empty Cache for a Capacity of 1", func(t *testing.T) {
			t.Parallel()
			c, _, _ := newCache(t, 1, nil)
			testkit.Equal(t, c.Len(), 0, "a new Cache must hold no entry")
			testkit.Equal(t, c.Cost(), int64(0), "a new Cache must cost nothing")

			_, ok := c.Get("k")
			testkit.False(t, ok, "a new Cache must miss every key")
		})
	})

	t.Run("Get", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the value that Set stored", func(t *testing.T) {
			t.Parallel()
			c, _, _ := newCache(t, 10, nil)
			c.Set("k", 7, time.Time{})

			v, ok := c.Get("k")
			testkit.True(t, ok, "Get must find the stored key")
			testkit.Equal(t, v, 7, "Get must return the stored value")
		})

		t.Run("reports false for a key that Set never stored", func(t *testing.T) {
			t.Parallel()
			c, _, _ := newCache(t, 10, nil)
			c.Set("k", 7, time.Time{})

			v, ok := c.Get("other")
			testkit.False(t, ok, "Get must miss a key that was never stored")
			testkit.Equal(t, v, 0, "a miss must return the zero value")
		})

		t.Run("returns the value one nanosecond before its expiry time", func(t *testing.T) {
			t.Parallel()
			c, clk, _ := newCache(t, 10, nil)
			c.Set("k", 7, origin.Add(time.Second))

			clk.Advance(time.Second - time.Nanosecond)
			v, ok := c.Get("k")
			testkit.True(t, ok, "an entry before its expiry time must be a hit")
			testkit.Equal(t, v, 7, "Get must return the stored value")
		})

		t.Run("reports false at the expiry time", func(t *testing.T) {
			t.Parallel()
			c, clk, _ := newCache(t, 10, nil)
			c.Set("k", 7, origin.Add(time.Second))

			clk.Advance(time.Second)
			_, ok := c.Get("k")
			testkit.False(t, ok, "an entry at its expiry time must be a miss")
		})

		t.Run("removes an expired entry and passes it to Evicted", func(t *testing.T) {
			t.Parallel()
			c, clk, r := newCache(t, 10, nil)
			c.Set("k", 7, origin.Add(time.Second))
			c.Set("other", 1, time.Time{})

			clk.Advance(time.Second)
			c.Get("k")
			testkit.Equal(t, r.got(), []string{"k"}, "the expired entry must reach Evicted once")
			testkit.Equal(t, c.Len(), 1, "the expired entry must leave the cache")

			c.Get("k")
			testkit.Equal(t, r.got(), []string{"k"}, "a second Get must not pass the entry to Evicted again")
		})

		t.Run("returns the value of an entry without an expiry time at any time", func(t *testing.T) {
			t.Parallel()
			c, clk, _ := newCache(t, 10, nil)
			c.Set("k", 7, time.Time{})

			clk.Advance(1000 * time.Hour)
			_, ok := c.Get("k")
			testkit.True(t, ok, "the zero expiry time must never expire")
		})
	})

	t.Run("Pin", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the entry that Set stored", func(t *testing.T) {
			t.Parallel()
			c, _, _ := newCache(t, 10, nil)
			c.Set("k", 7, time.Time{})

			p, ok := c.Pin("k")
			testkit.True(t, ok, "Pin must find the stored key")
			testkit.Equal(t, p.Value(), 7, "the Pinned must hold the stored value")
			p.Unpin()
		})

		t.Run("reports false for a key that Set never stored", func(t *testing.T) {
			t.Parallel()
			c, _, _ := newCache(t, 10, nil)

			p, ok := c.Pin("k")
			testkit.False(t, ok, "Pin must miss a key that was never stored")
			testkit.Equal(t, p.Value(), 0, "a miss must return the zero Pinned")
		})

		t.Run("reports false for an expired entry and removes it", func(t *testing.T) {
			t.Parallel()
			c, clk, r := newCache(t, 10, nil)
			c.Set("k", 7, origin.Add(time.Second))

			clk.Advance(time.Second)
			_, ok := c.Pin("k")
			testkit.False(t, ok, "Pin must miss an expired entry")
			testkit.Equal(t, r.got(), []string{"k"}, "the expired entry must reach Evicted")
			testkit.Equal(t, c.Len(), 0, "the expired entry must leave the cache")
		})

		t.Run("reports false for an entry that a Delete removed", func(t *testing.T) {
			t.Parallel()
			c, _, _ := newCache(t, 10, nil)
			c.Set("k", 7, time.Time{})
			c.Delete("k")

			_, ok := c.Pin("k")
			testkit.False(t, ok, "Pin must miss a deleted entry")
		})
	})

	t.Run("Set", func(t *testing.T) {
		t.Parallel()

		t.Run("replaces the value of a key", func(t *testing.T) {
			t.Parallel()
			c, _, r := newCache(t, 10, nil)
			c.Set("k", 1, time.Time{})
			c.Set("k", 2, time.Time{})

			v, _ := c.Get("k")
			testkit.Equal(t, v, 2, "Get must return the second value")
			testkit.Equal(t, c.Len(), 1, "a replaced key must hold one entry")
			testkit.Equal(t, r.got(), []string{"k"}, "the replaced value must reach Evicted")
		})

		t.Run("evicts the oldest entries without hits until the cost fits the capacity", func(t *testing.T) {
			t.Parallel()
			c, _, r := newCache(t, 3, nil)
			setAll(c, "a", "b", "c", "d", "e")

			testkit.Equal(t, c.Len(), 3, "the cache must hold Capacity entries of cost 1")
			testkit.Equal(t, c.Cost(), int64(3), "the cost must fit the capacity")
			testkit.Equal(t, r.got(), []string{"a", "b"}, "the oldest entries must leave first")
		})

		t.Run("bounds the sum of the costs that Cost returns", func(t *testing.T) {
			t.Parallel()
			c, _, r := newCache(t, 10, valueCost)
			c.Set("a", 4, time.Time{})
			c.Set("b", 4, time.Time{})
			c.Set("c", 4, time.Time{})

			testkit.Equal(t, c.Cost(), int64(8), "the cache must evict until the costs fit 10")
			testkit.Equal(t, r.got(), []string{"a"}, "one entry of cost 4 must leave")
		})

		t.Run("counts a negative cost as zero", func(t *testing.T) {
			t.Parallel()
			c, _, _ := newCache(t, 10, valueCost)
			c.Set("k", -5, time.Time{})

			testkit.Equal(t, c.Cost(), int64(0), "a negative cost must count as 0")
			testkit.Equal(t, c.Len(), 1, "an entry of negative cost must be stored")
		})

		t.Run("stores a value whose cost equals the capacity", func(t *testing.T) {
			t.Parallel()
			c, _, _ := newCache(t, 10, valueCost)
			c.Set("k", 10, time.Time{})

			_, ok := c.Get("k")
			testkit.True(t, ok, "a value of the capacity's cost must fit")
		})

		t.Run("passes a value whose cost exceeds the capacity to Evicted", func(t *testing.T) {
			t.Parallel()
			c, _, r := newCache(t, 10, valueCost)
			c.Set("small", 1, time.Time{})
			c.Set("k", 1, time.Time{})
			c.Set("k", 11, time.Time{})

			_, ok := c.Get("k")
			testkit.False(t, ok, "a value above the capacity must not be stored")
			testkit.Equal(t, r.got(), []string{"k", "k"}, "the replaced value and the refused value must reach Evicted")
			testkit.Equal(t, c.Cost(), int64(1), "the refused value must evict nothing else")
		})

		t.Run("keeps its entry past the capacity while pinned entries fill the cache", func(t *testing.T) {
			t.Parallel()
			c, _, r := newCache(t, 2, nil)
			setAll(c, "a", "b")
			a, _ := c.Pin("a")
			b, _ := c.Pin("b")

			c.Set("c", 1, time.Time{})
			testkit.Equal(t, c.Len(), 3, "a Set into a pinned cache must keep its entry")
			testkit.Equal(t, r.got(), []string(nil), "no pinned entry may leave")

			a.Unpin()
			b.Unpin()
			c.Set("d", 1, time.Time{})
			testkit.Equal(t, c.Len(), 2, "the next Set must evict down to the capacity")
		})

		t.Run("stores an entry whose expiry time has passed as a miss", func(t *testing.T) {
			t.Parallel()
			c, _, _ := newCache(t, 10, nil)
			c.Set("k", 7, origin.Add(-time.Second))

			_, ok := c.Get("k")
			testkit.False(t, ok, "an entry stored after its expiry time must be a miss")
		})

		t.Run("passes the replaced entry to Evicted before the entries that it evicts", func(t *testing.T) {
			t.Parallel()
			c, _, r := newCache(t, 3, valueCost)
			c.Set("a", 1, time.Time{})
			c.Set("b", 1, time.Time{})
			c.Set("c", 1, time.Time{})
			c.Set("c", 2, time.Time{})

			testkit.Equal(t, r.got(), []string{"c", "a"}, "the replaced value must reach Evicted first")
		})

		t.Run("calls Evicted after it releases its lock", func(t *testing.T) {
			t.Parallel()
			var c *cache.Cache[string, int]

			deleted := make(chan bool, 1)
			c, err := cache.New(cache.Config[string, int]{
				Clock:    fake.New(origin),
				Capacity: 1,
				Evicted:  func(string, int) { deleted <- c.Delete("other") },
			})
			testkit.NoError(t, err, "New must accept the configuration")

			done := make(chan struct{})
			go func() {
				setAll(c, "a", "b")
				close(done)
			}()

			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("Evicted ran under the lock, and its Delete never returned")
			}
			testkit.False(t, <-deleted, "the Delete inside Evicted must run")
		})
	})

	t.Run("Delete", func(t *testing.T) {
		t.Parallel()

		t.Run("removes the entry and reports true", func(t *testing.T) {
			t.Parallel()
			c, _, r := newCache(t, 10, nil)
			c.Set("k", 7, time.Time{})

			testkit.True(t, c.Delete("k"), "Delete must report the removed entry")
			_, ok := c.Get("k")
			testkit.False(t, ok, "a deleted key must miss")
			testkit.Equal(t, r.got(), []string{"k"}, "the deleted entry must reach Evicted")
			testkit.Equal(t, c.Len(), 0, "the deleted entry must leave the cache")
		})

		t.Run("reports false for a key that the cache does not hold", func(t *testing.T) {
			t.Parallel()
			c, _, r := newCache(t, 10, nil)

			testkit.False(t, c.Delete("k"), "Delete of an absent key must report false")
			testkit.Equal(t, r.got(), []string(nil), "Delete of an absent key must not call Evicted")
		})

		t.Run("reports true for an expired entry", func(t *testing.T) {
			t.Parallel()
			c, clk, r := newCache(t, 10, nil)
			c.Set("k", 7, origin.Add(time.Second))

			clk.Advance(2 * time.Second)
			testkit.True(t, c.Delete("k"), "an expired entry that no Get removed must count as held")
			testkit.Equal(t, r.got(), []string{"k"}, "the deleted entry must reach Evicted")
		})
	})

	t.Run("Len", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the number of entries", func(t *testing.T) {
			t.Parallel()
			c, _, _ := newCache(t, 10, valueCost)
			c.Set("a", 3, time.Time{})
			c.Set("b", 4, time.Time{})

			testkit.Equal(t, c.Len(), 2, "Len must count the entries, not their costs")
		})

		t.Run("excludes a pinned entry that left", func(t *testing.T) {
			t.Parallel()
			c, _, _ := newCache(t, 10, nil)
			c.Set("k", 7, time.Time{})
			p, _ := c.Pin("k")
			defer p.Unpin()

			c.Delete("k")
			testkit.Equal(t, c.Len(), 0, "an entry that left must not count while pinned")
		})
	})

	t.Run("Cost", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the sum of the costs of the entries", func(t *testing.T) {
			t.Parallel()
			c, _, _ := newCache(t, 10, valueCost)
			c.Set("a", 3, time.Time{})
			c.Set("b", 4, time.Time{})

			testkit.Equal(t, c.Cost(), int64(7), "Cost must sum the costs")

			c.Delete("a")
			testkit.Equal(t, c.Cost(), int64(4), "Cost must drop by the cost of a removed entry")
		})
	})

	t.Run("passes every value that Set stored to Evicted once, or keeps it", func(t *testing.T) {
		t.Parallel()
		const (
			goroutines = 8
			operations = 2_000
		)

		var (
			sets    atomic.Int64
			evicted atomic.Int64
		)
		c, err := cache.New(cache.Config[int, int]{
			Clock:    fake.New(origin),
			Capacity: 16,
			Evicted:  func(int, int) { evicted.Add(1) },
		})
		testkit.NoError(t, err, "New must accept the configuration")

		var wg sync.WaitGroup
		for g := range goroutines {
			wg.Go(func() {
				r := rand.New(rand.NewPCG(uint64(g), 1))
				for range operations {
					k := r.IntN(64)
					switch r.IntN(4) {
					case 0:
						c.Set(k, k, time.Time{})
						sets.Add(1)
					case 1:
						c.Delete(k)
					case 2:
						if p, ok := c.Pin(k); ok {
							p.Unpin()
						}
					default:
						c.Get(k)
					}
				}
			})
		}
		wg.Wait()

		testkit.Equal(t, evicted.Load()+int64(c.Len()), sets.Load(),
			"each stored value must be in the cache or have reached Evicted once")
		testkit.True(t, c.Cost() <= 16, "without pins the cost must fit the capacity")
	})
}

func BenchmarkCache(b *testing.B) {
	keys := names("k", 2*benchCapacity)

	b.Run("Get of a hit", func(b *testing.B) {
		c := benchCache(b, keys[:benchCapacity])

		cacheAllocs(b, 0, func() { sinkValue, sinkOK = c.Get("k500") })
		testkit.True(b, sinkOK, "the benchmark must measure a hit")
	})

	b.Run("Get of a miss", func(b *testing.B) {
		c := benchCache(b, keys[:benchCapacity])

		cacheAllocs(b, 0, func() { sinkValue, sinkOK = c.Get("absent") })
		testkit.False(b, sinkOK, "the benchmark must measure a miss")
	})

	b.Run("Get of a hit with an expiry time", func(b *testing.B) {
		c := benchCache(b, nil)
		c.Set("k", 1, origin.Add(time.Hour))

		cacheAllocs(b, 0, func() { sinkValue, sinkOK = c.Get("k") })
		testkit.True(b, sinkOK, "the benchmark must measure a hit")
	})

	b.Run("Pin and Unpin", func(b *testing.B) {
		c := benchCache(b, keys[:benchCapacity])

		cacheAllocs(b, 0, func() {
			p, ok := c.Pin("k500")
			sinkOK = ok
			p.Unpin()
		})
		testkit.True(b, sinkOK, "the benchmark must measure a hit")
	})

	b.Run("Set of a key that replaces its entry", func(b *testing.B) {
		c := benchCache(b, keys[:benchCapacity])

		cacheAllocs(b, 1, func() { c.Set("k500", 1, time.Time{}) })
	})

	b.Run("Set of a new key that evicts an entry", func(b *testing.B) {
		c := benchCache(b, keys)

		i := 0
		set := func() {
			c.Set(keys[i%len(keys)], i, time.Time{})
			i++
		}
		for range 10 * len(keys) {
			set()
		}

		cacheAllocs(b, 1, set)
	})

	b.Run("Delete of an absent key", func(b *testing.B) {
		c := benchCache(b, keys[:benchCapacity])

		cacheAllocs(b, 0, func() { sinkOK = c.Delete("absent") })
		testkit.False(b, sinkOK, "the benchmark must measure an absent key")
	})

	b.Run("Get of a hit by goroutines in parallel", func(b *testing.B) {
		c := benchCache(b, keys[:benchCapacity])

		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			i := 0
			for pb.Next() {
				c.Get(keys[i%len(keys)])
				i++
			}
		})
	})
}

// benchCache returns a Cache of benchCapacity without Cost and without
// Evicted, so that a benchmark measures the cache alone, after a Set of 1
// under each key of keys in order.
func benchCache(b *testing.B, keys []string) *cache.Cache[string, int] {
	b.Helper()

	c, err := cache.New(cache.Config[string, int]{Clock: fake.New(origin), Capacity: benchCapacity})
	testkit.NoError(b, err, "New must accept the configuration")
	setAll(c, keys...)

	return c
}

// cacheAllocs fails b when call does not allocate want times per call,
// averaged over benchRuns calls, and then reports the time and the
// allocations of call per iteration. The check runs in the benchmark, so
// it applies to the build that the benchmark measures.
func cacheAllocs(b *testing.B, want float64, call func()) {
	b.Helper()

	if allocs := testing.AllocsPerRun(benchRuns, call); allocs != want {
		b.Fatalf("allocates %v times per call, want %v", allocs, want)
	}

	b.ReportAllocs()
	for b.Loop() {
		call()
	}
}
