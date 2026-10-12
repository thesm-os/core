// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package idmap_test

import (
	"maps"
	"runtime"
	"testing"
	"weak"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"

	"go.thesmos.sh/core/id"
	"go.thesmos.sh/core/idmap"
)

// benchKeys is the number of keys of the maps of the benchmarks and of the
// allocation tests.
const benchKeys = 1 << 16

// entry is the input of a step that stores a key: the key and its value.
type entry[K idmap.Key] struct {
	key   K
	value int
}

func TestMap(t *testing.T) {
	t.Parallel()

	t.Run("New", func(t *testing.T) {
		t.Parallel()

		t.Run("returns an empty map for a capacity below 1", func(t *testing.T) {
			t.Parallel()
			m := idmap.New[id.ID128, int](-1)
			_, ok := m.Get(keyOf[id.ID128](0))
			expect.Equal(t, m.Len(), 0, "the new map must have no keys")
			expect.False(t, ok, "the new map must not contain a key")
		})
	})

	t.Run("Get", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the value that Set stored under the key", func(t *testing.T) {
			t.Parallel()
			m := idmap.New[id.ID160, int](4)
			m.Set(keyOf[id.ID160](1), 10)
			m.Set(keyOf[id.ID160](2), 20)
			v, ok := m.Get(keyOf[id.ID160](2))
			expect.True(t, ok, "Get must find the key")
			expect.Equal(t, v, 20, "Get must return the value of the key")
		})

		t.Run("reports false for a key that the map does not contain", func(t *testing.T) {
			t.Parallel()
			m := idmap.New[id.ID160, int](4)
			m.Set(keyOf[id.ID160](1), 10)
			v, ok := m.Get(keyOf[id.ID160](2))
			expect.False(t, ok, "Get must not find a key that no Set stored")
			expect.Equal(t, v, 0, "Get must return the zero value for a missing key")
		})
	})

	t.Run("Set", func(t *testing.T) {
		t.Parallel()

		t.Run("reports false for a new key", func(t *testing.T) {
			t.Parallel()
			m := idmap.New[id.ID256, int](4)
			old, ok := m.Set(keyOf[id.ID256](1), 10)
			expect.False(t, ok, "Set must report a new key as not present")
			expect.Equal(t, old, 0, "Set must return the zero value for a new key")
		})

		t.Run("returns the previous value of a present key", func(t *testing.T) {
			t.Parallel()
			m := idmap.New[id.ID256, int](4)
			m.Set(keyOf[id.ID256](1), 10)
			old, ok := m.Set(keyOf[id.ID256](1), 11)
			v, _ := m.Get(keyOf[id.ID256](1))
			expect.True(t, ok, "Set must report a present key as present")
			expect.Equal(t, old, 10, "Set must return the previous value")
			expect.Equal(t, v, 11, "Set must store the new value")
		})

		t.Run("keeps every key as it grows the map", func(t *testing.T) {
			t.Parallel()
			const keys = 1000
			m := idmap.New[id.ID256, int](1)
			got, want := make([]int, 0, keys), make([]int, 0, keys)
			for i := range keys {
				m.Set(keyOf[id.ID256](i), i+1)
			}
			for i := range keys {
				v, _ := m.Get(keyOf[id.ID256](i))
				got, want = append(got, v), append(want, i+1)
			}
			assert.Equal(t, got, want, "Get must return the value of every key that Set stored")
		})
	})

	t.Run("Delete", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the value of a present key", func(t *testing.T) {
			t.Parallel()
			m := idmap.New[id.ID128, int](4)
			m.Set(keyOf[id.ID128](1), 10)
			old, ok := m.Delete(keyOf[id.ID128](1))
			_, found := m.Get(keyOf[id.ID128](1))
			expect.True(t, ok, "Delete must report a present key as present")
			expect.Equal(t, old, 10, "Delete must return the value of the key")
			expect.False(t, found, "Get must not find a deleted key")
		})

		t.Run("reports false for a key that the map does not contain", func(t *testing.T) {
			t.Parallel()
			m := idmap.New[id.ID128, int](4)
			m.Set(keyOf[id.ID128](1), 10)
			old, ok := m.Delete(keyOf[id.ID128](2))
			expect.False(t, ok, "Delete must report a missing key as not present")
			expect.Equal(t, old, 0, "Delete must return the zero value for a missing key")
			expect.Equal(t, m.Len(), 1, "Delete of a missing key must not remove another key")
		})

		t.Run("drops the value that it removes", func(t *testing.T) {
			t.Parallel()
			m := idmap.New[id.ID128, *[64]byte](4)
			value := new([64]byte)
			w := weak.Make(value)
			m.Set(keyOf[id.ID128](1), value)
			m.Delete(keyOf[id.ID128](1))
			runtime.GC()
			assert.Nil(t, w.Value(), "the map must not refer to a value that Delete removed")
			runtime.KeepAlive(m)
		})
	})

	t.Run("Len", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the number of keys", func(t *testing.T) {
			t.Parallel()
			m := idmap.New[id.ID128, int](4)
			m.Set(keyOf[id.ID128](1), 10)
			m.Set(keyOf[id.ID128](2), 20)
			m.Set(keyOf[id.ID128](1), 11)
			assert.Equal(t, m.Len(), 2, "Len must count each key once")
		})
	})

	t.Run("Clear", func(t *testing.T) {
		t.Parallel()

		t.Run("removes every key", func(t *testing.T) {
			t.Parallel()
			m := idmap.New[id.ID128, int](4)
			m.Set(keyOf[id.ID128](1), 10)
			m.Set(keyOf[id.ID128](2), 20)
			m.Clear()
			_, ok := m.Get(keyOf[id.ID128](1))
			expect.Equal(t, m.Len(), 0, "Clear must leave no key")
			expect.False(t, ok, "Get must not find a key after Clear")
		})

		t.Run("drops the values", func(t *testing.T) {
			t.Parallel()
			m := idmap.New[id.ID128, *[64]byte](4)
			value := new([64]byte)
			w := weak.Make(value)
			m.Set(keyOf[id.ID128](1), value)
			m.Clear()
			runtime.GC()
			assert.Nil(t, w.Value(), "the map must not refer to a value that Clear removed")
			runtime.KeepAlive(m)
		})
	})

	t.Run("Clone", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a map with the keys and the values of the map", func(t *testing.T) {
			t.Parallel()
			m := idmap.New[id.ID256, int](1)
			for i := range 100 {
				m.Set(keyOf[id.ID256](i), i)
			}
			c := m.Clone()
			expect.Equal(t, maps.Collect(c.All()), maps.Collect(m.All()),
				"the copy must have every key of the map with its value")
			expect.Equal(t, c.Len(), m.Len(), "the copy must have as many keys as the map")
		})

		t.Run("returns a copy that a write to the map does not change", func(t *testing.T) {
			t.Parallel()
			m := idmap.New[id.ID256, int](4)
			m.Set(keyOf[id.ID256](1), 10)
			m.Set(keyOf[id.ID256](2), 20)
			c := m.Clone()
			m.Set(keyOf[id.ID256](1), 11)
			m.Delete(keyOf[id.ID256](2))
			m.Set(keyOf[id.ID256](3), 30)
			assert.Equal(t, maps.Collect(c.All()), map[id.ID256]int{keyOf[id.ID256](1): 10, keyOf[id.ID256](2): 20},
				"a write to the map must not change the copy")
		})

		t.Run("returns a copy whose writes do not change the map", func(t *testing.T) {
			t.Parallel()
			m := idmap.New[id.ID256, int](4)
			m.Set(keyOf[id.ID256](1), 10)
			m.Set(keyOf[id.ID256](2), 20)
			c := m.Clone()
			c.Set(keyOf[id.ID256](1), 11)
			c.Delete(keyOf[id.ID256](2))
			c.Set(keyOf[id.ID256](3), 30)
			assert.Equal(t, maps.Collect(m.All()), map[id.ID256]int{keyOf[id.ID256](1): 10, keyOf[id.ID256](2): 20},
				"a write to the copy must not change the map")
		})
	})

	t.Run("All", func(t *testing.T) {
		t.Parallel()

		t.Run("yields every key with its value", func(t *testing.T) {
			t.Parallel()
			m := idmap.New[id.ID160, int](1)
			want := map[id.ID160]int{}
			for i := range 100 {
				m.Set(keyOf[id.ID160](i), i)
				want[keyOf[id.ID160](i)] = i
			}
			got := map[id.ID160]int{}
			yields := 0
			for k, v := range m.All() {
				got[k] = v
				yields++
			}
			expect.Equal(t, got, want, "All must yield every key with its value")
			expect.Equal(t, yields, len(want), "All must yield each key once")
		})

		t.Run("stops when the loop body breaks", func(t *testing.T) {
			t.Parallel()
			m := idmap.New[id.ID160, int](4)
			m.Set(keyOf[id.ID160](1), 10)
			m.Set(keyOf[id.ID160](2), 20)
			yields := 0
			for range m.All() {
				yields++

				break
			}
			assert.Equal(t, yields, 1, "All must not yield after the loop body breaks")
		})

		t.Run("yields every key once while the loop body deletes the key that it received", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "All must yield each key once while the loop body deletes keys", func(pc *prop.Case) {
				n := pc.Draw(prop.Integer(1, 40), "keys")
				drop := pc.Draw(prop.List(prop.Boolean(), prop.MinSize(n), prop.MaxSize(n)), "drop")
				m := idmap.New[id.ID128, int](1)
				want, kept := make([]int, 0, n), 0
				for i := range n {
					m.Set(keyOf[id.ID128](i), i)
					want = append(want, i)
					if !drop[i] {
						kept++
					}
				}
				got := make([]int, 0, n)
				for k, v := range m.All() {
					got = append(got, v)
					if drop[v] {
						m.Delete(k)
					}
				}
				assert.Permutation(pc, got, want, "All must yield each key once")
				assert.Equal(pc, m.Len(), kept, "the map must keep the keys that the loop body did not delete")
			})
		})
	})

	t.Run("returns what a Go map returns for random calls with ID128 keys", func(t *testing.T) {
		t.Parallel()
		checkMap[id.ID128](t)
	})

	t.Run("returns what a Go map returns for random calls with ID160 keys", func(t *testing.T) {
		t.Parallel()
		checkMap[id.ID160](t)
	})

	t.Run("returns what a Go map returns for random calls with ID256 keys", func(t *testing.T) {
		t.Parallel()
		checkMap[id.ID256](t)
	})
}

