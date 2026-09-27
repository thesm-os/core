// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package btree_test

import (
	"iter"
	"maps"
	"math"
	"slices"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/btree"
)

// Sizes of the collections of the tests of Map, MapFunc and Set. A
// collection of small keys has one level of internal nodes, and one of
// big keys has two.
const (
	small = 300
	big   = 10_000
)

// benchKeys is the number of keys of the collections of the benchmarks.
const benchKeys = 1 << 16

// sinkInt keeps the results of the benchmarks alive.
var sinkInt int

// evens returns the keys 0, 2, ..., 2(n-1) in a random order, so no odd
// key is present.
func evens(tb testing.TB, n int) []int {
	tb.Helper()
	keys := testkit.SeededRand(tb).Perm(n)
	for i := range keys {
		keys[i] *= 2
	}

	return keys
}

// items returns the keys that seq yields, and fails the test when a value
// is not the negation of its key.
func items(tb testing.TB, seq iter.Seq2[int, int]) []int {
	tb.Helper()
	var keys []int
	for k, v := range seq {
		testkit.True(tb, v == -k, "the iterator must yield the value of each key")
		keys = append(keys, k)
	}

	return keys
}

// filled returns a map of the keys 0, 2, ..., 2(n-1), set in a random
// order with the negation of each key as its value, and its keys in
// ascending order.
func filled(tb testing.TB, n int) (*btree.Map[int, int], []int) {
	tb.Helper()
	var m btree.Map[int, int]
	keys := evens(tb, n)
	for _, k := range keys {
		m.Set(k, -k)
	}
	slices.Sort(keys)

	return &m, keys
}

// requireWrites ranges over seq, an iterator over m in the direction
// ahead: 1 for ascending key order and -1 for descending. At every key
// that is a multiple of 6, the loop sets the key 1 ahead, deletes the key
// 4 ahead, sets the key 3 behind, and clones m. The test fails unless the
// iteration yields its keys in order, yields every key set ahead, and
// yields no key deleted ahead or set behind.
func requireWrites(tb testing.TB, m *btree.Map[int, int], seq iter.Seq2[int, int], ahead int) {
	tb.Helper()
	yielded, setAhead, deleted := map[int]bool{}, map[int]bool{}, map[int]bool{}
	last, n := 0, 0
	for k := range seq {
		testkit.True(tb, n == 0 || (k-last)*ahead > 0, "each key must be past the key before it")
		yielded[k], last = true, k
		n++
		if k%6 != 0 {
			continue
		}
		m.Set(k+ahead, 0)
		setAhead[k+ahead] = true
		m.Delete(k + 4*ahead)
		deleted[k+4*ahead] = true
		m.Set(k-3*ahead, 0)
		m.Clone()
	}
	for k := range setAhead {
		testkit.True(tb, yielded[k], "a key set ahead must be yielded")
	}
	for k := range yielded {
		testkit.False(tb, deleted[k], "a key deleted ahead must not be yielded")
		testkit.True(tb, k%2 == 0 || setAhead[k], "a key set behind must not be yielded")
	}
}

// requireEnds fails the test unless a loop over seq that clears m ends
// after one key.
func requireEnds(tb testing.TB, m *btree.Map[int, int], seq iter.Seq2[int, int]) {
	tb.Helper()
	n := 0
	for range seq {
		n++
		m.Clear()
	}
	testkit.Equal(tb, n, 1, "the iteration must end once the map is empty")
}

