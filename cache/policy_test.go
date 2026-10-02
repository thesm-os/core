// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package cache_test

import (
	"container/list"
	"math/rand/v2"
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/cache"
	"go.thesmos.sh/core/clock/fake"
)

const (
	// traceKeys is the number of keys of the Zipf distribution of a trace,
	// and traceRequests the number of requests of a trace.
	traceKeys     = 20_000
	traceRequests = 200_000

	// traceCapacity is the capacity of the caches that replay a trace: 5%
	// of its keys.
	traceCapacity = 1_000
)

// lru is the reference policy of the miss-ratio cases: a cache of
// traceCapacity keys that evicts the least recently used. It is not safe
// for concurrent use.
type lru struct {
	keys  map[uint64]*list.Element
	order *list.List
}

// newLRU returns an empty lru.
func newLRU() *lru {
	return &lru{keys: make(map[uint64]*list.Element, traceCapacity), order: list.New()}
}

// access reports whether k was a hit, and stores k as the most recently
// used key, evicting the least recently used key when the cache is full.
func (l *lru) access(k uint64) bool {
	if e, ok := l.keys[k]; ok {
		l.order.MoveToFront(e)

		return true
	}

	if l.order.Len() == traceCapacity {
		delete(l.keys, l.order.Remove(l.order.Back()).(uint64))
	}

	l.keys[k] = l.order.PushFront(k)

	return false
}

// zipfTrace returns traceRequests keys drawn from a Zipf distribution of
// exponent 1.1 over traceKeys keys, with a fixed seed, plus the offset that
// shift returns for the position of each request.
func zipfTrace(seed uint64, shift func(i int) uint64) []uint64 {
	z := rand.NewZipf(rand.New(rand.NewPCG(seed, seed)), 1.1, 1, traceKeys-1)

	trace := make([]uint64, traceRequests)
	for i := range trace {
		trace[i] = z.Uint64() + shift(i)
	}

	return trace
}

// misses returns the misses of a cache of traceCapacity over trace, which
// stores each key that it misses, and those of the lru reference.
func misses(tb testing.TB, trace []uint64) (got, reference int) {
	tb.Helper()

	c, err := cache.New(cache.Config[uint64, struct{}]{Clock: fake.New(origin), Capacity: traceCapacity})
	testkit.NoError(tb, err, "New must accept the configuration")

	ref := newLRU()
	for _, k := range trace {
		if _, ok := c.Get(k); !ok {
			got++
			c.Set(k, struct{}{}, time.Time{})
		}

		if !ref.access(k) {
			reference++
		}
	}

	return got, reference
}

// TestPolicy checks the eviction of a Cache against LRU on traces, and the
// ghost through Set and Get. A Cache of capacity 10 examines its small
// queue whenever the queue contains an entry, and its ghost remembers 9
// keys.
func TestPolicy(t *testing.T) {
	t.Parallel()

	t.Run("evict", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name  string
			shift func(i int) uint64
		}{
			{
				name:  "misses at most 90% as often as LRU on a Zipf trace",
				shift: func(int) uint64 { return 0 },
			},
			{
				name: "misses at most 90% as often as LRU on a Zipf trace with scans",
				shift: func(i int) uint64 {
					if i%10_000 < 2_000 {
						return traceKeys * uint64(1+i)
					}

					return 0
				},
			},
			{
				name:  "misses at most 90% as often as LRU on a Zipf trace whose keys change",
				shift: func(i int) uint64 { return traceKeys * uint64(i/50_000) },
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, reference := misses(t, zipfTrace(1, tt.shift))
				testkit.True(t, 10*got <= 9*reference, "S3-FIFO must miss less than LRU")
			})
		}
	})

	t.Run("ghost", func(t *testing.T) {
		t.Parallel()

		t.Run("admits a key that the small queue evicted to the main queue", func(t *testing.T) {
			t.Parallel()
			c, _, r := newCache(t, 10, nil)
			setAll(c, "a")
			setAll(c, names("k", 10)...)
			testkit.Equal(t, r.got(), []string{"a"}, "the first key must leave the small queue")

			setAll(c, "a")
			setAll(c, names("s", 30)...)
			_, ok := c.Get("a")
			testkit.True(t, ok, "a key that the ghost remembers must stay in the main queue through a scan")
		})

		t.Run("admits a key that the ghost forgot to the small queue", func(t *testing.T) {
			t.Parallel()
			c, _, _ := newCache(t, 10, nil)
			setAll(c, "a")
			setAll(c, names("k", 19)...)

			setAll(c, "a")
			setAll(c, names("s", 10)...)
			_, ok := c.Get("a")
			testkit.False(t, ok, "a key past the ghost's 9 keys must leave with the scan")
		})
	})
}
