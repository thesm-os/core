// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package idmap_test

import (
	"math"
	"sync/atomic"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/id"
	"go.thesmos.sh/core/idmap"
)

// The workload of the machine of concurrent calls: a map with a capacity of
// historyKeys keys over historyKeys keys, so no Store returns ErrFull.
const (
	historyKeys = 8

	// absent is the value that the machine records for a Load of a missing
	// key.
	absent = -1
)

// The workload of the check of the values that Load returns while writers
// change the map.
const (
	// churnKeys is the capacity of the map, and the number of its keys.
	churnKeys = 16

	// stableKeys is the number of keys that no writer deletes: the keys
	// below stableKeys.
	stableKeys = 8

	// churnRounds is the number of rounds of each client.
	churnRounds = 2000

	// valueStep is the step between the values of two consecutive keys.
	valueStep = 7919
)

// keyValues is the state of the spec of the machine of concurrent calls: the
// value of each key, and absent for a missing key.
type keyValues [historyKeys]int

func TestConcurrent(t *testing.T) {
	t.Parallel()

	t.Run("NewConcurrent", func(t *testing.T) {
		t.Parallel()

		t.Run("returns an empty map for a capacity below 1", func(t *testing.T) {
			t.Parallel()
			c := idmap.NewConcurrent[id.ID128, int64](0)
			_, ok := c.Load(keyOf[id.ID128](0))
			expect.Equal(t, c.Len(), 0, "the new map must have no keys")
			expect.False(t, ok, "the new map must not contain a key")
		})

		t.Run("returns a map with room for one key for a capacity below 1", func(t *testing.T) {
			t.Parallel()
			c := idmap.NewConcurrent[id.ID128, int64](-3)
			expect.NoError(t, c.Store(keyOf[id.ID128](0), 1), "the map must store one key")
			expect.ErrorIs(t, c.Store(keyOf[id.ID128](1), 2), idmap.ErrFull, "the map must refuse a second key")
		})
	})

	t.Run("Load", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the value that Store stored under the key", func(t *testing.T) {
			t.Parallel()
			c := idmap.NewConcurrent[id.ID160, int64](4)
			assert.NoError(t, c.Store(keyOf[id.ID160](1), 10), "Store must store the first key")
			assert.NoError(t, c.Store(keyOf[id.ID160](2), 20), "Store must store the second key")
			v, ok := c.Load(keyOf[id.ID160](2))
			expect.True(t, ok, "Load must find the key")
			expect.Equal(t, v, int64(20), "Load must return the value of the key")
		})

		t.Run("reports false for a key that the map does not contain", func(t *testing.T) {
			t.Parallel()
			c := idmap.NewConcurrent[id.ID160, int64](4)
			assert.NoError(t, c.Store(keyOf[id.ID160](1), 10), "Store must store the key")
			v, ok := c.Load(keyOf[id.ID160](2))
			expect.False(t, ok, "Load must not find a key that no Store stored")
			expect.Equal(t, v, int64(0), "Load must return the zero value for a missing key")
		})

		t.Run("returns every int32 value that Store stored", func(t *testing.T) {
			t.Parallel()
			c := idmap.NewConcurrent[id.ID128, int32](1)
			k := keyOf[id.ID128](1)
			prop.Equal(t, func(v int32) int32 {
				_ = c.Store(k, v)
				got, _ := c.Load(k)

				return got
			}, func(v int32) int32 { return v }, "Load must return the int32 value that Store stored",
				prop.Using(prop.Integer[int32](math.MinInt32, math.MaxInt32)), prop.Examples[int32](-1, math.MinInt32))
		})

		t.Run("returns every uint64 value that Store stored", func(t *testing.T) {
			t.Parallel()
			c := idmap.NewConcurrent[id.ID128, uint64](1)
			k := keyOf[id.ID128](1)
			prop.Equal(t, func(v uint64) uint64 {
				_ = c.Store(k, v)
				got, _ := c.Load(k)

				return got
			}, func(v uint64) uint64 { return v }, "Load must return the uint64 value that Store stored",
				prop.Using(prop.Integer[uint64](0, math.MaxUint64)), prop.Example[uint64](math.MaxUint64))
		})

		t.Run("returns the value of its key while writers change its buckets with ID128 keys", func(t *testing.T) {
			t.Parallel()
			checkOwnValues[id.ID128](t)
		})

		t.Run("returns the value of its key while writers change its buckets with ID256 keys", func(t *testing.T) {
			t.Parallel()
			checkOwnValues[id.ID256](t)
		})
	})

	t.Run("Store", func(t *testing.T) {
		t.Parallel()

		t.Run("returns ErrFull for a new key in a full map", func(t *testing.T) {
			t.Parallel()
			c := idmap.NewConcurrent[id.ID256, int64](2)
			assert.NoError(t, c.Store(keyOf[id.ID256](1), 10), "Store must store the first key")
			assert.NoError(t, c.Store(keyOf[id.ID256](2), 20), "Store must store the second key")
			err := c.Store(keyOf[id.ID256](3), 30)
			_, ok := c.Load(keyOf[id.ID256](3))
			expect.ErrorIs(t, err, idmap.ErrFull, "Store must refuse a new key in a full map")
			expect.Equal(t, errs.Classify(err), errs.Transient, "ErrFull must classify as Transient")
			expect.False(t, ok, "a refused Store must not store its key")
			expect.Equal(t, c.Len(), 2, "a refused Store must leave the map unchanged")
		})

		t.Run("replaces the value of a present key in a full map", func(t *testing.T) {
			t.Parallel()
			c := idmap.NewConcurrent[id.ID256, int64](1)
			assert.NoError(t, c.Store(keyOf[id.ID256](1), 10), "Store must store the key")
			err := c.Store(keyOf[id.ID256](1), 11)
			v, _ := c.Load(keyOf[id.ID256](1))
			expect.NoError(t, err, "Store must replace the value of a present key in a full map")
			expect.Equal(t, v, int64(11), "Store must store the new value")
		})

		t.Run("stores a new key once a Delete made room", func(t *testing.T) {
			t.Parallel()
			c := idmap.NewConcurrent[id.ID256, int64](1)
			assert.NoError(t, c.Store(keyOf[id.ID256](1), 10), "Store must store the first key")
			assert.True(t, c.Delete(keyOf[id.ID256](1)), "Delete must remove the first key")
			err := c.Store(keyOf[id.ID256](2), 20)
			v, _ := c.Load(keyOf[id.ID256](2))
			expect.NoError(t, err, "Store must store a new key in the room that Delete made")
			expect.Equal(t, v, int64(20), "Load must find the new key")
		})
	})

	t.Run("Delete", func(t *testing.T) {
		t.Parallel()

		t.Run("reports true for a present key", func(t *testing.T) {
			t.Parallel()
			c := idmap.NewConcurrent[id.ID128, int64](4)
			assert.NoError(t, c.Store(keyOf[id.ID128](1), 10), "Store must store the key")
			ok := c.Delete(keyOf[id.ID128](1))
			_, found := c.Load(keyOf[id.ID128](1))
			expect.True(t, ok, "Delete must report a present key as present")
			expect.False(t, found, "Load must not find a deleted key")
		})

		t.Run("reports false for a key that the map does not contain", func(t *testing.T) {
			t.Parallel()
			c := idmap.NewConcurrent[id.ID128, int64](4)
			assert.NoError(t, c.Store(keyOf[id.ID128](1), 10), "Store must store the key")
			expect.False(t, c.Delete(keyOf[id.ID128](2)), "Delete must report a missing key as not present")
			expect.Equal(t, c.Len(), 1, "Delete of a missing key must not remove another key")
		})
	})

	t.Run("Len", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the number of keys", func(t *testing.T) {
			t.Parallel()
			c := idmap.NewConcurrent[id.ID128, int64](4)
			assert.NoError(t, c.Store(keyOf[id.ID128](1), 10), "Store must store the first key")
			assert.NoError(t, c.Store(keyOf[id.ID128](2), 20), "Store must store the second key")
			assert.NoError(t, c.Store(keyOf[id.ID128](1), 11), "Store must replace the value of the first key")
			assert.Equal(t, c.Len(), 2, "Len must count each key once")
		})
	})

	t.Run("returns what a Go map returns for random calls with ID128 keys", func(t *testing.T) {
		t.Parallel()
		checkConcurrent[id.ID128](t)
	})

	t.Run("returns what a Go map returns for random calls with ID160 keys", func(t *testing.T) {
		t.Parallel()
		checkConcurrent[id.ID160](t)
	})

	t.Run("returns what a Go map returns for random calls with ID256 keys", func(t *testing.T) {
		t.Parallel()
		checkConcurrent[id.ID256](t)
	})

	t.Run("orders concurrent calls as one sequence of calls with ID128 keys", func(t *testing.T) {
		t.Parallel()
		checkHistory[id.ID128](t)
	})

	t.Run("orders concurrent calls as one sequence of calls with ID256 keys", func(t *testing.T) {
		t.Parallel()
		checkHistory[id.ID256](t)
	})
}

