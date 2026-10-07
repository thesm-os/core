// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package btree_test

import (
	"bytes"
	"cmp"
	"fmt"
	"slices"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/btree"
)

// leafItems is the capacity of a leaf in items, which the package documents.
// Keys that a map receives in ascending order fill every leaf.
const leafItems = 63

func TestMapFunc(t *testing.T) {
	t.Parallel()

	// top is the largest key of a map from descending(small), which sorts
	// first in reverse order.
	const top = 2 * (small - 1)

	t.Run("NewMapFunc", func(t *testing.T) {
		t.Parallel()

		t.Run("orders the keys by the function", func(t *testing.T) {
			t.Parallel()
			m := btree.NewMapFunc[string, int](func(a, b string) int { return cmp.Compare(len(a), len(b)) })
			for i, k := range []string{"ccc", "a", "bb"} {
				m.Set(k, i)
			}
			assert.Equal(t, slices.Collect(m.Keys()), []string{"a", "bb", "ccc"}, "keys must sort by length")
		})

		t.Run("treats keys that the function finds equal as one key", func(t *testing.T) {
			t.Parallel()
			m := btree.NewMapFunc[string, int](func(a, b string) int { return cmp.Compare(len(a), len(b)) })
			m.Set("bb", 2)
			m.Set("dd", 3)
			v, _ := m.Get("xx")
			assert.Equal(t, v, 3, "the function must decide which keys are equal")
			assert.Equal(t, slices.Collect(m.Keys()), []string{"bb"}, "Set must keep the stored key")
		})

		t.Run("panics on a nil function", func(t *testing.T) {
			t.Parallel()
			want := any("btree: NewMapFunc with a nil comparison function")
			got := assert.Panics(t, func() { btree.NewMapFunc[int, int](nil) }, "a map without an order must panic")
			assert.Equal(t, got, want, "the panic must name the cause")
		})
	})

	t.Run("Get", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the value of every key", func(t *testing.T) {
			t.Parallel()
			m, keys := descending(big)
			assert.Total(t, func(k int) error {
				if v, ok := m.Get(k); !ok || v != -k {
					return fmt.Errorf("key %d: Get returned %d, %t", k, v, ok)
				}

				return nil
			}, keys, "Get must return the value of every present key")
		})

		t.Run("reports an absent key", func(t *testing.T) {
			t.Parallel()
			m, _ := descending(big)
			v, ok := m.Get(1)
			expect.False(t, ok, "Get must report an absent key")
			expect.Equal(t, v, 0, "a miss must return the zero value")
		})
	})

	t.Run("Has", func(t *testing.T) {
		t.Parallel()

		t.Run("reports true for a present key", func(t *testing.T) {
			t.Parallel()
			m, _ := descending(small)
			assert.True(t, m.Has(4), "Has must report a present key")
		})

		t.Run("reports false for an absent key", func(t *testing.T) {
			t.Parallel()
			m, _ := descending(small)
			assert.False(t, m.Has(5), "Has must report an absent key")
		})
	})

	t.Run("Set", func(t *testing.T) {
		t.Parallel()

		t.Run("reports false for a new key", func(t *testing.T) {
			t.Parallel()
			m, _ := descending(small)
			old, ok := m.Set(5, -5)
			expect.False(t, ok, "Set must report a new key")
			expect.Equal(t, old, 0, "Set of a new key must return the zero value")
		})

		t.Run("returns the previous value of a present key", func(t *testing.T) {
			t.Parallel()
			m, _ := descending(small)
			m.Set(5, -5)
			old, ok := m.Set(5, -6)
			assert.True(t, ok, "Set must report a present key")
			assert.Equal(t, old, -5, "Set must return the previous value")
		})

		t.Run("inserts a new key at its place in the order of the function", func(t *testing.T) {
			t.Parallel()
			m, keys := descending(small)
			m.Set(5, -5)
			want := slices.Insert(keys, slices.Index(keys, 4), 5)
			assert.Equal(t, items(t, m.All()), want, "Set must insert the key in the order of the function")
		})
	})

	t.Run("Update", func(t *testing.T) {
		t.Parallel()

		t.Run("passes fn the value of a present key", func(t *testing.T) {
			t.Parallel()
			m, _ := descending(small)
			var old int
			var exists bool
			m.Update(4, func(o int, e bool) int {
				old, exists = o, e

				return o
			})
			assert.True(t, exists, "fn must receive a present key")
			assert.Equal(t, old, -4, "fn must receive the value")
		})

		t.Run("stores the result of fn", func(t *testing.T) {
			t.Parallel()
			m, _ := descending(small)
			got := m.Update(4, func(int, bool) int { return 40 })
			v, _ := m.Get(4)
			expect.Equal(t, got, 40, "Update must return the new value")
			expect.Equal(t, v, 40, "Update must store the new value")
		})
	})

	t.Run("Delete", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the value of a present key", func(t *testing.T) {
			t.Parallel()
			m, _ := descending(small)
			old, ok := m.Delete(4)
			assert.True(t, ok, "Delete must report a present key")
			assert.Equal(t, old, -4, "Delete must return the value")
		})

		t.Run("removes the key", func(t *testing.T) {
			t.Parallel()
			m, keys := descending(small)
			m.Delete(4)
			assert.Equal(t, items(t, m.All()), slices.DeleteFunc(keys, func(k int) bool { return k == 4 }),
				"Delete must remove the key")
		})

		t.Run("lets the caller reuse the bytes of a removed key at the start of a leaf", func(t *testing.T) {
			t.Parallel()
			m, keys := byteKeys()
			_, ok := m.Delete(keys[leafItems])
			assert.True(t, ok, "Delete must remove the key")
			clear(keys[leafItems])
			got, want := make([]bool, 0, len(keys)), make([]bool, 0, len(keys))
			for i, k := range keys {
				_, ok := m.Get(k)
				got, want = append(got, ok), append(want, i != leafItems)
			}
			assert.Equal(t, got, want, "Get must find every key that the map contains")
		})
	})

	t.Run("DeleteRange", func(t *testing.T) {
		t.Parallel()

		t.Run("removes the keys in [lo, hi) of the order of the function", func(t *testing.T) {
			t.Parallel()
			m, keys := descending(small)
			assert.Equal(t, m.DeleteRange(50, 100), 0, "hi sorts before lo in reverse order")
			assert.Equal(t, m.DeleteRange(100, 50), 25, "DeleteRange must remove 100 down to 52")
			assert.Equal(t, items(t, m.All()), slices.DeleteFunc(keys, func(k int) bool { return k <= 100 && k > 50 }),
				"DeleteRange must remove exactly the range")
		})

		t.Run("lets the caller reuse the bytes of removed keys at the start of a leaf", func(t *testing.T) {
			t.Parallel()
			m, keys := byteKeys()
			assert.Equal(t, m.DeleteRange(keys[leafItems], keys[leafItems+2]), 2, "DeleteRange must remove two keys")
			clear(keys[leafItems])
			clear(keys[leafItems+1])
			got, want := make([]bool, 0, len(keys)), make([]bool, 0, len(keys))
			for i, k := range keys {
				_, ok := m.Get(k)
				got, want = append(got, ok), append(want, i < leafItems || i >= leafItems+2)
			}
			assert.Equal(t, got, want, "Get must find every key that the map contains")
		})
	})

	t.Run("Len", func(t *testing.T) {
		t.Parallel()

		t.Run("counts the keys", func(t *testing.T) {
			t.Parallel()
			m, _ := descending(small)
			assert.Equal(t, m.Len(), small, "Len must count the keys")
		})
	})

	t.Run("Clear", func(t *testing.T) {
		t.Parallel()

		t.Run("removes every key", func(t *testing.T) {
			t.Parallel()
			m, _ := descending(small)
			m.Clear()
			assert.Equal(t, m.Len(), 0, "Clear must remove every key")
		})

		t.Run("keeps the order of the function", func(t *testing.T) {
			t.Parallel()
			m, _ := descending(small)
			m.Clear()
			m.Set(1, -1)
			m.Set(2, -2)
			assert.Equal(t, items(t, m.All()), []int{2, 1}, "the map must keep its order")
		})
	})

	t.Run("Reset", func(t *testing.T) {
		t.Parallel()

		t.Run("removes every key", func(t *testing.T) {
			t.Parallel()
			m, _ := descending(small)
			m.Reset()
			assert.Equal(t, m.Len(), 0, "Reset must remove every key")
		})

		t.Run("keeps the order of the function", func(t *testing.T) {
			t.Parallel()
			m, keys := descending(small)
			m.Reset()
			for _, k := range evens(small) {
				m.Set(k, -k)
			}
			assert.Equal(t, items(t, m.All()), keys, "the refilled map must keep the order of the function")
		})
	})

	t.Run("Min", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the key that sorts first", func(t *testing.T) {
			t.Parallel()
			m, _ := descending(small)
			k, v, ok := m.Min()
			assert.True(t, ok, "Min must find a key")
			expect.Equal(t, k, top, "Min must return the largest number")
			expect.Equal(t, v, -top, "Min must return the value of the largest number")
		})
	})

	t.Run("Max", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the key that sorts last", func(t *testing.T) {
			t.Parallel()
			m, _ := descending(small)
			k, v, ok := m.Max()
			assert.True(t, ok, "Max must find a key")
			expect.Equal(t, k, 0, "Max must return the smallest number")
			expect.Equal(t, v, 0, "Max must return the value of the smallest number")
		})
	})

	t.Run("Floor", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the nearest key at or before the probe in the order of the function", func(t *testing.T) {
			t.Parallel()
			m, _ := descending(small)
			k, v, ok := m.Floor(7)
			assert.True(t, ok, "Floor must find a key")
			expect.Equal(t, k, 8, "Floor must return the smallest number above 7")
			expect.Equal(t, v, -8, "Floor must return the value of that number")
		})
	})

	t.Run("Ceil", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the nearest key at or after the probe in the order of the function", func(t *testing.T) {
			t.Parallel()
			m, _ := descending(small)
			k, v, ok := m.Ceil(7)
			assert.True(t, ok, "Ceil must find a key")
			expect.Equal(t, k, 6, "Ceil must return the largest number below 7")
			expect.Equal(t, v, -6, "Ceil must return the value of that number")
		})
	})

	t.Run("PopMin", func(t *testing.T) {
		t.Parallel()

		t.Run("removes the key that sorts first", func(t *testing.T) {
			t.Parallel()
			m, _ := descending(small)
			k, v, ok := m.PopMin()
			assert.True(t, ok, "PopMin must find a key")
			expect.Equal(t, k, top, "PopMin must return the largest number")
			expect.Equal(t, v, -top, "PopMin must return the value of the largest number")
			expect.False(t, m.Has(top), "PopMin must remove the largest number")
		})
	})

	t.Run("PopMax", func(t *testing.T) {
		t.Parallel()

		t.Run("removes the key that sorts last", func(t *testing.T) {
			t.Parallel()
			m, _ := descending(small)
			k, v, ok := m.PopMax()
			assert.True(t, ok, "PopMax must find a key")
			expect.Equal(t, k, 0, "PopMax must return the smallest number")
			expect.Equal(t, v, 0, "PopMax must return the value of the smallest number")
			expect.False(t, m.Has(0), "PopMax must remove the smallest number")
		})
	})

	t.Run("At", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the key at every index in the order of the function", func(t *testing.T) {
			t.Parallel()
			m, keys := descending(small)
			n := len(keys)
			gotKeys, gotValues, wantValues := make([]int, 0, n), make([]int, 0, n), make([]int, 0, n)
			for i := range n {
				k, v, ok := m.At(i)
				assert.True(t, ok, "At must find a key at every index below Len")
				gotKeys, gotValues, wantValues = append(gotKeys, k), append(gotValues, v), append(wantValues, -keys[i])
			}
			expect.Equal(t, gotKeys, keys, "At must return the keys in the order of the function")
			expect.Equal(t, gotValues, wantValues, "At must return the value of each key")
		})
	})

	t.Run("Rank", func(t *testing.T) {
		t.Parallel()

		t.Run("counts the keys that sort before the probe in the order of the function", func(t *testing.T) {
			t.Parallel()
			m, _ := descending(small)
			assert.Equal(t, m.Rank(7), small-4, "Rank must count the keys above 7")
		})
	})

	t.Run("All", func(t *testing.T) {
		t.Parallel()

		t.Run("yields every item in the order of the function", func(t *testing.T) {
			t.Parallel()
			m, keys := descending(big)
			assert.Equal(t, items(t, m.All()), keys, "All must yield the keys from largest to smallest")
		})
	})

	t.Run("Keys", func(t *testing.T) {
		t.Parallel()

		t.Run("yields every key in the order of the function", func(t *testing.T) {
			t.Parallel()
			m, keys := descending(small)
			assert.Equal(t, slices.Collect(m.Keys()), keys, "Keys must yield the keys from largest to smallest")
		})
	})

	t.Run("Values", func(t *testing.T) {
		t.Parallel()

		t.Run("yields every value in the order of its key", func(t *testing.T) {
			t.Parallel()
			m, keys := descending(small)
			want := make([]int, len(keys))
			for i, k := range keys {
				want[i] = -k
			}
			assert.Equal(t, slices.Collect(m.Values()), want, "Values must yield the value of each key in order")
		})
	})

	t.Run("Backward", func(t *testing.T) {
		t.Parallel()

		t.Run("yields every item against the order of the function", func(t *testing.T) {
			t.Parallel()
			m, keys := descending(small)
			slices.Reverse(keys)
			assert.Equal(t, items(t, m.Backward()), keys, "Backward must yield the keys from smallest to largest")
		})
	})

	t.Run("Range", func(t *testing.T) {
		t.Parallel()

		t.Run("yields the items in [lo, hi) of the order of the function", func(t *testing.T) {
			t.Parallel()
			m, _ := descending(small)
			assert.Equal(t, items(t, m.Range(100, 90)), []int{100, 98, 96, 94, 92}, "Range must yield 100 down to 92")
		})
	})

	t.Run("Ascend", func(t *testing.T) {
		t.Parallel()

		t.Run("yields the items at or after the probe in the order of the function", func(t *testing.T) {
			t.Parallel()
			m, _ := descending(small)
			assert.Equal(t, items(t, m.Ascend(7)), []int{6, 4, 2, 0}, "Ascend must yield the keys below 7")
		})
	})

	t.Run("Descend", func(t *testing.T) {
		t.Parallel()

		t.Run("yields the items at or before the probe against the order of the function", func(t *testing.T) {
			t.Parallel()
			m, keys := descending(small)
			want := slices.Clone(keys[:slices.Index(keys, 8)+1])
			slices.Reverse(want)
			assert.Equal(t, items(t, m.Descend(7)), want, "Descend must yield the keys above 7 from smallest up")
		})
	})

	t.Run("Clone", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a map with the same items and order", func(t *testing.T) {
			t.Parallel()
			m, keys := descending(small)
			c := m.Clone()
			c.Set(5, -5)
			assert.Equal(t, items(t, m.All()), keys, "the map must not see the clone's write")
			assert.Equal(t, items(t, c.All()), slices.Insert(keys, slices.Index(keys, 4), 5),
				"the clone must contain the items in the order of the function")
		})
	})
}

