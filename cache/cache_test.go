// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package cache_test

import (
	"context"
	"errors"
	"math"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
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

// getOrLoadMissAllocs is the number of allocations of a GetOrLoad of a miss
// that starts a load: the state of the load, its channel, the closure that
// starts its goroutine, its two contexts, the cancel function of its context
// and the entry of its value.
const getOrLoadMissAllocs = 7

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
	opSet       = "set"
	opSetPinned = "setpinned"
	opGet       = "get"
	opGetOrLoad = "getorload"
	opPin       = "pin"
	opDelete    = "delete"
)

// loadBase is the first value that a load of the concurrent workload
// returns. A Set of the workload stores a value below it.
const loadBase = 1 << 20

// The actions of the machine of Expire, beside opSet and opDelete.
const (
	opAdvance = "advance"
	opExpire  = "expire"
)

// The machine of pins: keys of cost 1 in a Cache whose open pins fill it.
const (
	// opUnpin is the action that ends one of the open pins.
	opUnpin = "unpin"

	// pinCapacity is the capacity of the cache of the machine.
	pinCapacity = 3

	// pinKeys is the number of keys that the calls choose from.
	pinKeys = 6

	// pinDraws bounds the draw of the open pin that an Unpin ends.
	pinDraws = 8
)

// replacements is the number of Sets of one key while another goroutine
// gets the key.
const replacements = 100_000

// origin is the time of the fake clock of every case.
var origin = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// errBoom is the error of a load that fails.
var errBoom = errors.New("cache_test: boom")

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

// loaded is the result of one call of GetOrLoad.
type loaded struct {
	err error
	v   int
}

// loadKey is the key of the value of the context of a load.
type loadKey struct{}