// TestConcurrentAllocs checks the allocation contract of a Concurrent.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
func TestConcurrentAllocs(t *testing.T) {
	keys := make([]id.ID128, 0, benchKeys+1)
	for i := range benchKeys + 1 {
		keys = append(keys, keyOf[id.ID128](i))
	}

	t.Run("NewConcurrent", func(t *testing.T) {
		var narrow *idmap.Concurrent[id.ID128, int64]
		var wide *idmap.Concurrent[id.ID256, int64]
		expect.MaxAllocs(t, func() { narrow = idmap.NewConcurrent[id.ID128, int64](benchKeys) }, 2,
			"NewConcurrent must allocate the map and its buckets")
		expect.MaxAllocs(t, func() { wide = idmap.NewConcurrent[id.ID256, int64](benchKeys) }, 3,
			"NewConcurrent of ID256 keys must allocate the map, its buckets and the words past 16 bytes")
		expect.Equal(t, narrow.Len(), 0, "the test must measure an empty map of ID128 keys")
		expect.Equal(t, wide.Len(), 0, "the test must measure an empty map of ID256 keys")
	})

	t.Run("Load", func(t *testing.T) {
		c := filledConcurrent(t, keys[:benchKeys])
		var hit, miss bool
		expect.MaxAllocs(t, func() { _, hit = c.Load(keys[500]) }, 0, "a Load of a present key must not allocate")
		expect.MaxAllocs(t, func() { _, miss = c.Load(keys[benchKeys]) }, 0,
			"a Load of a missing key must not allocate")
		expect.True(t, hit, "the test must measure a present key")
		expect.False(t, miss, "the test must measure a missing key")
	})

	t.Run("Store", func(t *testing.T) {
		c := filledConcurrent(t, keys[:benchKeys])
		var replaced, full error
		expect.MaxAllocs(t, func() { replaced = c.Store(keys[500], 1) }, 0,
			"a Store of a present key must not allocate")
		expect.MaxAllocs(t, func() { full = c.Store(keys[benchKeys], 1) }, 0,
			"a Store that returns ErrFull must not allocate")
		expect.NoError(t, replaced, "the test must measure a stored value")
		expect.ErrorIs(t, full, idmap.ErrFull, "the test must measure a full map")
	})

	t.Run("Delete", func(t *testing.T) {
		c := filledConcurrent(t, keys[:benchKeys])
		var deleted bool
		var stored error
		expect.MaxAllocs(t, func() {
			deleted = c.Delete(keys[500])
			stored = c.Store(keys[500], 500)
		}, 0, "a Delete and a Store at a constant size must not allocate")
		expect.True(t, deleted, "the test must measure a present key")
		expect.NoError(t, stored, "the test must measure a stored key")
	})

	t.Run("Len", func(t *testing.T) {
		c := filledConcurrent(t, keys[:benchKeys])
		var n int
		expect.MaxAllocs(t, func() { n = c.Len() }, 0, "Len must not allocate")
		expect.Equal(t, n, benchKeys, "the test must measure a full map")
	})
}

