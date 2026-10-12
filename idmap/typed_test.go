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

	"go.thesmos.sh/core/id"
	"go.thesmos.sh/core/idmap"
)

// streamID is a key type of a caller whose one field embeds an id.ID.
type streamID struct{ id.ID }

func TestTyped(t *testing.T) {
	t.Parallel()

	// zero, k128, k160 and k256 are the key of id.Zero and the keys of 16, 20
	// and 32 bytes whose bytes are all zero.
	zero, k128, k160, k256 := typedKeyOf(0), typedKeyOf(1), typedKeyOf(2), typedKeyOf(3)

	t.Run("NewTyped", func(t *testing.T) {
		t.Parallel()

		t.Run("returns an empty map", func(t *testing.T) {
			t.Parallel()
			m := idmap.NewTyped[streamID, int](-1)
			expect.Equal(t, m.Len(), 0, "the new map must have no keys")
			for _, k := range []streamID{zero, k128, k160, k256} {
				_, ok := m.Get(k)
				expect.False(t, ok, "the new map must not contain a key")
			}
		})
	})

	t.Run("Get", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give streamID
		}{
			{name: "returns the value that Set stored under a key of 16 bytes", give: typedKeyOf(4)},
			{name: "returns the value that Set stored under a key of 20 bytes", give: typedKeyOf(5)},
			{name: "returns the value that Set stored under a key of 32 bytes", give: typedKeyOf(6)},
			{name: "returns the value that Set stored under the key of id.Zero", give: zero},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				m := idmap.NewTyped[streamID, int](4)
				for i := range 7 {
					m.Set(typedKeyOf(i), 10+i)
				}
				m.Set(tt.give, 99)
				v, ok := m.Get(tt.give)
				expect.True(t, ok, "Get must find the key")
				expect.Equal(t, v, 99, "Get must return the value of the key")
			})
		}

		t.Run("reports false for a key that no Set stored", func(t *testing.T) {
			t.Parallel()
			m := idmap.NewTyped[streamID, int](4)
			m.Set(typedKeyOf(4), 4)
			for _, k := range []streamID{k128, k160, k256, zero} {
				v, ok := m.Get(k)
				expect.False(t, ok, "Get must not find a key that no Set stored")
				expect.Equal(t, v, 0, "Get must return the zero value for a missing key")
			}
		})

		t.Run("reports false for an identifier of another size with the same bytes", func(t *testing.T) {
			t.Parallel()
			m := idmap.NewTyped[streamID, int](4)
			m.Set(k128, 1)
			m.Set(typedKeyOf(5), 5)
			m.Set(typedKeyOf(6), 6)
			for _, k := range []streamID{k160, k256, zero} {
				_, ok := m.Get(k)
				expect.False(t, ok, "Get must not find a key of another size")
			}
		})
	})

	t.Run("Set", func(t *testing.T) {
		t.Parallel()

		t.Run("reports false for a new key", func(t *testing.T) {
			t.Parallel()
			m := idmap.NewTyped[streamID, int](4)
			for _, k := range []streamID{zero, k128, k160, k256} {
				old, ok := m.Set(k, 1)
				expect.False(t, ok, "Set must report a new key as not present")
				expect.Equal(t, old, 0, "Set must return the zero value for a new key")
			}
		})

		tests := []struct {
			name string
			give streamID
		}{
			{name: "returns the previous value of a present key of 16 bytes", give: k128},
			{name: "returns the previous value of a present key of 20 bytes", give: k160},
			{name: "returns the previous value of a present key of 32 bytes", give: k256},
			{name: "returns the previous value of the present key of id.Zero", give: zero},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				m := idmap.NewTyped[streamID, int](4)
				m.Set(tt.give, 10)
				old, ok := m.Set(tt.give, 11)
				v, _ := m.Get(tt.give)
				expect.True(t, ok, "Set must report a present key as present")
				expect.Equal(t, old, 10, "Set must return the previous value")
				expect.Equal(t, v, 11, "Set must store the new value")
			})
		}

		t.Run("keeps the key of id.Zero apart from the identifiers whose bytes are all zero", func(t *testing.T) {
			t.Parallel()
			m := idmap.NewTyped[streamID, int](4)
			keys := []streamID{zero, k128, k160, k256}
			for i, k := range keys {
				m.Set(k, i)
			}
			got := make([]int, 0, len(keys))
			for _, k := range keys {
				v, _ := m.Get(k)
				got = append(got, v)
			}
			assert.Equal(t, got, []int{0, 1, 2, 3}, "Get must return the value of each key")
		})

		t.Run("keeps every key as it grows the map of each size", func(t *testing.T) {
			t.Parallel()
			const keys = 1000
			m := idmap.NewTyped[streamID, int](1)
			got, want := make([]int, 0, keys), make([]int, 0, keys)
			for i := range keys {
				m.Set(typedKeyOf(i), i+1)
			}
			for i := range keys {
				v, _ := m.Get(typedKeyOf(i))
				got, want = append(got, v), append(want, i+1)
			}
			assert.Equal(t, got, want, "Get must return the value of every key that Set stored")
		})
	})

	t.Run("Delete", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give streamID
		}{
			{name: "returns the value of a present key of 16 bytes", give: k128},
			{name: "returns the value of a present key of 20 bytes", give: k160},
			{name: "returns the value of a present key of 32 bytes", give: k256},
			{name: "returns the value of the present key of id.Zero", give: zero},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				m := idmap.NewTyped[streamID, int](4)
				for i := range 7 {
					m.Set(typedKeyOf(i), 10+i)
				}
				m.Set(tt.give, 99)
				old, ok := m.Delete(tt.give)
				_, found := m.Get(tt.give)
				expect.True(t, ok, "Delete must report a present key as present")
				expect.Equal(t, old, 99, "Delete must return the value of the key")
				expect.False(t, found, "Get must not find a deleted key")
				expect.Equal(t, m.Len(), 6, "Delete must not remove another key")
			})
		}

		t.Run("reports false for a key that the map does not contain", func(t *testing.T) {
			t.Parallel()
			m := idmap.NewTyped[streamID, int](4)
			m.Set(typedKeyOf(4), 4)
			for _, k := range []streamID{zero, k128, k160, k256} {
				old, ok := m.Delete(k)
				expect.False(t, ok, "Delete must report a missing key as not present")
				expect.Equal(t, old, 0, "Delete must return the zero value for a missing key")
			}
			expect.Equal(t, m.Len(), 1, "Delete of a missing key must not remove another key")
		})

		drops := []struct {
			name string
			give streamID
		}{
			{name: "drops the value that it removes under a key of 16 bytes", give: k128},
			{name: "drops the value that it removes under the key of id.Zero", give: zero},
		}
		for _, tt := range drops {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				m := idmap.NewTyped[streamID, *[64]byte](4)
				value := new([64]byte)
				w := weak.Make(value)
				m.Set(tt.give, value)
				m.Delete(tt.give)
				runtime.GC()
				assert.Nil(t, w.Value(), "the map must not refer to a value that Delete removed")
				runtime.KeepAlive(m)
			})
		}
	})

	t.Run("Len", func(t *testing.T) {
		t.Parallel()

		t.Run("counts the keys of every size and the key of id.Zero", func(t *testing.T) {
			t.Parallel()
			m := idmap.NewTyped[streamID, int](4)
			for i := range 7 {
				m.Set(typedKeyOf(i), i)
			}
			m.Set(zero, 10)
			m.Set(k256, 30)
			assert.Equal(t, m.Len(), 7, "Len must count each key once")
		})
	})

	t.Run("Clear", func(t *testing.T) {
		t.Parallel()

		t.Run("removes every key", func(t *testing.T) {
			t.Parallel()
			m := idmap.NewTyped[streamID, int](4)
			for i := range 7 {
				m.Set(typedKeyOf(i), i)
			}
			m.Clear()
			expect.Equal(t, m.Len(), 0, "Clear must leave no key")
			for i := range 7 {
				_, ok := m.Get(typedKeyOf(i))
				expect.False(t, ok, "Get must not find a key after Clear")
			}
		})

		drops := []struct {
			name string
			give streamID
		}{
			{name: "drops the value of a key of 20 bytes", give: k160},
			{name: "drops the value of the key of id.Zero", give: zero},
		}
		for _, tt := range drops {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				m := idmap.NewTyped[streamID, *[64]byte](4)
				value := new([64]byte)
				w := weak.Make(value)
				m.Set(tt.give, value)
				m.Clear()
				runtime.GC()
				assert.Nil(t, w.Value(), "the map must not refer to a value that Clear removed")
				runtime.KeepAlive(m)
			})
		}
	})

	t.Run("Clone", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a map with the keys and the values of the map", func(t *testing.T) {
			t.Parallel()
			m := idmap.NewTyped[streamID, int](1)
			for i := range 100 {
				m.Set(typedKeyOf(i), i)
			}
			c := m.Clone()
			expect.Equal(t, maps.Collect(c.All()), maps.Collect(m.All()),
				"the copy must have every key of the map with its value")
			expect.Equal(t, c.Len(), m.Len(), "the copy must have as many keys as the map")
		})

		t.Run("returns a copy that a write to the map does not change", func(t *testing.T) {
			t.Parallel()
			m := idmap.NewTyped[streamID, int](4)
			for i := range 4 {
				m.Set(typedKeyOf(i), i)
			}
			c := m.Clone()
			for i := range 4 {
				m.Set(typedKeyOf(i), 10+i)
			}
			m.Delete(k160)
			m.Set(typedKeyOf(4), 4)
			assert.Equal(t, maps.Collect(c.All()), map[streamID]int{zero: 0, k128: 1, k160: 2, k256: 3},
				"a write to the map must not change the copy")
		})

		t.Run("returns a copy whose writes do not change the map", func(t *testing.T) {
			t.Parallel()
			m := idmap.NewTyped[streamID, int](4)
			m.Set(k128, 1)
			c := m.Clone()
			for i := range 4 {
				c.Set(typedKeyOf(i), 10+i)
			}
			c.Delete(k128)
			assert.Equal(t, maps.Collect(m.All()), map[streamID]int{k128: 1},
				"a write to the copy must not change the map")
		})
	})

	t.Run("All", func(t *testing.T) {
		t.Parallel()

		t.Run("yields every key with its value", func(t *testing.T) {
			t.Parallel()
			m := idmap.NewTyped[streamID, int](1)
			want := map[streamID]int{}
			for i := range 100 {
				m.Set(typedKeyOf(i), i)
				want[typedKeyOf(i)] = i
			}
			got := map[streamID]int{}
			yields := 0
			for k, v := range m.All() {
				got[k] = v
				yields++
			}
			expect.Equal(t, got, want, "All must yield every key with its value")
			expect.Equal(t, yields, len(want), "All must yield each key once")
		})

		tests := []struct {
			name string
			give int
		}{
			{name: "stops when the loop body breaks at the key of id.Zero", give: 1},
			{name: "stops when the loop body breaks at a key of 16 bytes", give: 2},
			{name: "stops when the loop body breaks at a key of 20 bytes", give: 3},
			{name: "stops when the loop body breaks at a key of 32 bytes", give: 4},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				m := idmap.NewTyped[streamID, int](4)
				for i := range 4 {
					m.Set(typedKeyOf(i), i)
				}
				yields, last := 0, -1
				for _, v := range m.All() {
					yields, last = yields+1, v
					if yields == tt.give {
						break
					}
				}
				expect.Equal(t, yields, tt.give, "All must not yield after the loop body breaks")
				expect.Equal(t, last, tt.give-1,
					"All must yield the key of id.Zero first, then the keys of 16, 20 and 32 bytes")
			})
		}

		t.Run("yields every key once while the loop body deletes the key that it received", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "All must yield each key once while the loop body deletes keys", func(pc *prop.Case) {
				n := pc.Draw(prop.Integer(1, 40), "keys")
				drop := pc.Draw(prop.List(prop.Boolean(), prop.MinSize(n), prop.MaxSize(n)), "drop")
				m := idmap.NewTyped[streamID, int](1)
				want, kept := make([]int, 0, n), 0
				for i := range n {
					m.Set(typedKeyOf(i), i)
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

	t.Run("returns what a Go map returns for random calls", func(t *testing.T) {
		t.Parallel()
		checkMap(t, idmap.NewTyped[streamID, int], typedKeyOf)
	})
}

// TestTypedAllocs checks the allocation contract of a Typed. MaxAllocs counts
// the allocations of the whole process, so the test does not run in
// parallel.
func TestTypedAllocs(t *testing.T) {
	keys := make([]streamID, 0, benchKeys+1)
	for i := range benchKeys + 1 {
		keys = append(keys, streamID{keyOf[id.ID128](i).ID()})
	}

	t.Run("NewTyped", func(t *testing.T) {
		var m *idmap.Typed[streamID, int]
		expect.MaxAllocs(t, func() { m = idmap.NewTyped[streamID, int](benchKeys) }, 1,
			"NewTyped must allocate the map")
		expect.Equal(t, m.Len(), 0, "the test must measure an empty map")
	})

	t.Run("Get", func(t *testing.T) {
		m := filledTyped(keys[:benchKeys])
		var hit, miss bool
		expect.MaxAllocs(t, func() { _, hit = m.Get(keys[500]) }, 0, "a Get of a present key must not allocate")
		expect.MaxAllocs(t, func() { _, miss = m.Get(keys[benchKeys]) }, 0, "a Get of a missing key must not allocate")
		expect.True(t, hit, "the test must measure a present key")
		expect.False(t, miss, "the test must measure a missing key")
	})

	t.Run("Set", func(t *testing.T) {
		m := filledTyped(keys[:benchKeys/2])
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

		expect.MaxAllocsWithSetup(t, func() *idmap.Typed[streamID, int] { return idmap.NewTyped[streamID, int](4) },
			func(m *idmap.Typed[streamID, int]) { m.Set(keys[0], 0) }, 4,
			"the first Set of a size must allocate the Map of that size and its three arrays")

		expect.MaxAllocsWithSetup(t, func() *idmap.Typed[streamID, int] { return filledTyped(keys[:5]) },
			func(m *idmap.Typed[streamID, int]) { m.Set(keys[5], 5) }, 3,
			"a Set that grows the map must allocate its three arrays")
	})

	t.Run("Delete", func(t *testing.T) {
		m := filledTyped(keys[:benchKeys])
		var ok bool
		expect.MaxAllocs(t, func() {
			_, ok = m.Delete(keys[500])
			m.Set(keys[500], 500)
		}, 0, "a Delete and a Set at a constant size must not allocate")
		expect.True(t, ok, "the test must measure a present key")
	})

	t.Run("Len", func(t *testing.T) {
		m := filledTyped(keys[:benchKeys])
		var n int
		expect.MaxAllocs(t, func() { n = m.Len() }, 0, "Len must not allocate")
		expect.Equal(t, n, benchKeys, "the test must measure a full map")
	})

	t.Run("Clear", func(t *testing.T) {
		m := filledTyped(keys[:benchKeys])
		expect.MaxAllocs(t, m.Clear, 0, "Clear must not allocate")
		expect.Equal(t, m.Len(), 0, "the test must measure a cleared map")
	})

	t.Run("Clone", func(t *testing.T) {
		m := filledTyped(keys[:benchKeys])
		var c *idmap.Typed[streamID, int]
		expect.MaxAllocs(t, func() { c = m.Clone() }, 5,
			"Clone must allocate the copy, and the copy of its one Map with three arrays")
		expect.Equal(t, c.Len(), benchKeys, "the test must measure a copy of a full map")
	})

	t.Run("All", func(t *testing.T) {
		m := filledTyped(keys[:benchKeys])
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

func BenchmarkTyped(b *testing.B) {
	keys := make([]streamID, 0, benchKeys)
	for i := range benchKeys {
		keys = append(keys, streamID{keyOf[id.ID128](i).ID()})
	}

	b.Run("Get", func(b *testing.B) {
		m := filledTyped(keys)

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
		m := filledTyped(keys)
		m.Clear()

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
		m := filledTyped(keys)

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
		m := filledTyped(keys)

		c := bench.Start(b).MaxAllocs(5)
		defer c.End()

		var copied *idmap.Typed[streamID, int]
		for c.Loop() {
			copied = m.Clone()
		}

		assert.Equal(b, copied.Len(), benchKeys, "the benchmark must measure a copy of a full map")
	})

	b.Run("All", func(b *testing.B) {
		m := filledTyped(keys)

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

// typedKeyOf returns the key number i of a test of Typed. Key 0 is the key of
// id.Zero. For i above 0, key i has the identifier of keyOf((i-1)/3), of 16,
// 20 or 32 bytes by (i-1)%3, so keys 1, 2 and 3 have identifiers whose bytes
// are all zero.
func typedKeyOf(i int) streamID {
	if i == 0 {
		return streamID{}
	}

	j := i - 1
	switch j % 3 {
	case 0:
		return streamID{keyOf[id.ID128](j / 3).ID()}
	case 1:
		return streamID{keyOf[id.ID160](j / 3).ID()}
	}

	return streamID{keyOf[id.ID256](j / 3).ID()}
}

// filledTyped returns a map of the keys, presized for them, with the index of
// each key as its value.
func filledTyped(keys []streamID) *idmap.Typed[streamID, int] {
	m := idmap.NewTyped[streamID, int](len(keys))
	for i, k := range keys {
		m.Set(k, i)
	}

	return m
}