func TestCache(t *testing.T) {
	t.Parallel()

	t.Run("New", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give cache.Config[string, int]
		}{
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

		t.Run("returns an empty Cache for a nil Clock", func(t *testing.T) {
			t.Parallel()
			c, err := cache.New(cache.Config[string, int]{Capacity: 1})
			assert.NoError(t, err, "New must accept a Config without a Clock")
			assert.Equal(t, c.Len(), 0, "a new Cache must contain no entry")
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

		t.Run("returns a value of a key while Set replaces the entry of the key", func(t *testing.T) {
			t.Parallel()
			c, _, _ := newCache(t, 10, nil)
			c.Set("k", 0, time.Time{})

			var (
				done   atomic.Bool
				misses int
				wg     sync.WaitGroup
			)
			wg.Go(func() {
				defer done.Store(true)

				for i := range replacements {
					c.Set("k", i, time.Time{})
				}
			})
			wg.Go(func() {
				for !done.Load() {
					if _, ok := c.Get("k"); !ok {
						misses++
					}
				}
			})
			wg.Wait()
			assert.Equal(t, misses, 0, "a Get must find the old entry or the new one while Set replaces it")
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
			assert.Equal(t, r.got(), []string{"k"}, "Evicted must receive the expired entry once")
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
			expect.Equal(t, r.got(), []string{"k"}, "Evicted must receive the entry once")
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

	t.Run("GetOrLoad", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the value of a hit without a call of the load", func(t *testing.T) {
			t.Parallel()
			c, _, _ := newCache(t, 10, nil)
			c.Set("k", 7, time.Time{})

			var calls atomic.Int32
			v, err := c.GetOrLoad(t.Context(), "k", func(context.Context, string) (int, time.Time, error) {
				calls.Add(1)

				return 0, time.Time{}, nil
			})
			assert.NoError(t, err, "a hit must not fail")
			expect.Equal(t, v, 7, "a hit must return the stored value")
			expect.Equal(t, calls.Load(), int32(0), "a hit must not call the load")
		})

		t.Run("counts a hit toward the hits of the entry", func(t *testing.T) {
			t.Parallel()
			c, _, r := newCache(t, 2, nil)
			c.Set("a", 1, time.Time{})
			_, err := c.GetOrLoad(t.Context(), "a", nil)
			assert.NoError(t, err, "a hit must not fail")
			setAll(c, "b", "c")
			assert.Equal(t, r.got(), []string{"b"}, "the entry of the hit must move to the main queue")
		})

		t.Run("loads a missing value once for the callers that miss it together", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				c, _, _ := newCache(t, 10, nil)
				release := make(chan struct{})

				var calls atomic.Int32
				load := func(_ context.Context, k string) (int, time.Time, error) {
					calls.Add(1)
					<-release

					return len(k), time.Time{}, nil
				}
				waits := []<-chan loaded{
					startGetOrLoad(t.Context(), c, "key", load),
					startGetOrLoad(t.Context(), c, "key", load),
					startGetOrLoad(t.Context(), c, "key", load),
				}
				synctest.Wait()
				close(release)

				for _, got := range waits {
					expect.Equal(t, <-got, loaded{v: 3}, "each caller must receive the value of the one load")
				}
				expect.Equal(t, calls.Load(), int32(1), "the callers must share one load")
			})
		})

		t.Run("stores the value of a load", func(t *testing.T) {
			t.Parallel()
			c, _, _ := newCache(t, 10, nil)
			_, err := c.GetOrLoad(t.Context(), "k", func(context.Context, string) (int, time.Time, error) {
				return 7, time.Time{}, nil
			})
			assert.NoError(t, err, "the load must not fail")

			v, ok := c.Get("k")
			assert.True(t, ok, "the cache must keep the value of the load")
			assert.Equal(t, v, 7, "the entry must contain the value of the load")
		})

		t.Run("stores the value of a load with the expiry time that the load returns", func(t *testing.T) {
			t.Parallel()
			c, clk, _ := newCache(t, 10, nil)
			_, err := c.GetOrLoad(t.Context(), "k", func(context.Context, string) (int, time.Time, error) {
				return 7, origin.Add(time.Second), nil
			})
			assert.NoError(t, err, "the load must not fail")

			clk.Advance(time.Second - time.Nanosecond)
			_, before := c.Get("k")
			clk.Advance(time.Nanosecond)
			_, after := c.Get("k")
			expect.True(t, before, "the entry must be a hit before its expiry time")
			expect.False(t, after, "the entry must expire at the time that the load returned")
		})

		t.Run("returns the error of a load to each waiting caller", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				c, _, _ := newCache(t, 10, nil)
				release := make(chan struct{})

				var calls atomic.Int32
				load := func(context.Context, string) (int, time.Time, error) {
					calls.Add(1)
					<-release

					return 0, time.Time{}, errBoom
				}
				first := startGetOrLoad(t.Context(), c, "k", load)
				second := startGetOrLoad(t.Context(), c, "k", load)
				synctest.Wait()
				close(release)

				expect.ErrorIs(t, (<-first).err, errBoom, "the first caller must receive the error of the load")
				expect.ErrorIs(t, (<-second).err, errBoom, "the second caller must receive the error of the load")
				expect.Equal(t, calls.Load(), int32(1), "the callers must share one load")
			})
		})

		t.Run("does not store a value for a load that fails", func(t *testing.T) {
			t.Parallel()
			c, _, _ := newCache(t, 10, nil)
			_, err := c.GetOrLoad(t.Context(), "k", func(context.Context, string) (int, time.Time, error) {
				return 7, time.Time{}, errBoom
			})
			assert.ErrorIs(t, err, errBoom, "GetOrLoad must return the error of the load")

			_, ok := c.Get("k")
			assert.False(t, ok, "the cache must not keep a value of a failed load")
		})

		t.Run("starts a new load for the next caller after a load fails", func(t *testing.T) {
			t.Parallel()
			c, _, _ := newCache(t, 10, nil)

			var calls atomic.Int32
			load := func(context.Context, string) (int, time.Time, error) {
				if calls.Add(1) == 1 {
					return 0, time.Time{}, errBoom
				}

				return 7, time.Time{}, nil
			}
			_, err := c.GetOrLoad(t.Context(), "k", load)
			assert.ErrorIs(t, err, errBoom, "the first load must fail")

			v, err := c.GetOrLoad(t.Context(), "k", load)
			assert.NoError(t, err, "the second load must succeed")
			expect.Equal(t, v, 7, "the next caller must receive the value of a new load")
			expect.Equal(t, calls.Load(), int32(2), "the next caller must start a new load")
		})

		t.Run("returns the cause of the context of a caller whose context ends during the load", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				c, _, _ := newCache(t, 10, nil)
				release := make(chan struct{})
				defer close(release)

				ctx, cancel := context.WithCancelCause(t.Context())
				got := startGetOrLoad(ctx, c, "k", func(context.Context, string) (int, time.Time, error) {
					<-release

					return 7, time.Time{}, nil
				})
				synctest.Wait()
				cancel(errBoom)

				assert.ErrorIs(t, (<-got).err, errBoom, "the caller must return the cause of its context")
			})
		})

		t.Run("keeps the load running for a caller that still waits", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				c, _, _ := newCache(t, 10, nil)
				release := make(chan struct{})
				load := func(ctx context.Context, _ string) (int, time.Time, error) {
					<-release

					return 7, time.Time{}, ctx.Err()
				}

				ctx, cancel := context.WithCancel(t.Context())
				first := startGetOrLoad(ctx, c, "k", load)
				second := startGetOrLoad(t.Context(), c, "k", load)
				synctest.Wait()
				cancel()
				synctest.Wait()
				close(release)

				expect.ErrorIs(t, (<-first).err, context.Canceled, "the first caller must stop when its context ends")
				expect.Equal(t, <-second, loaded{v: 7}, "the second caller must receive the value of the load")
			})
		})

		t.Run("stores the value of a load after one of its callers stopped waiting", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				c, _, _ := newCache(t, 10, nil)
				release := make(chan struct{})
				load := func(context.Context, string) (int, time.Time, error) {
					<-release

					return 7, time.Time{}, nil
				}

				ctx, cancel := context.WithCancel(t.Context())
				first := startGetOrLoad(ctx, c, "k", load)
				second := startGetOrLoad(t.Context(), c, "k", load)
				synctest.Wait()
				cancel()
				<-first
				close(release)
				<-second

				v, ok := c.Get("k")
				assert.True(t, ok, "the cache must keep the value of a load for which a caller still waits")
				assert.Equal(t, v, 7, "the entry must contain the value of the load")
			})
		})

		t.Run("ends the context of the load once the load returns", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				c, _, _ := newCache(t, 10, nil)

				loads := make(chan context.Context, 1)
				_, err := c.GetOrLoad(t.Context(), "k", func(ctx context.Context, _ string) (int, time.Time, error) {
					loads <- ctx

					return 7, time.Time{}, nil
				})
				assert.NoError(t, err, "the load must not fail")
				synctest.Wait()
				assert.ErrorIs(t, (<-loads).Err(), context.Canceled, "the context of the load must end with the load")
			})
		})

		t.Run("ends the context of the load once every waiting caller stopped", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				c, _, _ := newCache(t, 10, nil)
				ended := make(chan struct{})

				ctx, cancel := context.WithCancel(t.Context())
				got := startGetOrLoad(ctx, c, "k", func(ctx context.Context, _ string) (int, time.Time, error) {
					<-ctx.Done()
					close(ended)

					return 0, time.Time{}, ctx.Err()
				})
				synctest.Wait()
				cancel()
				<-got
				synctest.Wait()

				stopped := false
				select {
				case <-ended:
					stopped = true
				default:
				}
				assert.True(t, stopped, "the context of the load must end once no caller waits")
			})
		})

		t.Run("does not store the value of a load whose callers all stopped waiting", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				c, _, _ := newCache(t, 10, nil)
				release := make(chan struct{})

				ctx, cancel := context.WithCancel(t.Context())
				got := startGetOrLoad(ctx, c, "k", func(context.Context, string) (int, time.Time, error) {
					<-release

					return 7, time.Time{}, nil
				})
				synctest.Wait()
				cancel()
				<-got
				close(release)
				synctest.Wait()

				_, ok := c.Get("k")
				assert.False(t, ok, "the cache must not keep the value of a load without a waiting caller")
			})
		})

		t.Run("starts a new load once every caller of a load stopped waiting", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				c, _, _ := newCache(t, 10, nil)
				release := make(chan struct{})
				defer close(release)

				ctx, cancel := context.WithCancel(t.Context())
				first := startGetOrLoad(ctx, c, "k", func(context.Context, string) (int, time.Time, error) {
					<-release

					return 7, time.Time{}, nil
				})
				synctest.Wait()
				cancel()
				<-first

				v, err := c.GetOrLoad(t.Context(), "k", func(context.Context, string) (int, time.Time, error) {
					return 8, time.Time{}, nil
				})
				assert.NoError(t, err, "the new load must succeed")
				assert.Equal(t, v, 8, "the next caller must receive the value of a new load")
			})
		})

		t.Run("discards the value of a load that a Set of the key overlaps", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				c, _, _ := newCache(t, 10, nil)
				release := make(chan struct{})
				got := startGetOrLoad(t.Context(), c, "k", func(context.Context, string) (int, time.Time, error) {
					<-release

					return 7, time.Time{}, nil
				})
				synctest.Wait()
				c.Set("k", 9, time.Time{})
				close(release)
				<-got

				v, ok := c.Get("k")
				assert.True(t, ok, "the cache must keep the value of the Set")
				assert.Equal(t, v, 9, "the value of the load must not replace the value of the Set")
			})
		})

		t.Run("returns the value of a load that a Set overlaps to its callers", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				c, _, _ := newCache(t, 10, nil)
				release := make(chan struct{})
				got := startGetOrLoad(t.Context(), c, "k", func(context.Context, string) (int, time.Time, error) {
					<-release

					return 7, time.Time{}, nil
				})
				synctest.Wait()
				c.Set("k", 9, time.Time{})
				close(release)

				assert.Equal(t, <-got, loaded{v: 7}, "the caller must receive the value of its load")
			})
		})

		t.Run("discards the value of a load that a Delete of the key overlaps", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				c, _, _ := newCache(t, 10, nil)
				release := make(chan struct{})
				got := startGetOrLoad(t.Context(), c, "k", func(context.Context, string) (int, time.Time, error) {
					<-release

					return 7, time.Time{}, nil
				})
				synctest.Wait()
				c.Delete("k")
				close(release)
				<-got

				_, ok := c.Get("k")
				assert.False(t, ok, "the value of the load must not return after the Delete")
			})
		})

		t.Run("stores the value of a new load after the last caller of a load that a Delete discarded stops waiting",
			func(t *testing.T) {
				t.Parallel()
				synctest.Test(t, func(t *testing.T) {
					c, _, _ := newCache(t, 10, nil)
					releaseOld := make(chan struct{})
					defer close(releaseOld)

					ctx, cancel := context.WithCancel(t.Context())
					old := startGetOrLoad(ctx, c, "k", func(context.Context, string) (int, time.Time, error) {
						<-releaseOld

						return 7, time.Time{}, nil
					})
					synctest.Wait()
					c.Delete("k")

					releaseNew := make(chan struct{})
					got := startGetOrLoad(t.Context(), c, "k", func(context.Context, string) (int, time.Time, error) {
						<-releaseNew

						return 8, time.Time{}, nil
					})
					synctest.Wait()
					cancel()
					<-old
					close(releaseNew)
					<-got

					v, ok := c.Get("k")
					assert.True(t, ok, "the cache must keep the value of the new load")
					assert.Equal(t, v, 8, "the entry must contain the value of the new load")
				})
			})

		t.Run("returns the cause of a context that ended before the call without a load", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				c, _, _ := newCache(t, 10, nil)
				ctx, cancel := context.WithCancelCause(t.Context())
				cancel(errBoom)

				var calls atomic.Int32
				_, err := c.GetOrLoad(ctx, "k", func(context.Context, string) (int, time.Time, error) {
					calls.Add(1)

					return 7, time.Time{}, nil
				})
				synctest.Wait()
				expect.ErrorIs(t, err, errBoom, "the caller must return the cause of its context")
				expect.Equal(t, calls.Load(), int32(0), "an ended context must not start a load")
			})
		})

		t.Run("returns ErrLoad for a nil load on a miss", func(t *testing.T) {
			t.Parallel()
			c, _, _ := newCache(t, 10, nil)
			_, err := c.GetOrLoad(t.Context(), "k", nil)
			expect.ErrorIs(t, err, cache.ErrLoad, "a miss must refuse a nil load")
			expect.Equal(t, errs.Classify(err), errs.Invalid, "ErrLoad must classify as Invalid")
		})

		t.Run("returns ErrLoad for a load that succeeds with an expiry time in a Cache without a Clock",
			func(t *testing.T) {
				t.Parallel()
				c, err := cache.New(cache.Config[string, int]{Capacity: 10})
				assert.NoError(t, err, "New must accept a Config without a Clock")

				_, err = c.GetOrLoad(t.Context(), "k", func(context.Context, string) (int, time.Time, error) {
					return 7, origin, nil
				})
				expect.ErrorIs(t, err, cache.ErrLoad, "a cache without a Clock must refuse an expiry time")
				expect.Equal(t, c.Len(), 0, "the cache must not keep the value of the refused load")
			})

		t.Run("returns the error of a load that fails with an expiry time in a Cache without a Clock",
			func(t *testing.T) {
				t.Parallel()
				c, err := cache.New(cache.Config[string, int]{Capacity: 10})
				assert.NoError(t, err, "New must accept a Config without a Clock")

				_, err = c.GetOrLoad(t.Context(), "k", func(context.Context, string) (int, time.Time, error) {
					return 0, origin, errBoom
				})
				assert.ErrorIs(t, err, errBoom, "GetOrLoad must return the error of the load")
			})

		t.Run("stores the value of a load in a Cache without a Clock", func(t *testing.T) {
			t.Parallel()
			c, err := cache.New(cache.Config[string, int]{Capacity: 10})
			assert.NoError(t, err, "New must accept a Config without a Clock")

			_, err = c.GetOrLoad(t.Context(), "k", func(context.Context, string) (int, time.Time, error) {
				return 7, time.Time{}, nil
			})
			assert.NoError(t, err, "a load without an expiry time must not fail in a cache without a Clock")

			v, ok := c.Get("k")
			assert.True(t, ok, "the cache must keep the value of the load")
			assert.Equal(t, v, 7, "the entry must contain the value of the load")
		})

		t.Run("passes the value of a load whose cost exceeds the capacity to Evicted", func(t *testing.T) {
			t.Parallel()
			c, _, r := newCache(t, 10, func(v int) int64 { return int64(v) })
			v, err := c.GetOrLoad(t.Context(), "k", func(context.Context, string) (int, time.Time, error) {
				return 11, time.Time{}, nil
			})
			assert.NoError(t, err, "the load must not fail")
			expect.Equal(t, v, 11, "the caller must receive the value above the capacity")
			expect.Equal(t, r.got(), []string{"k"}, "Evicted must receive the value that the cache does not store")
		})

		t.Run("runs the load with the values of the context of the caller that starts it", func(t *testing.T) {
			t.Parallel()
			c, _, _ := newCache(t, 10, nil)
			ctx := context.WithValue(t.Context(), loadKey{}, 7)
			v, err := c.GetOrLoad(ctx, "k", func(ctx context.Context, _ string) (int, time.Time, error) {
				n, _ := ctx.Value(loadKey{}).(int)

				return n, time.Time{}, nil
			})
			assert.NoError(t, err, "the load must not fail")
			assert.Equal(t, v, 7, "the load must read the value of the context of its caller")
		})

		tests := []struct {
			name        string
			giveExpires time.Time
		}{
			{name: "returns an entry that a Set stored after the lookup of the caller missed"},
			{
				name:        "returns an entry with an expiry time that a Set stored after the lookup of the caller missed",
				giveExpires: origin.Add(time.Hour),
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				clk := &countingClock{Clock: fake.New(origin)}
				c, err := cache.New(cache.Config[string, int]{Clock: clk, Capacity: 10})
				assert.NoError(t, err, "New must accept the configuration")
				c.Set("k", 1, origin)
				clk.onTime = func() {
					clk.onTime = nil
					c.Set("k", 5, tt.giveExpires)
				}

				var calls atomic.Int32
				v, err := c.GetOrLoad(t.Context(), "k", func(context.Context, string) (int, time.Time, error) {
					calls.Add(1)

					return 7, time.Time{}, nil
				})
				assert.NoError(t, err, "a hit under the lock must not fail")
				expect.Equal(t, v, 5, "the caller must receive the value of the Set")
				expect.Equal(t, calls.Load(), int32(0), "a hit under the lock must not start a load")
			})
		}

		t.Run("counts a hit toward an entry that a Set stored after the lookup of the caller missed",
			func(t *testing.T) {
				t.Parallel()
				clk := &countingClock{Clock: fake.New(origin)}
				r := &recorder{}
				c, err := cache.New(cache.Config[string, int]{Clock: clk, Capacity: 2, Evicted: r.evicted})
				assert.NoError(t, err, "New must accept the configuration")
				c.Set("a", 0, origin)
				clk.onTime = func() {
					clk.onTime = nil
					c.Set("a", 1, time.Time{})
				}

				_, err = c.GetOrLoad(t.Context(), "a", func(context.Context, string) (int, time.Time, error) {
					return 7, time.Time{}, nil
				})
				assert.NoError(t, err, "a hit under the lock must not fail")
				setAll(c, "b", "c")
				assert.Equal(t, r.got(), []string{"a", "b"}, "the entry of the hit must move to the main queue")
			})

		t.Run("reads no clock for an entry without an expiry time that a Set stored after the lookup missed",
			func(t *testing.T) {
				t.Parallel()
				clk := &countingClock{Clock: fake.New(origin)}
				c, err := cache.New(cache.Config[string, int]{Clock: clk, Capacity: 10})
				assert.NoError(t, err, "New must accept the configuration")
				c.Set("k", 1, origin)

				var stored int64
				clk.onTime = func() {
					clk.onTime = nil
					c.Set("k", 5, time.Time{})
					stored = clk.reads.Load()
				}

				_, err = c.GetOrLoad(t.Context(), "k", func(context.Context, string) (int, time.Time, error) {
					return 7, time.Time{}, nil
				})
				assert.NoError(t, err, "a hit under the lock must not fail")
				assert.Equal(t, clk.reads.Load(), stored, "GetOrLoad must not read the clock after the Set")
			})

		t.Run("loads a value in place of an expired entry that a Set stored after the lookup of the caller missed",
			func(t *testing.T) {
				t.Parallel()
				clk := &countingClock{Clock: fake.New(origin)}
				c, err := cache.New(cache.Config[string, int]{Clock: clk, Capacity: 10})
				assert.NoError(t, err, "New must accept the configuration")
				c.Set("k", 1, origin)
				clk.onTime = func() {
					clk.onTime = nil
					c.Set("k", 5, origin)
				}

				v, err := c.GetOrLoad(t.Context(), "k", func(context.Context, string) (int, time.Time, error) {
					return 7, time.Time{}, nil
				})
				assert.NoError(t, err, "the load must not fail")
				assert.Equal(t, v, 7, "an expired entry must not count as a hit")
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
			expect.Equal(t, r.got(), []string{"k"}, "Evicted must receive the expired entry")
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
			assert.Equal(t, r.got(), []string{"k"}, "Evicted must receive the replaced value")
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
			expect.Equal(t, r.got(), []string{"k", "k"}, "Evicted must receive both values of the key")
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

		t.Run("evicts an entry that a Set kept past the capacity", func(t *testing.T) {
			t.Parallel()
			c, _, r := newCache(t, 2, nil)
			setAll(c, "a", "b")
			a, _ := c.Pin("a")
			defer a.Unpin()
			b, _ := c.Pin("b")
			defer b.Unpin()
			c.Set("c", 1, time.Time{})
			c.Set("d", 1, time.Time{})
			expect.Equal(t, c.Len(), 3, "a Set into a pinned cache must keep its own entry")
			expect.Equal(t, r.got(), []string{"c"}, "the Set must evict the unpinned entry that the last Set kept")
		})

		t.Run("stores an entry whose expiry time has passed as a miss", func(t *testing.T) {
			t.Parallel()
			c, _, _ := newCache(t, 10, nil)
			c.Set("k", 7, origin.Add(-time.Second))
			_, ok := c.Get("k")
			assert.False(t, ok, "an entry stored after its expiry time must be a miss")
		})

		t.Run("stores a value without an expiry time in a Cache without a Clock", func(t *testing.T) {
			t.Parallel()
			c, err := cache.New(cache.Config[string, int]{Capacity: 10})
			assert.NoError(t, err, "New must accept a Config without a Clock")
			c.Set("k", 7, time.Time{})
			v, ok := c.Get("k")
			assert.True(t, ok, "Get must find the stored key")
			assert.Equal(t, v, 7, "Get must return the stored value")
		})

		t.Run("evicts the oldest entries without hits in a Cache without a Clock", func(t *testing.T) {
			t.Parallel()
			r := &recorder{}
			c, err := cache.New(cache.Config[string, int]{Capacity: 3, Evicted: r.evicted})
			assert.NoError(t, err, "New must accept a Config without a Clock")
			setAll(c, "a", "b", "c", "d", "e")
			assert.Equal(t, r.got(), []string{"a", "b"}, "the oldest entries must leave first")
		})

		t.Run("panics for an expiry time in a Cache without a Clock", func(t *testing.T) {
			t.Parallel()
			c, err := cache.New(cache.Config[string, int]{Capacity: 10})
			assert.NoError(t, err, "New must accept a Config without a Clock")
			assert.Panics(t, func() { c.Set("k", 7, origin.Add(time.Second)) },
				"Set must refuse an expiry time in a cache that cannot expire its entries")
		})

		t.Run("leaves the Cache unchanged when it panics for an expiry time", func(t *testing.T) {
			t.Parallel()
			r := &recorder{}
			c, err := cache.New(cache.Config[string, int]{Capacity: 10, Evicted: r.evicted})
			assert.NoError(t, err, "New must accept a Config without a Clock")
			c.Set("k", 1, time.Time{})
			assert.Panics(t, func() { c.Set("k", 2, origin.Add(time.Second)) },
				"Set must refuse an expiry time in a cache that cannot expire its entries")
			v, ok := c.Get("k")
			expect.True(t, ok, "the entry of the key must remain")
			expect.Equal(t, v, 1, "the entry must keep the value of the first Set")
			expect.Empty(t, r.got(), "the panic must not pass an entry to Evicted")
		})

		t.Run("passes the replaced entry to Evicted before the entries that it evicts", func(t *testing.T) {
			t.Parallel()
			c, _, r := newCache(t, 3, func(v int) int64 { return int64(v) })
			c.Set("a", 1, time.Time{})
			c.Set("b", 1, time.Time{})
			c.Set("c", 1, time.Time{})
			c.Set("c", 2, time.Time{})
			assert.Equal(t, r.got(), []string{"c", "a"}, "Evicted must receive the replaced value first")
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

	t.Run("SetPinned", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the entry that it stores", func(t *testing.T) {
			t.Parallel()
			c, _, _ := newCache(t, 10, nil)
			p := c.SetPinned("k", 7, time.Time{})
			defer p.Unpin()
			expect.Equal(t, p.Value(), 7, "the Pinned must contain the stored value")
			v, _ := c.Get("k")
			expect.Equal(t, v, 7, "Get must return the stored value")
		})

		t.Run("keeps its entry through evictions while it is pinned", func(t *testing.T) {
			t.Parallel()
			c, _, r := newCache(t, 1, nil)
			p := c.SetPinned("k", 7, time.Time{})
			defer p.Unpin()
			setAll(c, "a", "b", "c")
			_, ok := c.Get("k")
			expect.True(t, ok, "the pinned entry must remain in the cache")
			expect.NotContains(t, r.got(), "k", "Evicted must not receive the pinned entry")
		})

		t.Run("lets the cache evict its entry after Unpin", func(t *testing.T) {
			t.Parallel()
			c, _, r := newCache(t, 1, nil)
			p := c.SetPinned("k", 7, time.Time{})
			setAll(c, "a")
			p.Unpin()
			setAll(c, "b")
			_, ok := c.Get("k")
			expect.False(t, ok, "the unpinned entry must leave like any other")
			expect.Contains(t, r.got(), "k", "Evicted must receive the unpinned entry")
		})

		t.Run("pins its entry before it passes the replaced entry to Evicted", func(t *testing.T) {
			t.Parallel()
			var c *cache.Cache[string, int]
			c, err := cache.New(cache.Config[string, int]{
				Clock:    fake.New(origin),
				Capacity: 2,
				Evicted: func(k string, v int) {
					if k == "k" && v == 1 {
						setAll(c, "a", "b", "c")
					}
				},
			})
			assert.NoError(t, err, "New must accept the configuration")
			c.Set("k", 1, time.Time{})
			p := c.SetPinned("k", 2, time.Time{})
			defer p.Unpin()
			v, ok := c.Get("k")
			assert.True(t, ok, "the Sets inside Evicted must not evict the new entry")
			assert.Equal(t, v, 2, "Get must return the value of SetPinned")
		})

		t.Run("counts no hit on its entry", func(t *testing.T) {
			t.Parallel()
			c, _, r := newCache(t, 2, nil)
			c.SetPinned("a", 1, time.Time{}).Unpin()
			setAll(c, "b", "c")
			assert.Equal(t, r.got(), []string{"a"}, "an entry without a hit must leave at its first examination")
		})

		t.Run("returns a Pinned of a value whose cost exceeds the capacity", func(t *testing.T) {
			t.Parallel()
			c, _, _ := newCache(t, 10, func(v int) int64 { return int64(v) })
			p := c.SetPinned("k", 11, time.Time{})
			defer p.Unpin()
			assert.Equal(t, p.Value(), 11, "the Pinned must contain the value above the capacity")
		})

		t.Run("removes the entry of its key for a value whose cost exceeds the capacity", func(t *testing.T) {
			t.Parallel()
			c, _, r := newCache(t, 10, func(v int) int64 { return int64(v) })
			c.Set("k", 1, time.Time{})
			p := c.SetPinned("k", 11, time.Time{})
			defer p.Unpin()
			_, ok := c.Get("k")
			expect.False(t, ok, "a value above the capacity must not be stored")
			expect.Equal(t, r.got(), []string{"k"}, "Evicted must receive the replaced value")
			expect.Equal(t, c.Cost(), int64(0), "the refused value must not count toward the cost")
		})

		t.Run("passes a value whose cost exceeds the capacity to Evicted at its Unpin", func(t *testing.T) {
			t.Parallel()
			c, _, r := newCache(t, 10, func(v int) int64 { return int64(v) })
			p := c.SetPinned("k", 11, time.Time{})
			assert.Empty(t, r.got(), "the refused value must wait for its Unpin")
			p.Unpin()
			assert.Equal(t, r.got(), []string{"k"}, "the Unpin must pass the refused value to Evicted once")
		})

		t.Run("panics for an expiry time in a Cache without a Clock", func(t *testing.T) {
			t.Parallel()
			c, err := cache.New(cache.Config[string, int]{Capacity: 10})
			assert.NoError(t, err, "New must accept a Config without a Clock")
			assert.Panics(t, func() { c.SetPinned("k", 7, origin.Add(time.Second)) },
				"SetPinned must refuse an expiry time in a cache that cannot expire its entries")
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
			expect.Equal(t, r.got(), []string{"k"}, "Evicted must receive the deleted entry")
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
			expect.Equal(t, r.got(), []string{"k"}, "Evicted must receive the deleted entry")
		})

		t.Run("calls no Evicted when the Config sets none", func(t *testing.T) {
			t.Parallel()
			c, err := cache.New(cache.Config[string, int]{Clock: fake.New(origin), Capacity: 10})
			assert.NoError(t, err, "New must accept the configuration")
			c.Set("k", 7, time.Time{})
			assert.NotPanics(t, func() { c.Delete("k") }, "Delete must skip a nil Evicted")
		})
	})

	t.Run("Expire", func(t *testing.T) {
		t.Parallel()

		t.Run("removes the entries whose expiry time has passed", func(t *testing.T) {
			t.Parallel()
			c, clk, r := newCache(t, 10, nil)
			c.Set("a", 1, origin.Add(time.Second))
			c.Set("b", 1, origin.Add(2*time.Second))
			c.Set("c", 1, time.Time{})
			clk.Advance(time.Second)
			expect.Equal(t, c.Expire(), 1, "Expire must return the number of entries that it removed")
			expect.Equal(t, c.Len(), 2, "the expired entry must leave the cache")
			expect.Equal(t, r.got(), []string{"a"}, "Evicted must receive the expired entry")
		})

		t.Run("keeps an entry one nanosecond before its expiry time", func(t *testing.T) {
			t.Parallel()
			c, clk, r := newCache(t, 10, nil)
			c.Set("a", 1, origin.Add(time.Second))
			clk.Advance(time.Second - time.Nanosecond)
			expect.Equal(t, c.Expire(), 0, "Expire must keep an entry before its expiry time")
			expect.Empty(t, r.got(), "Evicted must not receive an entry")
		})

		t.Run("keeps an entry without an expiry time at any time", func(t *testing.T) {
			t.Parallel()
			c, clk, _ := newCache(t, 10, nil)
			c.Set("a", 1, time.Time{})
			clk.Advance(1000 * time.Hour)
			assert.Equal(t, c.Expire(), 0, "the zero expiry time must never expire")
		})

		t.Run("returns 0 for a Cache without a Clock", func(t *testing.T) {
			t.Parallel()
			c, err := cache.New(cache.Config[string, int]{Capacity: 10})
			assert.NoError(t, err, "New must accept a Config without a Clock")
			c.Set("a", 1, time.Time{})
			expect.Equal(t, c.Expire(), 0, "a cache without a Clock must expire nothing")
			expect.Equal(t, c.Len(), 1, "the entry must remain in the cache")
		})

		t.Run("passes the removed entries to Evicted in the order of their expiry times", func(t *testing.T) {
			t.Parallel()
			c, clk, r := newCache(t, 10, nil)
			c.Set("late", 1, origin.Add(3*time.Second))
			c.Set("early", 1, origin.Add(time.Second))
			c.Set("middle", 1, origin.Add(2*time.Second))
			clk.Advance(3 * time.Second)
			c.Expire()
			assert.Equal(t, r.got(), []string{"early", "middle", "late"},
				"Evicted must receive the entries in the order of their expiry times")
		})

		t.Run("removes more than eight expired entries in one call", func(t *testing.T) {
			t.Parallel()
			c, clk, r := newCache(t, 30, nil)
			keys := names("k", 20)
			for i, k := range keys {
				c.Set(k, 1, origin.Add(time.Duration(i+1)*time.Second))
			}
			clk.Advance(20 * time.Second)
			expect.Equal(t, c.Expire(), 20, "Expire must remove every expired entry")
			expect.Equal(t, r.got(), keys, "Evicted must receive every removed entry in the order of its expiry time")
		})

		t.Run("passes a pinned entry to Evicted at its Unpin", func(t *testing.T) {
			t.Parallel()
			c, clk, r := newCache(t, 10, nil)
			c.Set("k", 7, origin.Add(time.Second))
			p, _ := c.Pin("k")
			clk.Advance(time.Second)
			expect.Equal(t, c.Expire(), 1, "Expire must remove a pinned entry")
			expect.Empty(t, r.got(), "a pinned entry must wait for its Unpin")
			p.Unpin()
			expect.Equal(t, r.got(), []string{"k"}, "the Unpin must pass the entry to Evicted")
		})

		t.Run("does not remove an entry that a Set replaced", func(t *testing.T) {
			t.Parallel()
			c, clk, r := newCache(t, 10, nil)
			c.Set("k", 1, origin.Add(time.Second))
			c.Set("k", 2, origin.Add(time.Hour))
			clk.Advance(time.Second)
			expect.Equal(t, c.Expire(), 0, "the replaced entry must not leave the cache again")
			expect.Equal(t, r.got(), []string{"k"}, "Evicted must receive the replaced entry once")
		})

		t.Run("does not remove an entry that Delete removed", func(t *testing.T) {
			t.Parallel()
			c, clk, r := newCache(t, 10, nil)
			c.Set("k", 1, origin.Add(time.Second))
			c.Delete("k")
			clk.Advance(time.Second)
			expect.Equal(t, c.Expire(), 0, "the deleted entry must not leave the cache again")
			expect.Equal(t, r.got(), []string{"k"}, "Evicted must receive the deleted entry once")
		})

		t.Run("does not remove an entry that a Get removed", func(t *testing.T) {
			t.Parallel()
			c, clk, r := newCache(t, 10, nil)
			c.Set("k", 1, origin.Add(time.Second))
			clk.Advance(time.Second)
			c.Get("k")
			expect.Equal(t, c.Expire(), 0, "the entry that the Get removed must not leave the cache again")
			expect.Equal(t, r.got(), []string{"k"}, "Evicted must receive the expired entry once")
		})

		t.Run("does not remove an entry that the evictor removed", func(t *testing.T) {
			t.Parallel()
			c, clk, r := newCache(t, 1, nil)
			c.Set("a", 1, origin.Add(time.Second))
			c.Set("b", 1, origin.Add(time.Second))
			clk.Advance(time.Second)
			expect.Equal(t, c.Expire(), 1, "Expire must remove only the entry that the cache kept")
			expect.Equal(t, r.got(), []string{"a", "b"}, "Evicted must receive each entry once")
		})

		t.Run("calls Evicted after it releases its lock", func(t *testing.T) {
			t.Parallel()
			var c *cache.Cache[string, int]
			var deleted atomic.Bool
			clk := fake.New(origin)
			c, err := cache.New(cache.Config[string, int]{
				Clock:    clk,
				Capacity: 10,
				Evicted:  func(string, int) { deleted.Store(!c.Delete("other")) },
			})
			assert.NoError(t, err, "New must accept the configuration")
			c.Set("a", 1, origin.Add(time.Second))
			clk.Advance(time.Second)
			assert.CompletesWithin(t, time.Second, func(context.Context) error {
				c.Expire()

				return nil
			}, "a Delete inside Evicted must not wait for the lock of Expire")
			assert.True(t, deleted.Load(), "the Delete inside Evicted must run")
		})

		t.Run("removes exactly the entries whose expiry time has passed after any calls", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Expire must remove exactly the entries whose expiry time has passed", func(pc *prop.Case) {
				clk := fake.New(origin)
				evicted := 0
				c, err := cache.New(cache.Config[int, int]{
					Clock:    clk,
					Capacity: workloadKeys,
					Evicted:  func(int, int) { evicted++ },
				})
				assert.NoError(pc, err, "New must accept the configuration")
				stateful.Steps(pc, stateful.Machine[struct{}]{Actions: expiryActions(c, clk, &evicted)})
			})
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

	t.Run("Capacity", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the Capacity of its Config", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Capacity must return the Capacity of the Config", func(pc *prop.Case) {
				capacity := pc.Draw(prop.Integer[int64](1, math.MaxInt64), "capacity")
				c, err := cache.New(cache.Config[string, int]{Capacity: capacity})
				assert.NoError(pc, err, "New must accept a positive Capacity")
				assert.Equal(pc, c.Capacity(), capacity, "Capacity must return the Capacity of the Config")
			})
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
				Actions: append(cacheActions(c, &sets), loadAction(c)),
			}, stateful.Clients(clients-1))
		})
	})

	t.Run("exceeds the capacity only while pinned entries fill it under any sequence of calls", func(t *testing.T) {
		t.Parallel()
		prop.ForAll(t, "the cost must exceed the capacity only while pinned entries fill the cache", pinSteps)
	})

	t.Run("passes each value that leaves the cache to Evicted once", func(t *testing.T) {
		t.Parallel()
		prop.ForAll(t, "Evicted must receive once each stored value that leaves the cache", func(pc *prop.Case) {
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
						"Evicted must receive once each stored value that leaves the cache")
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

	t.Run("GetOrLoad", func(t *testing.T) {
		c := benchCache(t, keys[:benchCapacity])
		load := func(context.Context, string) (int, time.Time, error) { return 2, time.Time{}, nil }
		ctx := t.Context()

		var hit, miss int
		var hitErr, missErr error
		expect.MaxAllocs(t, func() { hit, hitErr = c.GetOrLoad(ctx, "k500", load) }, 0,
			"a GetOrLoad of a hit must not allocate")
		expect.MaxAllocsWithSetup(t, func() string {
			c.Delete("loaded")

			return "loaded"
		}, func(k string) { miss, missErr = c.GetOrLoad(ctx, k, load) }, getOrLoadMissAllocs,
			"a GetOrLoad of a miss must allocate the load, its goroutine and the entry")
		expect.NoError(t, hitErr, "the test must measure a hit that does not fail")
		expect.Equal(t, hit, 1, "the test must measure a hit")
		expect.NoError(t, missErr, "the test must measure a load that does not fail")
		expect.Equal(t, miss, 2, "the test must measure a miss")
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

		filled := pinnedCache(t, keys[:benchCapacity])
		j := 0
		expect.MaxAllocs(t, func() {
			filled.Set(keys[benchCapacity+j%benchCapacity], j, time.Time{})
			j++
		}, 1, "a Set of a new key while pinned entries fill the cache must allocate the entry")
		assert.Equal(t, filled.Cost(), int64(benchCapacity+1), "the test must measure Sets past the capacity")
	})

	t.Run("SetPinned", func(t *testing.T) {
		full := benchCache(t, keys[:benchCapacity])
		expect.MaxAllocs(t, func() { full.SetPinned("k500", 1, time.Time{}).Unpin() }, 1,
			"a SetPinned that replaces an entry must allocate the entry")
		assert.Equal(t, full.Len(), benchCapacity, "the test must measure SetPinned calls that replace")

		evicting := benchCache(t, keys)
		i := 0
		expect.MaxAllocs(t, func() {
			evicting.SetPinned(keys[i%len(keys)], i, time.Time{}).Unpin()
			i++
		}, 1, "a SetPinned of a new key that evicts an entry must allocate the entry")
		assert.Equal(t, evicting.Len(), benchCapacity, "the test must measure SetPinned calls that evict")
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

	t.Run("Expire", func(t *testing.T) {
		clk := fake.New(origin)
		c, err := cache.New(cache.Config[string, int]{Clock: clk, Capacity: benchCapacity})
		assert.NoError(t, err, "New must accept the configuration")
		var n int
		expect.MaxAllocsWithSetup(t, func() struct{} {
			for _, k := range keys[:20] {
				c.Set(k, 1, clk.Time().Add(time.Nanosecond))
			}
			clk.Advance(time.Nanosecond)

			return struct{}{}
		}, func(struct{}) { n = c.Expire() }, 0, "an Expire of twenty entries must not allocate")
		assert.Equal(t, n, 20, "the test must measure the removal of twenty entries")
		expect.MaxAllocs(t, func() { n = c.Expire() }, 0, "an Expire without an expired entry must not allocate")
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

	t.Run("Capacity", func(t *testing.T) {
		c := benchCache(t, keys[:benchCapacity])
		var capacity int64
		expect.MaxAllocs(t, func() { capacity = c.Capacity() }, 0, "Capacity must not allocate")
		assert.Equal(t, capacity, int64(benchCapacity), "the test must measure the Capacity of the Config")
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

	b.Run("GetOrLoad", func(b *testing.B) {
		load := func(context.Context, string) (int, time.Time, error) { return 2, time.Time{}, nil }

		b.Run("of a hit", func(b *testing.B) {
			c := benchCache(b, keys[:benchCapacity])
			ctx := b.Context()

			var (
				v   int
				err error
			)

			bc := bench.Start(b).MaxAllocs(0)
			defer bc.End()

			for bc.Loop() {
				v, err = c.GetOrLoad(ctx, "k500", load)
			}

			assert.NoError(b, err, "the benchmark must measure a hit that does not fail")
			assert.Equal(b, v, 1, "the benchmark must measure a hit")
		})

		b.Run("of a miss that starts a load", func(b *testing.B) {
			c := benchCache(b, keys[:benchCapacity])
			ctx := b.Context()

			var (
				v   int
				err error
			)

			bc := bench.Start(b).MaxAllocs(getOrLoadMissAllocs)
			defer bc.End()

			for bc.Loop() {
				bc.Excluding(func() { c.Delete("loaded") })
				v, err = c.GetOrLoad(ctx, "loaded", load)
			}

			assert.NoError(b, err, "the benchmark must measure a load that does not fail")
			assert.Equal(b, v, 2, "the benchmark must measure a miss")
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

		b.Run("of a new key while pinned entries fill the cache", func(b *testing.B) {
			c := pinnedCache(b, keys[:benchCapacity])
			i := 0

			bc := bench.Start(b).MaxAllocs(1)
			defer bc.End()

			for bc.Loop() {
				c.Set(keys[benchCapacity+i%benchCapacity], i, time.Time{})
				i++
			}

			assert.Equal(b, c.Cost(), int64(benchCapacity+1), "the benchmark must measure Sets past the capacity")
		})

		b.Run("of a key with an expiry time that replaces its entry", func(b *testing.B) {
			c := benchCache(b, keys[:benchCapacity])
			expires := origin.Add(time.Hour)

			bc := bench.Start(b).MaxAllocs(1)
			defer bc.End()

			for bc.Loop() {
				c.Set("k500", 1, expires)
			}

			assert.Equal(b, c.Len(), benchCapacity, "the benchmark must measure a full cache")
		})
	})

	b.Run("SetPinned", func(b *testing.B) {
		b.Run("of a key that replaces its entry followed by its Unpin", func(b *testing.B) {
			c := benchCache(b, keys[:benchCapacity])

			bc := bench.Start(b).MaxAllocs(1)
			defer bc.End()

			for bc.Loop() {
				c.SetPinned("k500", 1, time.Time{}).Unpin()
			}

			assert.Equal(b, c.Len(), benchCapacity, "the benchmark must measure a full cache")
		})

		b.Run("of a new key that evicts an entry followed by its Unpin", func(b *testing.B) {
			c := benchCache(b, keys)
			i := 0

			bc := bench.Start(b).MaxAllocs(1)
			defer bc.End()

			for bc.Loop() {
				c.SetPinned(keys[i%len(keys)], i, time.Time{}).Unpin()
				i++
			}

			assert.Equal(b, c.Len(), benchCapacity, "the benchmark must measure SetPinned calls that evict")
		})
	})

	b.Run("Expire", func(b *testing.B) {
		b.Run("of eight expired entries", func(b *testing.B) {
			clk := fake.New(origin)
			c, err := cache.New(cache.Config[string, int]{Clock: clk, Capacity: benchCapacity})
			assert.NoError(b, err, "New must accept the configuration")
			n := 0

			bc := bench.Start(b).MaxAllocs(0)
			defer bc.End()

			for bc.Loop() {
				bc.Excluding(func() {
					for _, k := range keys[:8] {
						c.Set(k, 1, clk.Time().Add(time.Nanosecond))
					}
					clk.Advance(time.Nanosecond)
				})
				n = c.Expire()
			}

			assert.Equal(b, n, 8, "the benchmark must measure the removal of eight entries")
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

// pinnedCache returns a Cache of benchCapacity without Cost and without
// Evicted, after a SetPinned of 1 under each key of keys whose pin it never
// ends. Pinned entries fill the cache once keys has benchCapacity keys, so
// each Set of a new key stores its entry past the capacity.
func pinnedCache(tb testing.TB, keys []string) *cache.Cache[string, int] {
	tb.Helper()
	c, err := cache.New(cache.Config[string, int]{Clock: fake.New(origin), Capacity: benchCapacity})
	assert.NoError(tb, err, "New must accept the configuration")
	for _, k := range keys {
		c.SetPinned(k, 1, time.Time{})
	}

	return c
}

// setAll stores 1 under each key, in order, without an expiry time.
func setAll(c *cache.Cache[string, int], keys ...string) {
	for _, k := range keys {
		c.Set(k, 1, time.Time{})
	}
}

// startGetOrLoad calls c.GetOrLoad(ctx, k, load) on a goroutine of its own,
// and returns a channel with room for its result.
func startGetOrLoad(
	ctx context.Context,
	c *cache.Cache[string, int],
	k string,
	load func(context.Context, string) (int, time.Time, error),
) <-chan loaded {
	got := make(chan loaded, 1)
	go func() {
		v, err := c.GetOrLoad(ctx, k, load)
		got <- loaded{v: v, err: err}
	}()

	return got
}

// names returns the keys prefix0 to prefix<n-1>.
func names(prefix string, n int) []string {
	keys := make([]string, n)
	for i := range keys {
		keys[i] = prefix + strconv.Itoa(i)
	}

	return keys
}

// cacheActions returns the actions of a machine over c. Each action calls c
// with a key below workloadKeys. The actions are a Set of a value that no
// call before it stored, a SetPinned of such a value with its Unpin, a Get,
// a Pin with its Unpin, and a Delete. Each action records its call in the
// history of its case. The key is the first argument of each call, and a
// miss records absent. sets counts the calls of Set and SetPinned.
func cacheActions(c *cache.Cache[int, int], sets *atomic.Int64) []stateful.Action[keyValues] {
	written := 0
	key := func(pc *prop.Case, _ keyValues) any { return pc.Draw(prop.Integer(0, workloadKeys-1), "key") }
	value := func(pc *prop.Case, _ keyValues) any {
		written++

		return [2]int{pc.Draw(prop.Integer(0, workloadKeys-1), "key"), written}
	}

	return []stateful.Action[keyValues]{
		{
			Name:  opSet,
			Input: value,
			Run: func(pc *prop.Case, client int, in any) {
				kv := in.([2]int)
				call := pc.History().Invoke(client, opSet, []any{kv[0], kv[1]})
				c.Set(kv[0], kv[1], time.Time{})
				call.OK(nil)
				sets.Add(1)
			},
		},
		{
			Name:  opSetPinned,
			Input: value,
			Run: func(pc *prop.Case, client int, in any) {
				kv := in.([2]int)
				call := pc.History().Invoke(client, opSetPinned, []any{kv[0], kv[1]})
				p := c.SetPinned(kv[0], kv[1], time.Time{})
				v := p.Value()
				p.Unpin()
				call.OK(nil)
				sets.Add(1)
				assert.Equal(pc, v, kv[1], "the Pinned of SetPinned must contain its value")
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

// loadAction returns an action of the machine of concurrent calls over c: a
// GetOrLoad of a key below workloadKeys, whose load returns a value from
// loadBase on that no load before it returned. The action records its call
// in the history of its case, with the key as its argument.
func loadAction(c *cache.Cache[int, int]) stateful.Action[keyValues] {
	var loads atomic.Int64
	load := func(context.Context, int) (int, time.Time, error) {
		return loadBase + int(loads.Add(1)), time.Time{}, nil
	}

	return stateful.Action[keyValues]{
		Name:  opGetOrLoad,
		Input: func(pc *prop.Case, _ keyValues) any { return pc.Draw(prop.Integer(0, workloadKeys-1), "key") },
		Run: func(pc *prop.Case, client int, in any) {
			call := pc.History().Invoke(client, opGetOrLoad, []any{in})
			v, err := c.GetOrLoad(pc.Context(), in.(int), load)
			assert.NoError(pc, err, "a load of the workload must not fail")
			call.OK(v)
		},
	}
}

// openPin is a pin that no Unpin of the machine of pins has ended, with the
// key and the version of its entry.
type openPin struct {
	p       cache.Pinned[int, int]
	key     int
	version int
}

// pinSteps runs a machine of Set, SetPinned, Pin, Unpin, Get and Delete on a
// Cache of pinCapacity, whose pins remain open across steps until an Unpin
// ends them. After every step it checks that the cost exceeds the capacity
// only while pinned entries fill the cache: past the capacity at most one
// entry lacks a pin, the entry of the last Set, and without an open pin the
// cost fits the capacity.
//
// The machine counts the entries of each key in version. An entry with an
// open pin is in the cache while its version is the version of its key,
// because the evictor keeps it and only a Set or a Delete of the key removes
// it.
func pinSteps(pc *prop.Case) {
	c, err := cache.New(cache.Config[int, int]{Capacity: pinCapacity})
	assert.NoError(pc, err, "New must accept the configuration")

	var open []openPin
	var version [pinKeys]int
	pinned := map[[2]int]int{}
	key := func(pc *prop.Case, _ struct{}) any { return pc.Draw(prop.Integer(0, pinKeys-1), "key") }

	stateful.Steps(pc, stateful.Machine[struct{}]{
		Actions: []stateful.Action[struct{}]{
			{
				Name:  opSet,
				Input: key,
				Run: func(_ *prop.Case, _ int, in any) {
					k := in.(int)
					c.Set(k, k, time.Time{})
					version[k]++
				},
			},
			{
				Name:  opSetPinned,
				Input: key,
				Run: func(_ *prop.Case, _ int, in any) {
					k := in.(int)
					version[k]++
					open = append(open, openPin{p: c.SetPinned(k, k, time.Time{}), key: k, version: version[k]})
					pinned[[2]int{k, version[k]}]++
				},
			},
			{
				Name:  opPin,
				Input: key,
				Run: func(_ *prop.Case, _ int, in any) {
					k := in.(int)
					if p, ok := c.Pin(k); ok {
						open = append(open, openPin{p: p, key: k, version: version[k]})
						pinned[[2]int{k, version[k]}]++
					}
				},
			},
			{
				Name:   opUnpin,
				Weight: 2,
				Input:  func(pc *prop.Case, _ struct{}) any { return pc.Draw(prop.Integer(0, pinDraws-1), "pin") },
				Run: func(_ *prop.Case, _ int, in any) {
					if len(open) == 0 {
						return
					}
					i := in.(int) % len(open)
					o := open[i]
					open = slices.Delete(open, i, i+1)
					pinned[[2]int{o.key, o.version}]--
					o.p.Unpin()
				},
			},
			{
				Name:  opGet,
				Input: key,
				Run:   func(_ *prop.Case, _ int, in any) { c.Get(in.(int)) },
			},
			{
				Name:  opDelete,
				Input: key,
				Run: func(_ *prop.Case, _ int, in any) {
					k := in.(int)
					c.Delete(k)
					version[k]++
				},
			},
		},
		Invariant: func(pc *prop.Case, _ struct{}) {
			withPins := 0
			for k, v := range version {
				if pinned[[2]int{k, v}] > 0 {
					withPins++
				}
			}
			if c.Cost() > pinCapacity {
				assert.InRange(pc, c.Len()-withPins, 0, 1,
					"past the capacity at most the entry of the last Set may lack a pin")
			}
			if len(open) == 0 {
				assert.InRange(pc, c.Cost(), 0, pinCapacity, "the cost must fit the capacity once every pin has ended")
			}
		},
	})
}

// stepCache returns the state of a Cache of the workload after op. The
// state of a key is its value, and absent for a key without an entry. A
// SetPinned changes the state as a Set does. An eviction removes an entry
// without a call, so a miss and a Delete that reports false are allowed in
// every state. A GetOrLoad returns the value of its key, or the value of a
// load from loadBase on, which then becomes the value of the key. An
// eviction allows a load in every state.
func stepCache(state keyValues, op history.Operation) []keyValues {
	k := op.Args[0].(int)
	if op.Name == opGetOrLoad {
		if !op.Known || state[k] != absent && op.Returned(state[k]) {
			return []keyValues{state}
		}
		if v, ok := op.Output.(int); ok && v >= loadBase {
			state[k] = v

			return []keyValues{state}
		}

		return nil
	}
	if op.Name == opSet || op.Name == opSetPinned {
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

// expiryActions returns the actions of a machine over c, whose capacity
// fits an entry of every key below workloadKeys, so no entry leaves it by
// eviction. The actions are a Set without an expiry time or with one of 0
// to 3 seconds after the clock, a Delete, an advance of clk by 0 to 2
// seconds, and an Expire. They keep the expiry time of each key that c
// contains, and the Expire checks its result against them. evicted counts
// the calls of Evicted.
func expiryActions(c *cache.Cache[int, int], clk *fake.Clock, evicted *int) []stateful.Action[struct{}] {
	expires := map[int]time.Time{}

	return []stateful.Action[struct{}]{
		{
			Name: opSet,
			Input: func(pc *prop.Case, _ struct{}) any {
				return [2]int{pc.Draw(prop.Integer(0, workloadKeys-1), "key"), pc.Draw(prop.Integer(0, 4), "seconds")}
			},
			Run: func(_ *prop.Case, _ int, in any) {
				kv := in.([2]int)
				var at time.Time
				if kv[1] > 0 {
					at = clk.Time().Add(time.Duration(kv[1]-1) * time.Second)
				}
				c.Set(kv[0], 1, at)
				expires[kv[0]] = at
			},
		},
		{
			Name:  opDelete,
			Input: func(pc *prop.Case, _ struct{}) any { return pc.Draw(prop.Integer(0, workloadKeys-1), "key") },
			Run: func(_ *prop.Case, _ int, in any) {
				c.Delete(in.(int))
				delete(expires, in.(int))
			},
		},
		{
			Name:  opAdvance,
			Input: func(pc *prop.Case, _ struct{}) any { return pc.Draw(prop.Integer(0, 2), "seconds") },
			Run: func(_ *prop.Case, _ int, in any) {
				seconds := in.(int)
				clk.Advance(time.Duration(seconds) * time.Second)
			},
		},
		{
			Name: opExpire,
			Run: func(pc *prop.Case, _ int, _ any) {
				now, want := clk.Time(), 0
				for k, at := range expires {
					if !at.IsZero() && !now.Before(at) {
						delete(expires, k)
						want++
					}
				}
				before := *evicted
				assert.Equal(pc, c.Expire(), want, "Expire must remove each entry whose expiry time has passed")
				assert.Equal(pc, *evicted-before, want, "Evicted must receive each entry that Expire removed")
				assert.Equal(pc, c.Len(), len(expires),
					"the cache must keep each entry whose expiry time has not passed")
			},
		},
	}
}