// TestMapAllocs checks the allocation contract of a Map. MaxAllocs counts the
// allocations of the whole process, so the test does not run in parallel.
func TestMapAllocs(t *testing.T) {
	keys := make([]id.ID128, 0, benchKeys+1)
	for i := range benchKeys + 1 {
		keys = append(keys, keyOf[id.ID128](i))
	}

	t.Run("New", func(t *testing.T) {
		var m *idmap.Map[id.ID128, int]
		expect.MaxAllocs(t, func() { m = idmap.New[id.ID128, int](benchKeys) }, 4,
			"New must allocate the map and its three arrays")
		expect.Equal(t, m.Len(), 0, "the test must measure an empty map")
	})

	t.Run("Get", func(t *testing.T) {
		m := filledMap(keys[:benchKeys])
		var hit, miss bool
		expect.MaxAllocs(t, func() { _, hit = m.Get(keys[500]) }, 0, "a Get of a present key must not allocate")
		expect.MaxAllocs(t, func() { _, miss = m.Get(keys[benchKeys]) }, 0, "a Get of a missing key must not allocate")
		expect.True(t, hit, "the test must measure a present key")
		expect.False(t, miss, "the test must measure a missing key")
	})

	t.Run("Set", func(t *testing.T) {
		m := filledMap(keys[:benchKeys/2])
		var present bool
		expect.MaxAllocs(t, func() { _, present = m.Set(keys[500], 1) }, 0,
			"a Set of a present key must not allocate")
		expect.True(t, present, "the test must measure a present key")

		next := benchKeys / 2
		expect.MaxAllocs(t, func() {
			m.Set(keys[next], next)
			next++
		}, 0, "a Set of a new key into a map with room must not allocate")
		expect.Equal(t, m.Len(), next, "the test must measure new keys")

		expect.MaxAllocsWithSetup(t, func() *idmap.Map[id.ID128, int] { return filledMap(keys[:5]) },
			func(m *idmap.Map[id.ID128, int]) { m.Set(keys[5], 5) }, 3,
			"a Set that grows the map must allocate its three arrays")
	})

	t.Run("Delete", func(t *testing.T) {
		m := filledMap(keys[:benchKeys])
		var ok bool
		expect.MaxAllocs(t, func() {
			_, ok = m.Delete(keys[500])
			m.Set(keys[500], 500)
		}, 0, "a Delete and a Set at a constant size must not allocate")
		expect.True(t, ok, "the test must measure a present key")
	})

	t.Run("Len", func(t *testing.T) {
		m := filledMap(keys[:benchKeys])
		var n int
		expect.MaxAllocs(t, func() { n = m.Len() }, 0, "Len must not allocate")
		expect.Equal(t, n, benchKeys, "the test must measure a full map")
	})

	t.Run("Clear", func(t *testing.T) {
		m := filledMap(keys[:benchKeys])
		expect.MaxAllocs(t, m.Clear, 0, "Clear must not allocate")
		expect.Equal(t, m.Len(), 0, "the test must measure a cleared map")
	})

	t.Run("Clone", func(t *testing.T) {
		m := filledMap(keys[:benchKeys])
		var c *idmap.Map[id.ID128, int]
		expect.MaxAllocs(t, func() { c = m.Clone() }, 4, "Clone must allocate the copy and its three arrays")
		expect.Equal(t, c.Len(), benchKeys, "the test must measure a copy of a full map")
	})

	t.Run("All", func(t *testing.T) {
		m := filledMap(keys[:benchKeys])
		var sum int
		expect.MaxAllocs(t, func() {
			sum = 0
			for _, v := range m.All() {
				sum += v
			}
		}, 0, "a range loop over All must not allocate")
		expect.Equal(t, sum, benchKeys*(benchKeys-1)/2, "the test must measure every key")
	})
}