func TestMap(t *testing.T) {
	t.Parallel()

	t.Run("Get", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the value of every key", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(t, big)
			for _, k := range keys {
				v, ok := m.Get(k)
				testkit.True(t, ok && v == -k, "Get must return the value of a present key")
			}
		})

		t.Run("reports an absent key", func(t *testing.T) {
			t.Parallel()
			m, _ := filled(t, big)
			for _, k := range []int{-2, -1, 1, 2*big - 1, 2 * big} {
				v, ok := m.Get(k)
				testkit.True(t, !ok && v == 0, "Get must report an absent key")
			}
			v, ok := new(btree.Map[int, int]).Get(0)
			testkit.True(t, !ok && v == 0, "an empty map has no key")
		})
	})

	t.Run("Has", func(t *testing.T) {
		t.Parallel()

		t.Run("reports whether each key is present", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(t, small)
			for p := -3; p < 2*small+3; p++ {
				_, want := slices.BinarySearch(keys, p)
				testkit.Equal(t, m.Has(p), want, "Has must report whether the key is present")
			}
			testkit.False(t, new(btree.Map[int, int]).Has(0), "an empty map has no key")
		})
	})

	t.Run("Set", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the previous value of a present key and replaces it", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(t, big)
			for _, k := range keys {
				old, ok := m.Set(k, k)
				testkit.True(t, ok && old == -k, "Set must return the previous value")
			}
			testkit.Equal(t, slices.Collect(m.Values()), keys, "Set must replace every value")
		})

		t.Run("reports a new key and inserts it", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(t, small)
			added := []int{-1, 1, 3, 2*small + 1}
			for _, k := range added {
				old, ok := m.Set(k, -k)
				testkit.True(t, !ok && old == 0, "Set must report a new key")
			}
			want := slices.Sorted(slices.Values(slices.Concat(keys, added)))
			testkit.Equal(t, items(t, m.All()), want, "Set must insert the keys in order")
		})

		for name, arrange := range map[string]func([]int){
			"ascending":  slices.Sort[[]int],
			"descending": func(keys []int) { slices.Sort(keys); slices.Reverse(keys) },
			"random":     func([]int) {},
		} {
			t.Run("keeps every key in order when keys arrive in "+name+" order", func(t *testing.T) {
				t.Parallel()
				keys := evens(t, big)
				arrange(keys)
				var m btree.Map[int, int]
				for _, k := range keys {
					m.Set(k, -k)
				}
				testkit.Equal(
					t,
					items(t, m.All()),
					slices.Sorted(slices.Values(keys)),
					"the map must hold every key in order",
				)
			})
		}

		t.Run("sorts a NaN before every number and treats every NaN as one key", func(t *testing.T) {
			t.Parallel()
			var m btree.Map[float64, int]
			for i, k := range []float64{1, math.NaN(), -1, math.Inf(-1), math.NaN()} {
				m.Set(k, i)
			}
			keys := slices.Collect(m.Keys())
			testkit.Len(t, keys, 4, "every NaN must be one key")
			testkit.True(t, math.IsNaN(keys[0]), "a NaN must sort first")
			testkit.Equal(t, keys[1:], []float64{math.Inf(-1), -1, 1}, "the numbers must follow in order")
			v, ok := m.Get(math.NaN())
			testkit.True(t, ok && v == 4, "a NaN must find the NaN key and its last value")
		})

		t.Run("treats -0.0 and 0.0 as one key and keeps the stored one", func(t *testing.T) {
			t.Parallel()
			var m btree.Map[float64, int]
			m.Set(math.Copysign(0, -1), 1)
			_, present := m.Set(0, 2)
			testkit.True(t, present, "0.0 must be the key -0.0")
			k, v, _ := m.Min()
			testkit.True(t, math.Signbit(k) && v == 2, "Set must replace the value and keep the stored key")
		})

		t.Run("sorts strings byte by byte", func(t *testing.T) {
			t.Parallel()
			var m btree.Map[string, int]
			for i, k := range []string{"b", "a", "ab", "", "B"} {
				m.Set(k, i)
			}
			testkit.Equal(t, slices.Collect(m.Keys()), []string{"", "B", "a", "ab", "b"}, "strings must sort by bytes")
		})
	})

	t.Run("Update", func(t *testing.T) {
		t.Parallel()

		t.Run("passes the value of a present key and stores the result", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(t, big)
			for _, k := range keys {
				got := m.Update(k, func(old int, exists bool) int {
					testkit.True(t, exists && old == -k, "fn must receive the value")
					return k
				})
				testkit.Equal(t, got, k, "Update must return the new value")
			}
			testkit.Equal(t, slices.Collect(m.Values()), keys, "Update must store every result")
		})

		t.Run("passes the zero value for an absent key and inserts the result", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(t, big)
			for _, k := range []int{-1, 501, 2*big + 1} {
				got := m.Update(k, func(old int, exists bool) int {
					testkit.True(t, !exists && old == 0, "fn must receive the zero value")
					return -k
				})
				testkit.Equal(t, got, -k, "Update must return the new value")
			}
			want := slices.Sorted(slices.Values(slices.Concat(keys, []int{-1, 501, 2*big + 1})))
			testkit.Equal(t, items(t, m.All()), want, "Update must insert the keys")
		})

		t.Run("inserts the first key of an empty map", func(t *testing.T) {
			t.Parallel()
			var m btree.Map[int, int]
			got := m.Update(3, func(old int, exists bool) int {
				testkit.True(t, !exists && old == 0, "fn must receive the zero value")
				return -3
			})
			testkit.Equal(t, got, -3, "Update must return the new value")
			testkit.Equal(t, items(t, m.All()), []int{3}, "the map must hold the key")
		})

		for name, n := range map[string]int{"an empty map": 0, "a map with internal nodes": big} {
			t.Run("stores the result when fn writes to "+name, func(t *testing.T) {
				t.Parallel()
				m, keys := filled(t, n)
				want := slices.Clone(keys)
				for _, key := range []int{4, 5} {
					m.Update(key, func(int, bool) int {
						for k := 1; k < 2*small; k += 2 {
							m.Set(k, -k)
							want = append(want, k)
						}
						m.Delete(8)
						return -key
					})
					want = append(want, key)
				}
				want = slices.Compact(
					slices.Sorted(slices.Values(slices.DeleteFunc(want, func(k int) bool { return k == 8 }))),
				)
				testkit.Equal(t, items(t, m.All()), want, "the map must hold the writes of fn and the results")
			})
		}
	})

	t.Run("Delete", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the value of a present key and removes it", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(t, big)
			doomed := evens(t, big)[:big/2]
			for _, k := range doomed {
				old, ok := m.Delete(k)
				testkit.True(t, ok && old == -k, "Delete must return the value")
			}
			want := slices.DeleteFunc(keys, func(k int) bool { return slices.Contains(doomed, k) })
			testkit.Equal(t, items(t, m.All()), want, "Delete must keep the other keys in order")
		})

		t.Run("reports an absent key and changes nothing", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(t, small)
			for _, k := range []int{-1, 1, 2*small + 1} {
				old, ok := m.Delete(k)
				testkit.True(t, !ok && old == 0, "Delete must report an absent key")
			}
			testkit.Equal(t, items(t, m.All()), keys, "Delete of an absent key must change nothing")
			old, ok := new(btree.Map[int, int]).Delete(1)
			testkit.True(t, !ok && old == 0, "an empty map has no key")
		})

		t.Run("removes every key and leaves a map that accepts keys", func(t *testing.T) {
			t.Parallel()
			m, _ := filled(t, big)
			for _, k := range evens(t, big) {
				m.Delete(k)
			}
			testkit.Equal(t, m.Len(), 0, "the map must be empty")
			m.Set(1, -1)
			testkit.Equal(t, items(t, m.All()), []int{1}, "the empty map must accept a key")
		})
	})

	t.Run("DeleteRange", func(t *testing.T) {
		t.Parallel()

		t.Run("removes the keys in [lo, hi) and returns how many", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(t, big)
			r := testkit.SeededRand(t)
			for range 200 {
				lo := r.IntN(2*big+10) - 5
				hi := lo + r.IntN(300)
				i, _ := slices.BinarySearch(keys, lo)
				j, _ := slices.BinarySearch(keys, hi)
				testkit.Equal(t, m.DeleteRange(lo, hi), j-i, "DeleteRange must count the keys it removed")
				keys = slices.Delete(keys, i, j)
			}
			testkit.Equal(t, items(t, m.All()), keys, "DeleteRange must remove exactly the ranges")
		})

		t.Run("removes every key of a range that spans the map", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(t, big)
			testkit.Equal(t, m.DeleteRange(keys[0], keys[big-1]+1), big, "DeleteRange must remove every key")
			testkit.Equal(t, m.Len(), 0, "the map must be empty")
		})

		t.Run("removes nothing when hi sorts at or before lo", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(t, small)
			testkit.Equal(t, m.DeleteRange(10, 10), 0, "an empty range must remove nothing")
			testkit.Equal(t, m.DeleteRange(30, 10), 0, "a reversed range must remove nothing")
			testkit.Equal(t, items(t, m.All()), keys, "the map must be unchanged")
			testkit.Equal(t, new(btree.Map[int, int]).DeleteRange(0, 10), 0, "an empty map has no key")
		})
	})

	t.Run("Len", func(t *testing.T) {
		t.Parallel()

		t.Run("counts the keys after inserts and deletes", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(t, big)
			testkit.Equal(t, m.Len(), big, "Len must count every key")
			m.Set(keys[0], 1)
			testkit.Equal(t, m.Len(), big, "a new value must not change Len")
			for _, k := range keys[:100] {
				m.Delete(k)
			}
			testkit.Equal(t, m.Len(), big-100, "Len must drop the deleted keys")
			testkit.Equal(t, new(btree.Map[int, int]).Len(), 0, "an empty map has no key")
		})
	})

	t.Run("Clear", func(t *testing.T) {
		t.Parallel()

		t.Run("removes every key and leaves a map that accepts keys", func(t *testing.T) {
			t.Parallel()
			m, _ := filled(t, big)
			m.Clear()
			testkit.Equal(t, m.Len(), 0, "Clear must remove every key")
			testkit.Len(t, items(t, m.All()), 0, "a cleared map has no item")
			m.Set(5, -5)
			testkit.Equal(t, items(t, m.All()), []int{5}, "the map must accept a key")
		})

		t.Run("leaves a clone unchanged", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(t, big)
			c := m.Clone()
			m.Clear()
			testkit.Equal(t, items(t, c.All()), keys, "the clone must keep its keys")
		})
	})

	t.Run("Reset", func(t *testing.T) {
		t.Parallel()

		t.Run("removes every key and leaves a map that refills with every key in order", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(t, big)
			m.Reset()
			testkit.Equal(t, m.Len(), 0, "Reset must remove every key")
			testkit.Len(t, items(t, m.All()), 0, "a reset map has no item")
			for _, k := range evens(t, big) {
				m.Set(k, -k)
			}
			testkit.Equal(t, items(t, m.All()), keys, "the refilled map must hold every key in order")
		})

		t.Run("leaves a clone unchanged", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(t, big)
			c := m.Clone()
			m.Set(1, -1)
			m.Reset()
			m.Set(3, -3)
			testkit.Equal(t, items(t, c.All()), keys, "the clone must keep its keys")
		})

		t.Run("ends an iteration whose loop resets the map", func(t *testing.T) {
			t.Parallel()
			m, _ := filled(t, big)
			n := 0
			for range m.All() {
				n++
				m.Reset()
			}
			testkit.Equal(t, n, 1, "the iteration must end once the map is empty")
		})
	})

	t.Run("Min", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the smallest key and its value", func(t *testing.T) {
			t.Parallel()
			for _, n := range []int{1, small, big} {
				m, _ := filled(t, n)
				k, v, ok := m.Min()
				testkit.True(t, ok && k == 0 && v == 0, "Min must return the smallest key")
			}
			k, v, ok := new(btree.Map[int, int]).Min()
			testkit.True(t, !ok && k == 0 && v == 0, "an empty map has no smallest key")
		})
	})

	t.Run("Max", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the largest key and its value", func(t *testing.T) {
			t.Parallel()
			for _, n := range []int{1, small, big} {
				m, keys := filled(t, n)
				k, v, ok := m.Max()
				testkit.True(t, ok && k == keys[n-1] && v == -k, "Max must return the largest key")
			}
			k, v, ok := new(btree.Map[int, int]).Max()
			testkit.True(t, !ok && k == 0 && v == 0, "an empty map has no largest key")
		})
	})

	t.Run("Floor", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the largest key at or before every probe", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(t, small)
			for p := -3; p < 2*small+3; p++ {
				i, found := slices.BinarySearch(keys, p)
				if found {
					i++
				}
				k, v, ok := m.Floor(p)
				if i == 0 {
					testkit.True(t, !ok && k == 0 && v == 0, "Floor must report that no key is at or before the probe")
					continue
				}
				testkit.True(
					t,
					ok && k == keys[i-1] && v == -k,
					"Floor must return the nearest key at or before the probe",
				)
			}
			k, v, ok := new(btree.Map[int, int]).Floor(0)
			testkit.True(t, !ok && k == 0 && v == 0, "an empty map has no key")
		})
	})

	t.Run("Ceil", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the smallest key at or after every probe", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(t, small)
			for p := -3; p < 2*small+3; p++ {
				i, _ := slices.BinarySearch(keys, p)
				k, v, ok := m.Ceil(p)
				if i == len(keys) {
					testkit.True(t, !ok && k == 0 && v == 0, "Ceil must report that no key is at or after the probe")
					continue
				}
				testkit.True(t, ok && k == keys[i] && v == -k, "Ceil must return the nearest key at or after the probe")
			}
			k, v, ok := new(btree.Map[int, int]).Ceil(0)
			testkit.True(t, !ok && k == 0 && v == 0, "an empty map has no key")
		})
	})

	t.Run("PopMin", func(t *testing.T) {
		t.Parallel()

		t.Run("removes the keys in ascending order", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(t, big)
			for _, want := range keys {
				k, v, ok := m.PopMin()
				testkit.True(t, ok && k == want && v == -want, "PopMin must return the smallest key")
			}
			k, v, ok := m.PopMin()
			testkit.True(t, !ok && k == 0 && v == 0, "an empty map has no key to remove")
		})

		t.Run("leaves a clone unchanged", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(t, big)
			c := m.Clone()
			for range big / 2 {
				m.PopMin()
			}
			testkit.Equal(t, items(t, c.All()), keys, "the clone must keep its keys")
		})
	})

	t.Run("PopMax", func(t *testing.T) {
		t.Parallel()

		t.Run("removes the keys in descending order", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(t, big)
			for _, want := range slices.Backward(keys) {
				k, v, ok := m.PopMax()
				testkit.True(t, ok && k == want && v == -want, "PopMax must return the largest key")
			}
			k, v, ok := m.PopMax()
			testkit.True(t, !ok && k == 0 && v == 0, "an empty map has no key to remove")
		})

		t.Run("leaves a clone unchanged", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(t, big)
			c := m.Clone()
			for range big / 2 {
				m.PopMax()
			}
			testkit.Equal(t, items(t, c.All()), keys, "the clone must keep its keys")
		})
	})

	t.Run("At", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the key at every index and its value", func(t *testing.T) {
			t.Parallel()
			for _, n := range []int{1, small, big} {
				m, keys := filled(t, n)
				for i, want := range keys {
					k, v, ok := m.At(i)
					testkit.True(t, ok && k == want && v == -want, "At must return the key at the index")
				}
			}
		})

		t.Run("reports an index outside [0, Len)", func(t *testing.T) {
			t.Parallel()
			m, _ := filled(t, small)
			for _, i := range []int{-1, small, small + 1} {
				k, v, ok := m.At(i)
				testkit.True(t, !ok && k == 0 && v == 0, "At must reject an index outside [0, Len)")
			}
			k, v, ok := new(btree.Map[int, int]).At(0)
			testkit.True(t, !ok && k == 0 && v == 0, "an empty map has no index")
		})
	})

	t.Run("Rank", func(t *testing.T) {
		t.Parallel()

		t.Run("counts the keys before every probe", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(t, big)
			for p := -3; p < 2*big+3; p++ {
				want, _ := slices.BinarySearch(keys, p)
				testkit.True(t, m.Rank(p) == want, "Rank must count the keys before the probe")
			}
			testkit.Equal(t, new(btree.Map[int, int]).Rank(5), 0, "an empty map has no key")
		})
	})

	t.Run("All", func(t *testing.T) {
		t.Parallel()

		t.Run("yields every item in ascending order", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(t, big)
			testkit.Equal(t, items(t, m.All()), keys, "All must yield every item in order")
			testkit.Len(t, items(t, new(btree.Map[int, int]).All()), 0, "an empty map has no item")
		})

		t.Run("stops when the loop breaks", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(t, big)
			var got []int
			for k := range m.All() {
				if got = append(got, k); len(got) == 100 {
					break
				}
			}
			testkit.Equal(t, got, keys[:100], "All must stop at the break")
		})

		t.Run("continues past the last key it yielded when the loop writes to the map", func(t *testing.T) {
			t.Parallel()
			m, _ := filled(t, big)
			requireWrites(t, m, m.All(), 1)
		})

		t.Run("ends when the loop empties the map", func(t *testing.T) {
			t.Parallel()
			m, _ := filled(t, big)
			requireEnds(t, m, m.All())
		})
	})

	t.Run("Keys", func(t *testing.T) {
		t.Parallel()

		t.Run("yields every key in ascending order and stops when the loop breaks", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(t, big)
			testkit.Equal(t, slices.Collect(m.Keys()), keys, "Keys must yield every key in order")
			n := 0
			for range m.Keys() {
				if n++; n == 10 {
					break
				}
			}
			testkit.Equal(t, n, 10, "Keys must stop at the break")
		})
	})

	t.Run("Values", func(t *testing.T) {
		t.Parallel()

		t.Run("yields every value in the order of its key and stops when the loop breaks", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(t, big)
			for i, v := range slices.Collect(m.Values()) {
				testkit.True(t, v == -keys[i], "Values must yield the value of each key in order")
			}
			n := 0
			for range m.Values() {
				if n++; n == 10 {
					break
				}
			}
			testkit.Equal(t, n, 10, "Values must stop at the break")
		})
	})

	t.Run("Backward", func(t *testing.T) {
		t.Parallel()

		t.Run("yields every item in descending order", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(t, big)
			slices.Reverse(keys)
			testkit.Equal(t, items(t, m.Backward()), keys, "Backward must yield every item in reverse order")
			testkit.Len(t, items(t, new(btree.Map[int, int]).Backward()), 0, "an empty map has no item")
		})

		t.Run("stops when the loop breaks", func(t *testing.T) {
			t.Parallel()
			m, _ := filled(t, big)
			n := 0
			for range m.Backward() {
				if n++; n == 100 {
					break
				}
			}
			testkit.Equal(t, n, 100, "Backward must stop at the break")
		})

		t.Run("continues past the last key it yielded when the loop writes to the map", func(t *testing.T) {
			t.Parallel()
			m, _ := filled(t, big)
			requireWrites(t, m, m.Backward(), -1)
		})

		t.Run("ends when the loop empties the map", func(t *testing.T) {
			t.Parallel()
			m, _ := filled(t, big)
			requireEnds(t, m, m.Backward())
		})
	})

	t.Run("Range", func(t *testing.T) {
		t.Parallel()

		t.Run("yields the items in [lo, hi) in ascending order", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(t, big)
			r := testkit.SeededRand(t)
			for range 300 {
				lo := r.IntN(2*big+10) - 5
				hi := lo + r.IntN(400) - 50
				i, _ := slices.BinarySearch(keys, lo)
				j, _ := slices.BinarySearch(keys, hi)
				want := keys[i:max(i, j)]
				if len(want) == 0 {
					want = nil
				}
				testkit.Equal(t, items(t, m.Range(lo, hi)), want, "Range must yield exactly the keys in the range")
			}
			testkit.Len(t, items(t, new(btree.Map[int, int]).Range(0, 10)), 0, "an empty map has no item")
		})

		t.Run("yields nothing between two adjacent keys", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(t, big)
			for _, k := range keys {
				testkit.Len(t, items(t, m.Range(k+1, k+2)), 0, "no key lies between two adjacent keys")
			}
		})

		t.Run("stops when the loop breaks", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(t, big)
			n := 0
			for range m.Range(keys[10], keys[500]) {
				if n++; n == 50 {
					break
				}
			}
			testkit.Equal(t, n, 50, "Range must stop at the break")
		})

		t.Run("continues past the last key it yielded when the loop writes to the map", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(t, big)
			requireWrites(t, m, m.Range(keys[0], keys[big-1]+2), 1)
		})

		t.Run("ends when the loop empties the map", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(t, big)
			requireEnds(t, m, m.Range(keys[0], keys[big-1]))
		})
	})

	t.Run("Ascend", func(t *testing.T) {
		t.Parallel()

		t.Run("yields the items at or after every probe in ascending order", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(t, small)
			for p := -3; p < 2*small+3; p++ {
				i, _ := slices.BinarySearch(keys, p)
				want := keys[i:]
				if len(want) == 0 {
					want = nil
				}
				testkit.Equal(t, items(t, m.Ascend(p)), want, "Ascend must yield the keys at or after the probe")
			}
		})

		t.Run("stops when the loop breaks", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(t, big)
			n := 0
			for range m.Ascend(keys[5]) {
				if n++; n == 70 {
					break
				}
			}
			testkit.Equal(t, n, 70, "Ascend must stop at the break")
		})

		t.Run("continues past the last key it yielded when the loop writes to the map", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(t, big)
			requireWrites(t, m, m.Ascend(keys[0]), 1)
		})

		t.Run("ends when the loop empties the map", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(t, big)
			requireEnds(t, m, m.Ascend(keys[0]))
		})
	})

	t.Run("Descend", func(t *testing.T) {
		t.Parallel()

		t.Run("yields the items at or before every probe in descending order", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(t, small)
			for p := -3; p < 2*small+3; p++ {
				i, found := slices.BinarySearch(keys, p)
				if found {
					i++
				}
				var want []int
				for _, k := range slices.Backward(keys[:i]) {
					want = append(want, k)
				}
				testkit.Equal(t, items(t, m.Descend(p)), want, "Descend must yield the keys at or before the probe")
			}
		})

		t.Run("stops when the loop breaks", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(t, big)
			n := 0
			for range m.Descend(keys[big-5]) {
				if n++; n == 70 {
					break
				}
			}
			testkit.Equal(t, n, 70, "Descend must stop at the break")
		})

		t.Run("continues past the last key it yielded when the loop writes to the map", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(t, big)
			requireWrites(t, m, m.Descend(keys[big-1]), -1)
		})

		t.Run("ends when the loop empties the map", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(t, big)
			requireEnds(t, m, m.Descend(keys[big-1]))
		})
	})

	t.Run("Clone", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a map with the same items", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(t, big)
			testkit.Equal(t, items(t, m.Clone().All()), keys, "the clone must hold the items")
			testkit.Len(t, items(t, new(btree.Map[int, int]).Clone().All()), 0, "the clone of an empty map is empty")
		})

		t.Run("keeps the writes to each map out of the other", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(t, big)
			c := m.Clone()
			models := [2]map[int]int{{}, {}}
			for _, k := range keys {
				models[0][k], models[1][k] = -k, -k
			}
			r := testkit.SeededRand(t)
			for i := range 20_000 {
				target, model := m, models[0]
				if i%2 == 1 {
					target, model = c, models[1]
				}
				if k := r.IntN(2 * big); r.IntN(2) == 0 {
					target.Set(k, -k)
					model[k] = -k
				} else {
					target.Delete(k)
					delete(model, k)
				}
				if i%5_000 == 0 {
					c = c.Clone()
				}
			}
			testkit.Equal(
				t,
				items(t, m.All()),
				slices.Sorted(maps.Keys(models[0])),
				"the map must hold only its own writes",
			)
			testkit.Equal(
				t,
				items(t, c.All()),
				slices.Sorted(maps.Keys(models[1])),
				"the clone must hold only its own writes",
			)
		})
	})
}

