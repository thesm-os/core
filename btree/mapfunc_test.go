// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package btree_test

import (
	"cmp"
	"slices"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/btree"
)

// reverse orders ints from largest to smallest.
func reverse(a, b int) int {
	return cmp.Compare(b, a)
}

// descending returns a map in reverse order of the keys 0, 2, ...,
// 2(n-1), set in a random order with the negation of each key as its
// value, and its keys in that order: from largest to smallest.
func descending(tb testing.TB, n int) (*btree.MapFunc[int, int], []int) {
	tb.Helper()
	m := btree.NewMapFunc[int, int](reverse)
	keys := evens(tb, n)
	for _, k := range keys {
		m.Set(k, -k)
	}
	slices.SortFunc(keys, reverse)

	return m, keys
}

func TestMapFunc(t *testing.T) {
	t.Parallel()

	// top is the largest key of a map from descending(t, small), which
	// sorts first in reverse order.
	const top = 2 * (small - 1)

	t.Run("NewMapFunc", func(t *testing.T) {
		t.Parallel()

		t.Run("orders the keys by the function and treats keys that it finds equal as one key", func(t *testing.T) {
			t.Parallel()
			m := btree.NewMapFunc[string, int](func(a, b string) int { return cmp.Compare(len(a), len(b)) })
			for i, k := range []string{"ccc", "a", "bb", "dd"} {
				m.Set(k, i)
			}
			testkit.Equal(t, slices.Collect(m.Keys()), []string{"a", "bb", "ccc"}, "keys must sort by length")
			v, _ := m.Get("xx")
			testkit.Equal(t, v, 3, "the function must decide which keys are equal, and Set must keep the stored key")
		})

		t.Run("panics on a nil function", func(t *testing.T) {
			t.Parallel()
			got := testkit.Panics(t, func() { btree.NewMapFunc[int, int](nil) }, "a map without an order must panic")
			testkit.Equal(
				t,
				got,
				any("btree: NewMapFunc with a nil comparison function"),
				"the panic must name the cause",
			)
		})
	})

	t.Run("Get", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the value of every key and reports an absent key", func(t *testing.T) {
			t.Parallel()
			m, keys := descending(t, big)
			for _, k := range keys {
				v, ok := m.Get(k)
				testkit.True(t, ok && v == -k, "Get must return the value of a present key")
			}
			v, ok := m.Get(1)
			testkit.True(t, !ok && v == 0, "Get must report an absent key")
		})
	})

	t.Run("Has", func(t *testing.T) {
		t.Parallel()

		t.Run("reports whether a key is present", func(t *testing.T) {
			t.Parallel()
			m, _ := descending(t, small)
			testkit.True(t, m.Has(4) && !m.Has(5), "Has must report whether the key is present")
		})
	})

	t.Run("Set", func(t *testing.T) {
		t.Parallel()

		t.Run("inserts a new key at its place in the order of the function", func(t *testing.T) {
			t.Parallel()
			m, keys := descending(t, small)
			old, ok := m.Set(5, -5)
			testkit.True(t, !ok && old == 0, "Set must report a new key")
			old, ok = m.Set(5, -5)
			testkit.True(t, ok && old == -5, "Set must return the previous value")
			want := slices.Insert(keys, slices.Index(keys, 4), 5)
			testkit.Equal(t, items(t, m.All()), want, "Set must insert the key in the order of the function")
		})
	})

	t.Run("Update", func(t *testing.T) {
		t.Parallel()

		t.Run("passes the value of a key and stores the result", func(t *testing.T) {
			t.Parallel()
			m, _ := descending(t, small)
			got := m.Update(4, func(old int, exists bool) int {
				testkit.True(t, exists && old == -4, "fn must receive the value")
				return 40
			})
			v, _ := m.Get(4)
			testkit.True(t, got == 40 && v == 40, "Update must store and return the new value")
		})
	})

	t.Run("Delete", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the value of a present key and removes it", func(t *testing.T) {
			t.Parallel()
			m, keys := descending(t, small)
			old, ok := m.Delete(4)
			testkit.True(t, ok && old == -4, "Delete must return the value")
			testkit.Equal(t, items(t, m.All()), slices.DeleteFunc(keys, func(k int) bool { return k == 4 }),
				"Delete must remove the key")
		})
	})

	t.Run("DeleteRange", func(t *testing.T) {
		t.Parallel()

		t.Run("removes the keys in [lo, hi) of the order of the function", func(t *testing.T) {
			t.Parallel()
			m, keys := descending(t, small)
			testkit.Equal(t, m.DeleteRange(50, 100), 0, "hi sorts before lo in reverse order")
			testkit.Equal(t, m.DeleteRange(100, 50), 25, "DeleteRange must remove 100 down to 52")
			testkit.Equal(t, items(t, m.All()), slices.DeleteFunc(keys, func(k int) bool { return k <= 100 && k > 50 }),
				"DeleteRange must remove exactly the range")
		})
	})

	t.Run("Len", func(t *testing.T) {
		t.Parallel()

		t.Run("counts the keys", func(t *testing.T) {
			t.Parallel()
			m, _ := descending(t, small)
			testkit.Equal(t, m.Len(), small, "Len must count the keys")
		})
	})

	t.Run("Clear", func(t *testing.T) {
		t.Parallel()

		t.Run("removes every key and keeps the order", func(t *testing.T) {
			t.Parallel()
			m, _ := descending(t, small)
			m.Clear()
			testkit.Equal(t, m.Len(), 0, "Clear must remove every key")
			m.Set(1, -1)
			m.Set(2, -2)
			testkit.Equal(t, items(t, m.All()), []int{2, 1}, "the map must keep its order")
		})
	})

	t.Run("Reset", func(t *testing.T) {
		t.Parallel()

		t.Run("removes every key and keeps the order", func(t *testing.T) {
			t.Parallel()
			m, keys := descending(t, small)
			m.Reset()
			testkit.Equal(t, m.Len(), 0, "Reset must remove every key")
			for _, k := range evens(t, small) {
				m.Set(k, -k)
			}
			testkit.Equal(t, items(t, m.All()), keys, "the refilled map must keep the order of the function")
		})
	})

	t.Run("Min", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the key that sorts first", func(t *testing.T) {
			t.Parallel()
			m, _ := descending(t, small)
			k, v, ok := m.Min()
			testkit.True(t, ok && k == top && v == -top, "Min must return the largest number")
		})
	})

	t.Run("Max", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the key that sorts last", func(t *testing.T) {
			t.Parallel()
			m, _ := descending(t, small)
			k, v, ok := m.Max()
			testkit.True(t, ok && k == 0 && v == 0, "Max must return the smallest number")
		})
	})

	t.Run("Floor", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the nearest key at or before the probe in the order of the function", func(t *testing.T) {
			t.Parallel()
			m, _ := descending(t, small)
			k, v, ok := m.Floor(7)
			testkit.True(t, ok && k == 8 && v == -8, "Floor must return the smallest number above 7")
		})
	})

	t.Run("Ceil", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the nearest key at or after the probe in the order of the function", func(t *testing.T) {
			t.Parallel()
			m, _ := descending(t, small)
			k, v, ok := m.Ceil(7)
			testkit.True(t, ok && k == 6 && v == -6, "Ceil must return the largest number below 7")
		})
	})

	t.Run("PopMin", func(t *testing.T) {
		t.Parallel()

		t.Run("removes the key that sorts first", func(t *testing.T) {
			t.Parallel()
			m, _ := descending(t, small)
			k, v, ok := m.PopMin()
			testkit.True(t, ok && k == top && v == -top && !m.Has(top), "PopMin must remove the largest number")
		})
	})

	t.Run("PopMax", func(t *testing.T) {
		t.Parallel()

		t.Run("removes the key that sorts last", func(t *testing.T) {
			t.Parallel()
			m, _ := descending(t, small)
			k, v, ok := m.PopMax()
			testkit.True(t, ok && k == 0 && v == 0 && !m.Has(0), "PopMax must remove the smallest number")
		})
	})

	t.Run("At", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the key at every index in the order of the function", func(t *testing.T) {
			t.Parallel()
			m, keys := descending(t, small)
			for i, want := range keys {
				k, v, ok := m.At(i)
				testkit.True(t, ok && k == want && v == -want, "At must return the key at the index")
			}
		})
	})

	t.Run("Rank", func(t *testing.T) {
		t.Parallel()

		t.Run("counts the keys that sort before the probe in the order of the function", func(t *testing.T) {
			t.Parallel()
			m, _ := descending(t, small)
			testkit.Equal(t, m.Rank(7), small-4, "Rank must count the keys above 7")
		})
	})

	t.Run("All", func(t *testing.T) {
		t.Parallel()

		t.Run("yields every item in the order of the function", func(t *testing.T) {
			t.Parallel()
			m, keys := descending(t, big)
			testkit.Equal(t, items(t, m.All()), keys, "All must yield the keys from largest to smallest")
		})
	})

	t.Run("Keys", func(t *testing.T) {
		t.Parallel()

		t.Run("yields every key in the order of the function", func(t *testing.T) {
			t.Parallel()
			m, keys := descending(t, small)
			testkit.Equal(t, slices.Collect(m.Keys()), keys, "Keys must yield the keys from largest to smallest")
		})
	})

	t.Run("Values", func(t *testing.T) {
		t.Parallel()

		t.Run("yields every value in the order of its key", func(t *testing.T) {
			t.Parallel()
			m, keys := descending(t, small)
			for i, v := range slices.Collect(m.Values()) {
				testkit.True(t, v == -keys[i], "Values must yield the value of each key in order")
			}
		})
	})

	t.Run("Backward", func(t *testing.T) {
		t.Parallel()

		t.Run("yields every item against the order of the function", func(t *testing.T) {
			t.Parallel()
			m, keys := descending(t, small)
			slices.Reverse(keys)
			testkit.Equal(t, items(t, m.Backward()), keys, "Backward must yield the keys from smallest to largest")
		})
	})

	t.Run("Range", func(t *testing.T) {
		t.Parallel()

		t.Run("yields the items in [lo, hi) of the order of the function", func(t *testing.T) {
			t.Parallel()
			m, _ := descending(t, small)
			testkit.Equal(t, items(t, m.Range(100, 90)), []int{100, 98, 96, 94, 92}, "Range must yield 100 down to 92")
		})
	})

	t.Run("Ascend", func(t *testing.T) {
		t.Parallel()

		t.Run("yields the items at or after the probe in the order of the function", func(t *testing.T) {
			t.Parallel()
			m, _ := descending(t, small)
			testkit.Equal(t, items(t, m.Ascend(7)), []int{6, 4, 2, 0}, "Ascend must yield the keys below 7")
		})
	})

	t.Run("Descend", func(t *testing.T) {
		t.Parallel()

		t.Run("yields the items at or before the probe against the order of the function", func(t *testing.T) {
			t.Parallel()
			m, keys := descending(t, small)
			want := slices.Clone(keys[:slices.Index(keys, 8)+1])
			slices.Reverse(want)
			testkit.Equal(t, items(t, m.Descend(7)), want, "Descend must yield the keys above 7 from smallest up")
		})
	})

	t.Run("Clone", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a map with the same items and order", func(t *testing.T) {
			t.Parallel()
			m, keys := descending(t, small)
			c := m.Clone()
			c.Set(5, -5)
			testkit.Equal(t, items(t, m.All()), keys, "the map must not see the clone's write")
			testkit.Equal(t, items(t, c.All()), slices.Insert(keys, slices.Index(keys, 4), 5),
				"the clone must hold the items in the order of the function")
		})
	})
}

// BenchmarkMapFunc reports the cost of Get and of Set of a present key on a
// MapFunc of 65,536 int keys ordered by cmp.Compare, to compare with the
// same operations on a Map. Neither allocates.
func BenchmarkMapFunc(b *testing.B) {
	keys := evens(b, benchKeys)
	m := btree.NewMapFunc[int, int](cmp.Compare[int])
	for _, k := range keys {
		m.Set(k, k)
	}

	b.Run("Get", func(b *testing.B) {
		b.ReportAllocs()
		i, sum := 0, 0
		for b.Loop() {
			v, _ := m.Get(keys[i])
			sum += v
			i = (i + 1) % benchKeys
		}
		sinkInt = sum
	})

	b.Run("Set of a present key", func(b *testing.B) {
		b.ReportAllocs()
		i := 0
		for b.Loop() {
			m.Set(keys[i], i)
			i = (i + 1) % benchKeys
		}
	})
}