// TestMapFuncAllocs checks that Get and Set of a present key of a MapFunc
// do not allocate. MaxAllocs counts the allocations of the whole process,
// so the test does not run in parallel.
//
//nolint:paralleltest // see above
func TestMapFuncAllocs(t *testing.T) {
	keys := evens(benchKeys)
	m := btree.NewMapFunc[int, int](cmp.Compare[int])
	for _, k := range keys {
		m.Set(k, k)
	}
	k := keys[benchKeys/2]

	t.Run("Get", func(t *testing.T) {
		var got int
		expect.MaxAllocs(t, func() { got, _ = m.Get(k) }, 0, "Get must not allocate")
		assert.Equal(t, got, k, "the test must measure a present key")
	})

	t.Run("Set", func(t *testing.T) {
		var present bool
		expect.MaxAllocs(t, func() { _, present = m.Set(k, k) }, 0, "Set of a present key must not allocate")
		assert.True(t, present, "the test must measure a present key")
	})
}

// BenchmarkMapFunc reports the cost of Get and of Set of a present key on a
// MapFunc of 65,536 int keys ordered by cmp.Compare, to compare with the
// same operations on a Map, and fails when either allocates.
func BenchmarkMapFunc(b *testing.B) {
	keys := evens(benchKeys)
	m := btree.NewMapFunc[int, int](cmp.Compare[int])
	for _, k := range keys {
		m.Set(k, k)
	}

	b.Run("Get", func(b *testing.B) {
		i, sum := 0, 0

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			v, _ := m.Get(keys[i])
			sum += v
			i = (i + 1) % benchKeys
		}

		assert.NotEqual(b, sum, 0, "the benchmark must measure present keys")
	})

	b.Run("Set", func(b *testing.B) {
		b.Run("of a present key", func(b *testing.B) {
			i := 0

			var present bool

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				_, present = m.Set(keys[i], i)
				i = (i + 1) % benchKeys
			}

			assert.True(b, present, "the benchmark must measure present keys")
		})
	})
}

// descending returns a map in reverse order of the keys of evens(n), set in
// that order with the negation of each key as its value, and its keys from
// largest to smallest.
func descending(n int) (*btree.MapFunc[int, int], []int) {
	reverse := func(a, b int) int { return cmp.Compare(b, a) }
	m := btree.NewMapFunc[int, int](reverse)
	keys := evens(n)
	for _, k := range keys {
		m.Set(k, -k)
	}
	slices.SortFunc(keys, reverse)

	return m, keys
}

// byteKeys returns a map of the []byte keys k0000 to k0188 ordered by
// bytes.Compare, set in ascending order with the index of each key as its
// value, and its keys in that order. The keys fill three leaves, so
// keys[leafItems] is the first key of the second leaf.
func byteKeys() (*btree.MapFunc[[]byte, int], [][]byte) {
	m := btree.NewMapFunc[[]byte, int](bytes.Compare)
	keys := make([][]byte, 3*leafItems)
	for i := range keys {
		keys[i] = fmt.Appendf(nil, "k%04d", i)
		m.Set(keys[i], i)
	}

	return m, keys
}