func BenchmarkMap(b *testing.B) {
	keys := make([]id.ID128, 0, benchKeys)
	for i := range benchKeys {
		keys = append(keys, keyOf[id.ID128](i))
	}

	b.Run("Get", func(b *testing.B) {
		m := filledMap(keys)

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		var ok bool
		i := 0
		for c.Loop() {
			_, ok = m.Get(keys[i&(benchKeys-1)])
			i++
		}

		assert.True(b, ok, "the benchmark must measure present keys")
	})

	b.Run("Set", func(b *testing.B) {
		m := idmap.New[id.ID128, int](benchKeys)

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		i := 0
		for c.Loop() {
			if i == benchKeys {
				c.Excluding(m.Clear)
				i = 0
			}
			m.Set(keys[i], i)
			i++
		}

		assert.Equal(b, m.Len(), i, "the benchmark must measure new keys")
	})

	b.Run("Delete", func(b *testing.B) {
		m := filledMap(keys)

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		var ok bool
		i := 0
		for c.Loop() {
			k := keys[i&(benchKeys-1)]
			_, ok = m.Delete(k)
			m.Set(k, i)
			i++
		}

		assert.True(b, ok, "the benchmark must measure present keys")
	})

	b.Run("Clone", func(b *testing.B) {
		m := filledMap(keys)

		c := bench.Start(b).MaxAllocs(4)
		defer c.End()

		var copied *idmap.Map[id.ID128, int]
		for c.Loop() {
			copied = m.Clone()
		}

		assert.Equal(b, copied.Len(), benchKeys, "the benchmark must measure a copy of a full map")
	})

	b.Run("All", func(b *testing.B) {
		m := filledMap(keys)

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		var sum int
		for c.Loop() {
			sum = 0
			for _, v := range m.All() {
				sum += v
			}
		}

		assert.Equal(b, sum, benchKeys*(benchKeys-1)/2, "the benchmark must measure every key")
	})
}