// BenchmarkMap reports the cost of the operations of a Map of 65,536 int
// keys set in random order. Lookups, iteration, and a Delete and Set of
// the same key allocate nothing. A map built from empty allocates one
// object for each leaf and two for each internal node, and a map that
// Reset emptied refills without allocating. A clone followed by a Set
// allocates the new map and the copied path.
func BenchmarkMap(b *testing.B) {
	keys := evens(b, benchKeys)
	var m btree.Map[int, int]
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

	b.Run("Delete then Set of the same key", func(b *testing.B) {
		b.ReportAllocs()
		i := 0
		for b.Loop() {
			m.Delete(keys[i])
			m.Set(keys[i], i)
			i = (i + 1) % benchKeys
		}
	})

	b.Run("Set of 65,536 keys in random order", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			var fresh btree.Map[int, int]
			for _, k := range keys {
				fresh.Set(k, k)
			}
		}
	})

	b.Run("Set of 65,536 keys in ascending order", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			var fresh btree.Map[int, int]
			for k := range benchKeys {
				fresh.Set(k, k)
			}
		}
	})

	b.Run("Reset then Set of 65,536 keys in random order", func(b *testing.B) {
		// The first Reset grows the free lists, so the loop measures the
		// fills that follow it.
		var reused btree.Map[int, int]
		for _, k := range keys {
			reused.Set(k, k)
		}
		reused.Reset()
		for _, k := range keys {
			reused.Set(k, k)
		}
		b.ReportAllocs()
		for b.Loop() {
			reused.Reset()
			for _, k := range keys {
				reused.Set(k, k)
			}
		}
	})

	b.Run("All", func(b *testing.B) {
		b.ReportAllocs()
		sum := 0
		for b.Loop() {
			for _, v := range m.All() {
				sum += v
			}
		}
		sinkInt = sum
	})

	b.Run("Range of 64 keys", func(b *testing.B) {
		b.ReportAllocs()
		i, sum := 0, 0
		for b.Loop() {
			for _, v := range m.Range(keys[i], keys[i]+128) {
				sum += v
			}
			i = (i + 1) % benchKeys
		}
		sinkInt = sum
	})

	b.Run("Floor", func(b *testing.B) {
		b.ReportAllocs()
		i, sum := 0, 0
		for b.Loop() {
			k, _, _ := m.Floor(keys[i] + 1)
			sum += k
			i = (i + 1) % benchKeys
		}
		sinkInt = sum
	})

	b.Run("Rank", func(b *testing.B) {
		b.ReportAllocs()
		i, sum := 0, 0
		for b.Loop() {
			sum += m.Rank(keys[i])
			i = (i + 1) % benchKeys
		}
		sinkInt = sum
	})

	b.Run("At", func(b *testing.B) {
		b.ReportAllocs()
		i, sum := 0, 0
		for b.Loop() {
			k, _, _ := m.At(i)
			sum += k
			i = (i + 1) % benchKeys
		}
		sinkInt = sum
	})

	b.Run("Clone then Set", func(b *testing.B) {
		b.ReportAllocs()
		i := 0
		for b.Loop() {
			c := m.Clone()
			c.Set(keys[i], 0)
			i = (i + 1) % benchKeys
		}
	})
}