func BenchmarkConcurrent(b *testing.B) {
	keys := make([]id.ID128, 0, benchKeys)
	for i := range benchKeys {
		keys = append(keys, keyOf[id.ID128](i))
	}

	b.Run("Load", func(b *testing.B) {
		c := filledConcurrent(b, keys)

		m := bench.Start(b).MaxAllocs(0)
		defer m.End()

		var ok bool
		i := 0
		for m.Loop() {
			_, ok = c.Load(keys[i&(benchKeys-1)])
			i++
		}

		assert.True(b, ok, "the benchmark must measure present keys")
	})

	b.Run("Store", func(b *testing.B) {
		c := filledConcurrent(b, keys)

		m := bench.Start(b).MaxAllocs(0)
		defer m.End()

		var err error
		i := 0
		for m.Loop() {
			err = c.Store(keys[i&(benchKeys-1)], int64(i))
			i++
		}

		assert.NoError(b, err, "the benchmark must measure stored values")
	})

	b.Run("Delete", func(b *testing.B) {
		c := filledConcurrent(b, keys)

		m := bench.Start(b).MaxAllocs(0)
		defer m.End()

		var ok bool
		var err error
		i := 0
		for m.Loop() {
			k := keys[i&(benchKeys-1)]
			ok = c.Delete(k)
			err = c.Store(k, int64(i))
			i++
		}

		expect.True(b, ok, "the benchmark must measure present keys")
		expect.NoError(b, err, "the benchmark must measure stored keys")
	})
}