// checkMap runs random calls on a Map with keys of type K, over a key space
// of three times its capacity, and checks each result against a Go map that
// receives the same calls. A Clone step continues the calls on the copy,
// and every later step checks that the map that Clone copied is unchanged.
func checkMap[K idmap.Key](t *testing.T) {
	t.Helper()

	prop.ForAll(t, "a Map must return what a Go map returns for the same calls", func(pc *prop.Case) {
		capacity := pc.Draw(prop.Integer(1, 16), "capacity")
		m := idmap.New[K, int](capacity)
		ref := map[K]int{}
		var original *idmap.Map[K, int]
		var originalRef map[K]int
		written := 0
		key := func(pc *prop.Case, _ struct{}) any {
			return keyOf[K](pc.Draw(prop.Integer(0, 3*capacity-1), "key"))
		}

		stateful.Steps(pc, stateful.Machine[struct{}]{
			Actions: []stateful.Action[struct{}]{
				{
					Name: opClear,
					Run: func(*prop.Case, int, any) {
						m.Clear()
						clear(ref)
					},
				},
				{
					Name: opClone,
					Run: func(*prop.Case, int, any) {
						original, originalRef = m, maps.Clone(ref)
						m = m.Clone()
					},
				},
				{
					Name:   opGet,
					Weight: 4,
					Input:  key,
					Run: func(pc *prop.Case, _ int, in any) {
						got, ok := m.Get(in.(K))
						want, present := ref[in.(K)]
						assert.Equal(pc, ok, present, "Get must report whether the key is present")
						assert.Equal(pc, got, want, "Get must return the value of the key")
					},
				},
				{
					Name:   opSet,
					Weight: 4,
					Input: func(pc *prop.Case, state struct{}) any {
						written++

						return entry[K]{key: key(pc, state).(K), value: written}
					},
					Run: func(pc *prop.Case, _ int, in any) {
						e := in.(entry[K])
						old, ok := m.Set(e.key, e.value)
						want, present := ref[e.key]
						ref[e.key] = e.value
						assert.Equal(pc, ok, present, "Set must report whether the key was present")
						assert.Equal(pc, old, want, "Set must return the previous value of the key")
					},
				},
				{
					Name:   opDelete,
					Weight: 4,
					Input:  key,
					Run: func(pc *prop.Case, _ int, in any) {
						old, ok := m.Delete(in.(K))
						want, present := ref[in.(K)]
						delete(ref, in.(K))
						assert.Equal(pc, ok, present, "Delete must report whether the key was present")
						assert.Equal(pc, old, want, "Delete must return the value of the key")
					},
				},
				{
					Name: opAll,
					Run: func(pc *prop.Case, _ int, _ any) {
						yields := 0
						for range m.All() {
							yields++
						}
						assert.Equal(pc, maps.Collect(m.All()), ref, "All must yield every key with its value")
						assert.Equal(pc, yields, len(ref), "All must yield each key once")
					},
				},
			},
			Invariant: func(pc *prop.Case, _ struct{}) {
				assert.Equal(pc, m.Len(), len(ref), "Len must return the number of keys")
				if original != nil {
					assert.Equal(pc, maps.Collect(original.All()), originalRef,
						"a write to the copy must not change the map that Clone copied")
				}
			},
		})
	})
}

// filledMap returns a map of the keys, presized for them, with the index of
// each key as its value.
func filledMap(keys []id.ID128) *idmap.Map[id.ID128, int] {
	m := idmap.New[id.ID128, int](len(keys))
	for i, k := range keys {
		m.Set(k, i)
	}

	return m
}