// checkConcurrent runs random calls on a Concurrent with keys of type K on
// one goroutine, over a key space of three times its capacity. It checks
// each result against a Go map that receives the same calls, and a Store of
// a new key into a full map against ErrFull.
func checkConcurrent[K idmap.Key](t *testing.T) {
	t.Helper()

	prop.ForAll(t, "a Concurrent must return what a Go map returns for the same calls", func(pc *prop.Case) {
		capacity := pc.Draw(prop.Integer(1, 16), "capacity")
		c := idmap.NewConcurrent[K, int64](capacity)
		ref := map[K]int64{}
		written := 0
		key := func(pc *prop.Case, _ struct{}) any {
			return keyOf[K](pc.Draw(prop.Integer(0, 3*capacity-1), "key"))
		}

		stateful.Steps(pc, stateful.Machine[struct{}]{
			Actions: []stateful.Action[struct{}]{
				{
					Name:  opLoad,
					Input: key,
					Run: func(pc *prop.Case, _ int, in any) {
						got, ok := c.Load(in.(K))
						want, present := ref[in.(K)]
						assert.Equal(pc, ok, present, "Load must report whether the key is present")
						assert.Equal(pc, got, want, "Load must return the value of the key")
					},
				},
				{
					Name: opStore,
					Input: func(pc *prop.Case, state struct{}) any {
						written++

						return entry[K]{key: key(pc, state).(K), value: written}
					},
					Run: func(pc *prop.Case, _ int, in any) {
						e := in.(entry[K])
						err := c.Store(e.key, int64(e.value))
						if _, present := ref[e.key]; !present && len(ref) == capacity {
							assert.ErrorIs(pc, err, idmap.ErrFull, "Store must refuse a new key in a full map")

							return
						}
						assert.NoError(pc, err, "Store must store a key that the map has room for")
						ref[e.key] = int64(e.value)
					},
				},
				{
					Name:  opDelete,
					Input: key,
					Run: func(pc *prop.Case, _ int, in any) {
						_, present := ref[in.(K)]
						delete(ref, in.(K))
						assert.Equal(pc, c.Delete(in.(K)), present, "Delete must report whether the key was present")
					},
				},
			},
			Invariant: func(pc *prop.Case, _ struct{}) {
				assert.Equal(pc, c.Len(), len(ref), "Len must return the number of keys")
			},
		})
	})
}

// checkHistory runs random calls on a Concurrent with keys of type K from
// concurrent clients, and checks that their history is linearizable against
// the spec of a map of historyKeys keys.
func checkHistory[K idmap.Key](t *testing.T) {
	t.Helper()

	prop.ForAll(t, "concurrent calls must linearize as calls on a Go map", func(pc *prop.Case) {
		c := idmap.NewConcurrent[K, int64](historyKeys)
		written := 0
		key := func(pc *prop.Case, _ keyValues) any { return pc.Draw(prop.Integer(0, historyKeys-1), "key") }

		stateful.Steps(pc, stateful.Machine[keyValues]{
			Spec: history.Spec[keyValues]{
				Initial: func() keyValues {
					var state keyValues
					for i := range state {
						state[i] = absent
					}

					return state
				},
				Next: stepConcurrent,
			},
			Actions: []stateful.Action[keyValues]{
				{
					Name:  opLoad,
					Input: key,
					Run: func(pc *prop.Case, client int, in any) {
						call := pc.History().Invoke(client, opLoad, []any{in})
						v, ok := c.Load(keyOf[K](in.(int)))
						if !ok {
							v = absent
						}
						call.OK(int(v))
					},
				},
				{
					Name: opStore,
					Input: func(pc *prop.Case, state keyValues) any {
						written++

						return [2]int{key(pc, state).(int), written}
					},
					Run: func(pc *prop.Case, client int, in any) {
						kv := in.([2]int)
						call := pc.History().Invoke(client, opStore, []any{kv[0], kv[1]})
						call.OK(c.Store(keyOf[K](kv[0]), int64(kv[1])))
					},
				},
				{
					Name:  opDelete,
					Input: key,
					Run: func(pc *prop.Case, client int, in any) {
						call := pc.History().Invoke(client, opDelete, []any{in})
						call.OK(c.Delete(keyOf[K](in.(int))))
					},
				},
			},
		}, stateful.Clients(2))
	})
}

// stepConcurrent returns the state of a map of historyKeys keys after op,
// or none when the map cannot have returned the output of op in state. A
// Store returns no error, because the map has room for every key.
func stepConcurrent(state keyValues, op history.Operation) []keyValues {
	k := op.Args[0].(int)
	if op.Name == opStore {
		if !op.Returned(nil) {
			return nil
		}
		state[k] = op.Args[1].(int)

		return []keyValues{state}
	}
	if op.Name == opDelete {
		if !op.Returned(state[k] != absent) {
			return nil
		}
		state[k] = absent

		return []keyValues{state}
	}
	if op.Returned(state[k]) {
		return []keyValues{state}
	}

	return nil
}

// checkOwnValues runs two writers that delete and store again the keys of a
// map of churnKeys keys from stableKeys up, and two readers that load every
// key. The value of each key is the key's number times valueStep. It checks
// that every Load returns the value of its own key, and that a Load of a key
// below stableKeys finds the key.
func checkOwnValues[K idmap.Key](t *testing.T) {
	t.Helper()

	c := idmap.NewConcurrent[K, int64](churnKeys)
	for i := range churnKeys {
		assert.NoError(t, c.Store(keyOf[K](i), int64(i)*valueStep), "Store must store every key of the map")
	}

	var wrong, missing atomic.Int64
	outcomes := history.Concurrently(4, time.Minute, func(client int) (any, error) {
		for round := range churnRounds {
			if client < 2 {
				i := stableKeys + (2*round+client)%(churnKeys-stableKeys)
				c.Delete(keyOf[K](i))
				if err := c.Store(keyOf[K](i), int64(i)*valueStep); err != nil {
					return nil, err
				}

				continue
			}

			for i := range churnKeys {
				v, ok := c.Load(keyOf[K](i))
				if ok && v != int64(i)*valueStep {
					wrong.Add(1)
				}
				if !ok && i < stableKeys {
					missing.Add(1)
				}
			}
		}

		return nil, nil
	})

	for _, o := range outcomes {
		expect.True(t, o.Finished, "every client must finish")
		expect.NoError(t, o.Error, "every Store of a writer must store its key")
	}
	expect.Equal(t, wrong.Load(), int64(0), "Load must return the value of its own key")
	expect.Equal(t, missing.Load(), int64(0), "Load must find every key that no writer deletes")
}

// filledConcurrent returns a map of the keys with a capacity of their
// number, with the index of each key as its value.
func filledConcurrent(tb testing.TB, keys []id.ID128) *idmap.Concurrent[id.ID128, int64] {
	tb.Helper()

	c := idmap.NewConcurrent[id.ID128, int64](len(keys))
	for i, k := range keys {
		assert.NoError(tb, c.Store(k, int64(i)), "Store must store every key of the map")
	}

	return c
}
