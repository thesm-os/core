// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package btree_test

import (
	"fmt"
	"iter"
	"maps"
	"math"
	"math/rand/v2"
	"slices"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/btree"
)

// Sizes of the collections of the tests of Map, MapFunc and Set. A
// collection of small keys has one level of internal nodes, and one of big
// keys has two.
const (
	small = 300
	big   = 10_000
)

// benchKeys is the number of keys of the collections of the benchmarks.
const benchKeys = 1 << 16

// Allocation counts of a Map or a Set of benchKeys keys. A fill of an empty
// collection allocates 1 object for each leaf and 2 for each internal node
// of the tree that it builds.
const (
	// fillAllocs is the number of allocations of a fill with the keys of
	// evens(benchKeys) in their order. The random order leaves most leaves
	// partly full, so the tree has more nodes than an ascending fill builds.
	fillAllocs = 1_563

	// ascendingAllocs is the number of allocations of a fill with the keys
	// 0 to benchKeys-1 in ascending order, which fills every leaf: 1,041
	// leaves of 63 items, and 17 internal nodes above them and a root at 2
	// allocations each.
	ascendingAllocs = 1_077

	// cloneSetAllocs is the number of allocations of a Clone followed by a
	// Set: 1 for the new map, 1 for the leaf that the Set copies, and 2 for
	// each of the two internal nodes on its path.
	cloneSetAllocs = 6
)

// rangeOp is one DeleteRange of a case: the start of the range and its
// length.
type rangeOp struct {
	lo, n int
}

// cloneOp is one write of the clone case: whether it writes to the clone,
// whether it sets or deletes, and the key.
type cloneOp struct {
	toClone, set bool
	key          int
}

// item is the result of a lookup of a map of ints: the key and the value
// that it found, and whether it found them. A property compares the item
// of a probe with the item that the sorted keys predict.
type item struct {
	key, value int
	found      bool
}

func TestMap(t *testing.T) {
	t.Parallel()

	t.Run("Get", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the value of every key", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(big)
			assert.Total(t, func(k int) error {
				if v, ok := m.Get(k); !ok || v != -k {
					return fmt.Errorf("key %d: Get returned %d, %t", k, v, ok)
				}

				return nil
			}, keys, "Get must return the value of every present key")
		})

		t.Run("reports an absent key", func(t *testing.T) {
			t.Parallel()
			m, _ := filled(big)
			assert.Total(t, func(k int) error {
				if v, ok := m.Get(k); ok || v != 0 {
					return fmt.Errorf("key %d: Get returned %d, %t", k, v, ok)
				}

				return nil
			}, []int{-2, -1, 1, 2*big - 1, 2 * big}, "Get must report every absent key")
			v, ok := new(btree.Map[int, int]).Get(0)
			expect.False(t, ok, "an empty map has no key")
			expect.Equal(t, v, 0, "a miss must return the zero value")
		})
	})

	t.Run("Has", func(t *testing.T) {
		t.Parallel()

		t.Run("reports whether each key is present", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(small)
			prop.Equal(t, m.Has, func(p int) bool {
				_, found := slices.BinarySearch(keys, p)

				return found
			}, "Has must report whether each probe is a key",
				prop.Using(prop.Integer(-3, 2*small+2)), prop.Example(-1), prop.Example(0), prop.Example(2*small-2))
		})

		t.Run("reports false for an empty map", func(t *testing.T) {
			t.Parallel()
			assert.False(t, new(btree.Map[int, int]).Has(0), "an empty map has no key")
		})
	})

	t.Run("Set", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the previous value of a present key", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(big)
			assert.Total(t, func(k int) error {
				if old, ok := m.Set(k, k); !ok || old != -k {
					return fmt.Errorf("key %d: Set returned %d, %t", k, old, ok)
				}

				return nil
			}, keys, "Set must return the previous value of every present key")
		})

		t.Run("replaces the value of a present key", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(big)
			for _, k := range keys {
				m.Set(k, k)
			}
			assert.Equal(t, slices.Collect(m.Values()), keys, "Set must replace every value")
		})

		t.Run("reports false for a new key", func(t *testing.T) {
			t.Parallel()
			m, _ := filled(small)
			assert.Total(t, func(k int) error {
				if old, ok := m.Set(k, -k); ok || old != 0 {
					return fmt.Errorf("key %d: Set returned %d, %t", k, old, ok)
				}

				return nil
			}, []int{-1, 1, 3, 2*small + 1}, "Set must report every new key")
		})

		t.Run("inserts a new key in order", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(small)
			added := []int{-1, 1, 3, 2*small + 1}
			for _, k := range added {
				m.Set(k, -k)
			}
			want := slices.Sorted(slices.Values(slices.Concat(keys, added)))
			assert.Equal(t, items(t, m.All()), want, "Set must insert the keys in order")
		})

		tests := []struct {
			name    string
			arrange func([]int)
		}{
			{"keeps every key in order when keys arrive in ascending order", slices.Sort[[]int]},
			{
				"keeps every key in order when keys arrive in descending order",
				func(keys []int) { slices.Sort(keys); slices.Reverse(keys) },
			},
			{"keeps every key in order when keys arrive in random order", func([]int) {}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				keys := evens(big)
				tt.arrange(keys)
				var m btree.Map[int, int]
				for _, k := range keys {
					m.Set(k, -k)
				}
				assert.Equal(t, items(t, m.All()), slices.Sorted(slices.Values(keys)),
					"the map must contain every key in order")
			})
		}

		t.Run("sorts a NaN before every number", func(t *testing.T) {
			t.Parallel()
			var m btree.Map[float64, int]
			for i, k := range []float64{1, math.NaN(), -1, math.Inf(-1)} {
				m.Set(k, i)
			}
			keys := slices.Collect(m.Keys())
			assert.True(t, math.IsNaN(keys[0]), "a NaN must sort first")
			assert.Equal(t, keys[1:], []float64{math.Inf(-1), -1, 1}, "the numbers must follow in order")
		})

		t.Run("treats every NaN as one key", func(t *testing.T) {
			t.Parallel()
			var m btree.Map[float64, int]
			_, first := m.Set(math.NaN(), 1)
			_, second := m.Set(math.NaN(), 2)
			expect.False(t, first, "the first NaN must be a new key")
			expect.True(t, second, "the second NaN must find the first")
			v, _ := m.Get(math.NaN())
			expect.Equal(t, v, 2, "a NaN must find the NaN key and its last value")
			expect.Equal(t, m.Len(), 1, "every NaN must be one key")
		})

		t.Run("keeps the stored key -0.0 for a Set of 0.0", func(t *testing.T) {
			t.Parallel()
			var m btree.Map[float64, int]
			m.Set(math.Copysign(0, -1), 1)
			_, present := m.Set(0, 2)
			assert.True(t, present, "0.0 must be the key -0.0")
			k, v, _ := m.Min()
			expect.True(t, math.Signbit(k), "Set must keep the stored key")
			expect.Equal(t, v, 2, "Set must replace the value")
		})

		t.Run("sorts strings byte by byte", func(t *testing.T) {
			t.Parallel()
			var m btree.Map[string, int]
			for i, k := range []string{"b", "a", "ab", "", "B"} {
				m.Set(k, i)
			}
			assert.Equal(t, slices.Collect(m.Keys()), []string{"", "B", "a", "ab", "b"}, "strings must sort by bytes")
		})
	})

	t.Run("Update", func(t *testing.T) {
		t.Parallel()

		t.Run("passes fn the value of a present key", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(big)
			assert.Total(t, func(k int) error {
				var passed item
				m.Update(k, func(old int, exists bool) int {
					passed = item{key: k, value: old, found: exists}

					return old
				})
				if passed != (item{key: k, value: -k, found: true}) {
					return fmt.Errorf("key %d: Update passed %+v", k, passed)
				}

				return nil
			}, keys, "Update must pass fn the value of every present key")
		})

		t.Run("stores the result of fn for a present key", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(big)
			assert.Total(t, func(k int) error {
				if got := m.Update(k, func(int, bool) int { return k }); got != k {
					return fmt.Errorf("key %d: Update returned %d", k, got)
				}

				return nil
			}, keys, "Update must return the result of fn")
			assert.Equal(t, slices.Collect(m.Values()), keys, "Update must store every result")
		})

		t.Run("passes fn the zero value for an absent key", func(t *testing.T) {
			t.Parallel()
			m, _ := filled(big)
			assert.Total(t, func(k int) error {
				var passed item
				m.Update(k, func(old int, exists bool) int {
					passed = item{key: k, value: old, found: exists}

					return -k
				})
				if passed != (item{key: k}) {
					return fmt.Errorf("key %d: Update passed %+v", k, passed)
				}

				return nil
			}, []int{-1, 501, 2*big + 1}, "Update must pass fn the zero value of every absent key")
		})

		t.Run("inserts the result of fn for an absent key", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(big)
			added := []int{-1, 501, 2*big + 1}
			assert.Total(t, func(k int) error {
				if got := m.Update(k, func(int, bool) int { return -k }); got != -k {
					return fmt.Errorf("key %d: Update returned %d", k, got)
				}

				return nil
			}, added, "Update must return the result of fn")
			want := slices.Sorted(slices.Values(slices.Concat(keys, added)))
			assert.Equal(t, items(t, m.All()), want, "Update must insert the keys")
		})

		t.Run("inserts the first key of an empty map", func(t *testing.T) {
			t.Parallel()
			var m btree.Map[int, int]
			var old int
			exists := true
			got := m.Update(3, func(o int, e bool) int {
				old, exists = o, e

				return -3
			})
			expect.False(t, exists, "fn must receive no key")
			expect.Equal(t, old, 0, "fn must receive the zero value")
			expect.Equal(t, got, -3, "Update must return the new value")
			expect.Equal(t, items(t, m.All()), []int{3}, "the map must contain the key")
		})

		tests := []struct {
			name string
			n    int
		}{
			{"stores the result when fn writes to an empty map", 0},
			{"stores the result when fn writes to a map with internal nodes", big},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				m, keys := filled(tt.n)
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
				assert.Equal(t, items(t, m.All()), want, "the map must contain the writes of fn and the results")
			})
		}
	})

	t.Run("Delete", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the value of a present key", func(t *testing.T) {
			t.Parallel()
			m, _ := filled(big)
			assert.Total(t, func(k int) error {
				if old, ok := m.Delete(k); !ok || old != -k {
					return fmt.Errorf("key %d: Delete returned %d, %t", k, old, ok)
				}

				return nil
			}, evens(big)[:big/2], "Delete must return the value of every present key")
		})

		t.Run("keeps the other keys in order", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(big)
			doomed := evens(big)[:big/2]
			for _, k := range doomed {
				m.Delete(k)
			}
			want := slices.DeleteFunc(keys, func(k int) bool { return slices.Contains(doomed, k) })
			assert.Equal(t, items(t, m.All()), want, "Delete must keep the other keys in order")
		})

		t.Run("reports false for an absent key", func(t *testing.T) {
			t.Parallel()
			m, _ := filled(small)
			assert.Total(t, func(k int) error {
				if old, ok := m.Delete(k); ok || old != 0 {
					return fmt.Errorf("key %d: Delete returned %d, %t", k, old, ok)
				}

				return nil
			}, []int{-1, 1, 2*small + 1}, "Delete must report every absent key")
			old, ok := new(btree.Map[int, int]).Delete(1)
			expect.False(t, ok, "an empty map has no key")
			expect.Equal(t, old, 0, "a miss must return the zero value")
		})

		t.Run("leaves the map unchanged for an absent key", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(small)
			for _, k := range []int{-1, 1, 2*small + 1} {
				m.Delete(k)
			}
			assert.Equal(t, items(t, m.All()), keys, "Delete of an absent key must change nothing")
		})

		t.Run("leaves an empty map that accepts keys when it removes every key", func(t *testing.T) {
			t.Parallel()
			m, _ := filled(big)
			for _, k := range evens(big) {
				m.Delete(k)
			}
			assert.Equal(t, m.Len(), 0, "the map must be empty")
			m.Set(1, -1)
			assert.Equal(t, items(t, m.All()), []int{1}, "the empty map must accept a key")
		})
	})

	t.Run("DeleteRange", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the number of keys that it removes", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "DeleteRange must count the keys that leave the map", func(c *prop.Case) {
				m, _ := filled(big)
				ops := c.Draw(prop.List(prop.Composite(func(c *prop.Case) rangeOp {
					return rangeOp{lo: c.Draw(prop.Integer(-5, 2*big+5), "lo"), n: c.Draw(prop.Integer(0, 300), "n")}
				}), prop.MaxSize(20)), "ranges")
				for _, op := range ops {
					before := m.Len()
					assert.Equal(c, m.DeleteRange(op.lo, op.lo+op.n), before-m.Len(),
						"DeleteRange must count the keys it removed")
				}
			}, prop.Cases(20))
		})

		t.Run("removes exactly the keys in [lo, hi)", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "DeleteRange must remove exactly the keys of each range", func(c *prop.Case) {
				m, keys := filled(big)
				ops := c.Draw(prop.List(prop.Composite(func(c *prop.Case) rangeOp {
					return rangeOp{lo: c.Draw(prop.Integer(-5, 2*big+5), "lo"), n: c.Draw(prop.Integer(0, 300), "n")}
				}), prop.MaxSize(20)), "ranges")
				for _, op := range ops {
					i, _ := slices.BinarySearch(keys, op.lo)
					j, _ := slices.BinarySearch(keys, op.lo+op.n)
					m.DeleteRange(op.lo, op.lo+op.n)
					keys = slices.Delete(keys, i, j)
				}
				assert.Equal(c, items(c, m.All()), keys, "DeleteRange must remove exactly the ranges",
					assert.EquateEmpty())
			}, prop.Cases(20))
		})

		t.Run("removes every key of a range that spans the map", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(big)
			assert.Equal(t, m.DeleteRange(keys[0], keys[big-1]+1), big, "DeleteRange must remove every key")
			assert.Equal(t, m.Len(), 0, "the map must be empty")
		})

		t.Run("removes nothing when hi sorts at or before lo", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(small)
			assert.Equal(t, m.DeleteRange(10, 10), 0, "an empty range must remove nothing")
			assert.Equal(t, m.DeleteRange(30, 10), 0, "a reversed range must remove nothing")
			assert.Equal(t, items(t, m.All()), keys, "the map must be unchanged")
			assert.Equal(t, new(btree.Map[int, int]).DeleteRange(0, 10), 0, "an empty map has no key")
		})
	})

	t.Run("Len", func(t *testing.T) {
		t.Parallel()

		t.Run("counts the keys after inserts and deletes", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(big)
			assert.Equal(t, m.Len(), big, "Len must count every key")
			m.Set(keys[0], 1)
			assert.Equal(t, m.Len(), big, "a new value must not change Len")
			for _, k := range keys[:100] {
				m.Delete(k)
			}
			assert.Equal(t, m.Len(), big-100, "Len must drop the deleted keys")
			assert.Equal(t, new(btree.Map[int, int]).Len(), 0, "an empty map has no key")
		})
	})

	t.Run("Clear", func(t *testing.T) {
		t.Parallel()

		t.Run("removes every key", func(t *testing.T) {
			t.Parallel()
			m, _ := filled(big)
			m.Clear()
			assert.Equal(t, m.Len(), 0, "Clear must remove every key")
			assert.Empty(t, items(t, m.All()), "a cleared map has no item")
		})

		t.Run("leaves a map that accepts keys", func(t *testing.T) {
			t.Parallel()
			m, _ := filled(big)
			m.Clear()
			m.Set(5, -5)
			assert.Equal(t, items(t, m.All()), []int{5}, "the map must accept a key")
		})

		t.Run("leaves a clone unchanged", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(big)
			c := m.Clone()
			m.Clear()
			assert.Equal(t, items(t, c.All()), keys, "the clone must keep its keys")
		})
	})

	t.Run("Reset", func(t *testing.T) {
		t.Parallel()

		t.Run("removes every key", func(t *testing.T) {
			t.Parallel()
			m, _ := filled(big)
			m.Reset()
			assert.Equal(t, m.Len(), 0, "Reset must remove every key")
			assert.Empty(t, items(t, m.All()), "a reset map has no item")
		})

		t.Run("leaves a map that refills with every key in order", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(big)
			m.Reset()
			for _, k := range evens(big) {
				m.Set(k, -k)
			}
			assert.Equal(t, items(t, m.All()), keys, "the refilled map must contain every key in order")
		})

		t.Run("leaves a clone unchanged", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(big)
			c := m.Clone()
			m.Set(1, -1)
			m.Reset()
			m.Set(3, -3)
			assert.Equal(t, items(t, c.All()), keys, "the clone must keep its keys")
		})

		t.Run("ends an iteration whose loop resets the map", func(t *testing.T) {
			t.Parallel()
			m, _ := filled(big)
			n := 0
			for range m.All() {
				if n++; n > 1 {
					break
				}
				m.Reset()
			}
			assert.Equal(t, n, 1, "the iteration must end once the map is empty")
		})
	})

	t.Run("Min", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the smallest key and its value", func(t *testing.T) {
			t.Parallel()
			for _, n := range []int{1, small, big} {
				m, _ := filled(n)
				k, v, ok := m.Min()
				assert.True(t, ok, "Min must find a key")
				expect.Equal(t, k, 0, "Min must return the smallest key")
				expect.Equal(t, v, 0, "Min must return the value of the smallest key")
			}
		})

		t.Run("reports false for an empty map", func(t *testing.T) {
			t.Parallel()
			k, v, ok := new(btree.Map[int, int]).Min()
			expect.False(t, ok, "an empty map has no smallest key")
			expect.Equal(t, k, 0, "a miss must return the zero key")
			expect.Equal(t, v, 0, "a miss must return the zero value")
		})
	})

	t.Run("Max", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the largest key and its value", func(t *testing.T) {
			t.Parallel()
			for _, n := range []int{1, small, big} {
				m, keys := filled(n)
				last := keys[n-1]
				k, v, ok := m.Max()
				assert.True(t, ok, "Max must find a key")
				expect.Equal(t, k, last, "Max must return the largest key")
				expect.Equal(t, v, -last, "Max must return the value of the largest key")
			}
		})

		t.Run("reports false for an empty map", func(t *testing.T) {
			t.Parallel()
			k, v, ok := new(btree.Map[int, int]).Max()
			expect.False(t, ok, "an empty map has no largest key")
			expect.Equal(t, k, 0, "a miss must return the zero key")
			expect.Equal(t, v, 0, "a miss must return the zero value")
		})
	})

	t.Run("Floor", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			n    int
		}{
			{name: "returns the largest key at or before every probe of a map of one leaf", n: 10},
			{name: "returns the largest key at or before every probe of a map of internal nodes", n: small},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				m, keys := filled(tt.n)
				prop.Equal(t, func(p int) item {
					var got item
					got.key, got.value, got.found = m.Floor(p)

					return got
				}, func(p int) item {
					i, found := slices.BinarySearch(keys, p)
					if found {
						i++
					}
					if i == 0 {
						return item{}
					}

					return item{key: keys[i-1], value: -keys[i-1], found: true}
				}, "Floor must return the nearest key at or before each probe",
					prop.Using(prop.Integer(-3, 2*tt.n+2)), prop.Example(-1), prop.Example(2*tt.n-1))
			})
		}

		t.Run("reports false for an empty map", func(t *testing.T) {
			t.Parallel()
			k, v, ok := new(btree.Map[int, int]).Floor(0)
			expect.False(t, ok, "an empty map has no key")
			expect.Equal(t, k, 0, "a miss must return the zero key")
			expect.Equal(t, v, 0, "a miss must return the zero value")
		})
	})

	t.Run("Ceil", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			n    int
		}{
			{name: "returns the smallest key at or after every probe of a map of one leaf", n: 10},
			{name: "returns the smallest key at or after every probe of a map of internal nodes", n: small},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				m, keys := filled(tt.n)
				prop.Equal(t, func(p int) item {
					var got item
					got.key, got.value, got.found = m.Ceil(p)

					return got
				}, func(p int) item {
					i, _ := slices.BinarySearch(keys, p)
					if i == len(keys) {
						return item{}
					}

					return item{key: keys[i], value: -keys[i], found: true}
				}, "Ceil must return the nearest key at or after each probe",
					prop.Using(prop.Integer(-3, 2*tt.n+2)), prop.Example(-1), prop.Example(2*tt.n-1))
			})
		}

		t.Run("reports false for an empty map", func(t *testing.T) {
			t.Parallel()
			k, v, ok := new(btree.Map[int, int]).Ceil(0)
			expect.False(t, ok, "an empty map has no key")
			expect.Equal(t, k, 0, "a miss must return the zero key")
			expect.Equal(t, v, 0, "a miss must return the zero value")
		})
	})

	t.Run("PopMin", func(t *testing.T) {
		t.Parallel()

		t.Run("removes the keys in ascending order", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(big)
			var popped []int
			for k, v, ok := m.PopMin(); ok; k, v, ok = m.PopMin() {
				assert.Equal(t, v, -k, "PopMin must return the value of its key")
				popped = append(popped, k)
			}
			assert.Equal(t, popped, keys, "PopMin must remove the keys from the smallest up")
			assert.Equal(t, m.Len(), 0, "the map must be empty")
		})

		t.Run("leaves a clone unchanged", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(big)
			c := m.Clone()
			for range big / 2 {
				m.PopMin()
			}
			assert.Equal(t, items(t, c.All()), keys, "the clone must keep its keys")
		})
	})

	t.Run("PopMax", func(t *testing.T) {
		t.Parallel()

		t.Run("removes the keys in descending order", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(big)
			var popped []int
			for k, v, ok := m.PopMax(); ok; k, v, ok = m.PopMax() {
				assert.Equal(t, v, -k, "PopMax must return the value of its key")
				popped = append(popped, k)
			}
			slices.Reverse(keys)
			assert.Equal(t, popped, keys, "PopMax must remove the keys from the largest down")
			assert.Equal(t, m.Len(), 0, "the map must be empty")
		})

		t.Run("leaves a clone unchanged", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(big)
			c := m.Clone()
			for range big / 2 {
				m.PopMax()
			}
			assert.Equal(t, items(t, c.All()), keys, "the clone must keep its keys")
		})
	})

	t.Run("At", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			n    int
		}{
			{name: "returns the key at the index of a map of one key", n: 1},
			{name: "returns the key at every index of a map of one level of internal nodes", n: small},
			{name: "returns the key at every index of a map of two levels of internal nodes", n: big},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				m, keys := filled(tt.n)
				got, want := make([]item, tt.n), make([]item, tt.n)
				for i := range tt.n {
					got[i].key, got[i].value, got[i].found = m.At(i)
					want[i] = item{key: keys[i], value: -keys[i], found: true}
				}
				assert.Equal(t, got, want, "At must return the key at each index and its value")
			})
		}

		t.Run("reports an index outside [0, Len)", func(t *testing.T) {
			t.Parallel()
			m, _ := filled(small)
			assert.Total(t, func(i int) error {
				if k, v, ok := m.At(i); ok || k != 0 || v != 0 {
					return fmt.Errorf("index %d: At returned %d, %d, %t", i, k, v, ok)
				}

				return nil
			}, []int{-1, small, small + 1}, "At must report every index outside [0, Len)")
		})

		t.Run("reports false for an empty map", func(t *testing.T) {
			t.Parallel()
			k, v, ok := new(btree.Map[int, int]).At(0)
			expect.False(t, ok, "an empty map has no index")
			expect.Equal(t, k, 0, "a miss must return the zero key")
			expect.Equal(t, v, 0, "a miss must return the zero value")
		})
	})

	t.Run("Rank", func(t *testing.T) {
		t.Parallel()

		t.Run("counts the keys before every probe", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(big)
			prop.Equal(t, m.Rank, func(p int) int {
				i, _ := slices.BinarySearch(keys, p)

				return i
			}, "Rank must count the keys before each probe",
				prop.Using(prop.Integer(-3, 2*big+2)), prop.Example(-1), prop.Example(2*big-1))
		})

		t.Run("returns 0 for an empty map", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, new(btree.Map[int, int]).Rank(5), 0, "an empty map has no key")
		})
	})

	t.Run("All", func(t *testing.T) {
		t.Parallel()

		t.Run("yields every item in ascending order", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(big)
			assert.Equal(t, items(t, m.All()), keys, "All must yield every item in order")
			assert.Empty(t, items(t, new(btree.Map[int, int]).All()), "an empty map has no item")
		})

		t.Run("stops when the loop breaks", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(big)
			assert.Equal(t, firstPairs(m.All(), 100), keys[:100], "All must stop at the break")
		})

		t.Run("continues past the last key it yielded when the loop writes to the map", func(t *testing.T) {
			t.Parallel()
			m, _ := filled(big)
			requireWrites(t, m, m.All(), 1)
		})

		t.Run("ends when the loop empties the map", func(t *testing.T) {
			t.Parallel()
			m, _ := filled(big)
			requireEnds(t, m, m.All())
		})
	})

	t.Run("Keys", func(t *testing.T) {
		t.Parallel()

		t.Run("yields every key in ascending order", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(big)
			assert.Equal(t, slices.Collect(m.Keys()), keys, "Keys must yield every key in order")
		})

		t.Run("stops when the loop breaks", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(big)
			assert.Equal(t, firstOf(m.Keys(), 10), keys[:10], "Keys must stop at the break")
		})
	})

	t.Run("Values", func(t *testing.T) {
		t.Parallel()

		t.Run("yields every value in the order of its key", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(big)
			want := make([]int, len(keys))
			for i, k := range keys {
				want[i] = -k
			}
			assert.Equal(t, slices.Collect(m.Values()), want, "Values must yield the value of each key in order")
		})

		t.Run("stops when the loop breaks", func(t *testing.T) {
			t.Parallel()
			m, _ := filled(big)
			assert.Length(t, firstOf(m.Values(), 10), 10, "Values must stop at the break")
		})
	})

	t.Run("Backward", func(t *testing.T) {
		t.Parallel()

		t.Run("yields every item in descending order", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(big)
			slices.Reverse(keys)
			assert.Equal(t, items(t, m.Backward()), keys, "Backward must yield every item in reverse order")
			assert.Empty(t, items(t, new(btree.Map[int, int]).Backward()), "an empty map has no item")
		})

		t.Run("stops when the loop breaks", func(t *testing.T) {
			t.Parallel()
			m, _ := filled(big)
			assert.Length(t, firstPairs(m.Backward(), 100), 100, "Backward must stop at the break")
		})

		t.Run("continues past the last key it yielded when the loop writes to the map", func(t *testing.T) {
			t.Parallel()
			m, _ := filled(big)
			requireWrites(t, m, m.Backward(), -1)
		})

		t.Run("ends when the loop empties the map", func(t *testing.T) {
			t.Parallel()
			m, _ := filled(big)
			requireEnds(t, m, m.Backward())
		})
	})

	t.Run("Range", func(t *testing.T) {
		t.Parallel()

		t.Run("yields the items in [lo, hi) in ascending order", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(big)
			prop.ForAll(t, "Range must yield exactly the keys in [lo, hi)", func(c *prop.Case) {
				lo := c.Draw(prop.Integer(-5, 2*big+5), "lo")
				hi := lo + c.Draw(prop.Integer(-50, 350), "width")
				i, _ := slices.BinarySearch(keys, lo)
				j, _ := slices.BinarySearch(keys, hi)
				assert.Equal(c, items(c, m.Range(lo, hi)), keys[i:max(i, j)], "Range must yield the keys in the range",
					assert.EquateEmpty())
			})
			assert.Empty(t, items(t, new(btree.Map[int, int]).Range(0, 10)), "an empty map has no item")
		})

		t.Run("yields nothing between two adjacent keys", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(big)
			assert.Total(t, func(k int) error {
				if got := items(t, m.Range(k+1, k+2)); len(got) != 0 {
					return fmt.Errorf("key %d: the Range of the odd key after it yielded %v", k, got)
				}

				return nil
			}, keys, "no key may lie between two adjacent keys")
		})

		t.Run("stops when the loop breaks", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(big)
			assert.Equal(t, firstPairs(m.Range(keys[10], keys[500]), 50), keys[10:60], "Range must stop at the break")
		})

		t.Run("continues past the last key it yielded when the loop writes to the map", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(big)
			requireWrites(t, m, m.Range(keys[0], keys[big-1]+2), 1)
		})

		t.Run("ends when the loop empties the map", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(big)
			requireEnds(t, m, m.Range(keys[0], keys[big-1]))
		})
	})

	t.Run("Ascend", func(t *testing.T) {
		t.Parallel()

		t.Run("yields the items at or after every probe in ascending order", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(small)
			for p := -3; p < 2*small+3; p++ {
				i, _ := slices.BinarySearch(keys, p)
				assert.Equal(t, items(t, m.Ascend(p)), keys[i:], "Ascend must yield the keys at or after the probe",
					assert.EquateEmpty())
			}
		})

		t.Run("stops when the loop breaks", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(big)
			assert.Equal(t, firstPairs(m.Ascend(keys[5]), 70), keys[5:75], "Ascend must stop at the break")
		})

		t.Run("continues past the last key it yielded when the loop writes to the map", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(big)
			requireWrites(t, m, m.Ascend(keys[0]), 1)
		})

		t.Run("ends when the loop empties the map", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(big)
			requireEnds(t, m, m.Ascend(keys[0]))
		})
	})

	t.Run("Descend", func(t *testing.T) {
		t.Parallel()

		t.Run("yields the items at or before every probe in descending order", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(small)
			for p := -3; p < 2*small+3; p++ {
				i, found := slices.BinarySearch(keys, p)
				if found {
					i++
				}
				want := slices.Clone(keys[:i])
				slices.Reverse(want)
				assert.Equal(t, items(t, m.Descend(p)), want, "Descend must yield the keys at or before the probe",
					assert.EquateEmpty())
			}
		})

		t.Run("stops when the loop breaks", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(big)
			assert.Length(t, firstPairs(m.Descend(keys[big-5]), 70), 70, "Descend must stop at the break")
		})

		t.Run("continues past the last key it yielded when the loop writes to the map", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(big)
			requireWrites(t, m, m.Descend(keys[big-1]), -1)
		})

		t.Run("ends when the loop empties the map", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(big)
			requireEnds(t, m, m.Descend(keys[big-1]))
		})
	})

	t.Run("Clone", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a map with the same items", func(t *testing.T) {
			t.Parallel()
			m, keys := filled(big)
			assert.Equal(t, items(t, m.Clone().All()), keys, "the clone must contain the items")
			assert.Empty(t, items(t, new(btree.Map[int, int]).Clone().All()), "the clone of an empty map is empty")
		})

		t.Run("keeps the writes to each map out of the other", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "a map and its clone must each keep only their own writes", func(c *prop.Case) {
				m, keys := filled(big)
				clone := m.Clone()
				models := [2]map[int]int{{}, {}}
				for _, k := range keys {
					models[0][k], models[1][k] = -k, -k
				}
				ops := c.Draw(prop.List(prop.Composite(func(c *prop.Case) cloneOp {
					return cloneOp{
						toClone: c.Draw(prop.Boolean(), "toClone"),
						set:     c.Draw(prop.Boolean(), "set"),
						key:     c.Draw(prop.Integer(0, 2*big-1), "key"),
					}
				}), prop.MaxSize(500)), "writes")
				for i, op := range ops {
					target, model := m, models[0]
					if op.toClone {
						target, model = clone, models[1]
					}
					if op.set {
						target.Set(op.key, -op.key)
						model[op.key] = -op.key
					} else {
						target.Delete(op.key)
						delete(model, op.key)
					}
					if i%100 == 99 {
						clone = clone.Clone()
					}
				}
				assert.Equal(c, items(c, m.All()), slices.Sorted(maps.Keys(models[0])),
					"the map must contain only its own writes", assert.EquateEmpty())
				assert.Equal(c, items(c, clone.All()), slices.Sorted(maps.Keys(models[1])),
					"the clone must contain only its own writes", assert.EquateEmpty())
			}, prop.Cases(20))
		})
	})
}

// TestMapAllocs checks the allocation contract of the methods of a Map of
// benchKeys keys. MaxAllocs counts the allocations of the whole process, so
// the test does not run in parallel.
func TestMapAllocs(t *testing.T) {
	keys := evens(benchKeys)
	var m btree.Map[int, int]
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

		var fresh btree.Map[int, int]
		expect.MaxAllocs(t, func() {
			fresh = btree.Map[int, int]{}
			for _, k := range keys {
				fresh.Set(k, k)
			}
		}, fillAllocs, "a fill in random order must allocate only the nodes of its tree")
		expect.MaxAllocs(t, func() {
			fresh = btree.Map[int, int]{}
			for k := range benchKeys {
				fresh.Set(k, k)
			}
		}, ascendingAllocs, "a fill in ascending order must allocate only the nodes of its tree")
		assert.Equal(t, fresh.Len(), benchKeys, "the test must measure a fill of every key")
	})

	t.Run("Delete", func(t *testing.T) {
		var present bool
		expect.MaxAllocs(t, func() {
			_, present = m.Delete(k)
			m.Set(k, k)
		}, 0, "Delete then Set of one key must not allocate")
		assert.True(t, present, "the test must measure a present key")
	})

	t.Run("Floor", func(t *testing.T) {
		var got int
		expect.MaxAllocs(t, func() { got, _, _ = m.Floor(k + 1) }, 0, "Floor must not allocate")
		assert.Equal(t, got, k, "the test must measure the key below the probe")
	})

	t.Run("At", func(t *testing.T) {
		var got int
		expect.MaxAllocs(t, func() { got, _, _ = m.At(benchKeys / 2) }, 0, "At must not allocate")
		assert.Equal(t, got, benchKeys, "the test must measure the middle key")
	})

	t.Run("Rank", func(t *testing.T) {
		var got int
		expect.MaxAllocs(t, func() { got = m.Rank(benchKeys) }, 0, "Rank must not allocate")
		assert.Equal(t, got, benchKeys/2, "the test must measure the middle key")
	})

	t.Run("All", func(t *testing.T) {
		var sum int
		expect.MaxAllocs(t, func() {
			for _, v := range m.All() {
				sum += v
			}
		}, 0, "a range loop over All must not allocate")
		assert.NotEqual(t, sum, 0, "the test must measure every item")
	})

	t.Run("Range", func(t *testing.T) {
		var n int
		expect.MaxAllocs(t, func() {
			for range m.Range(k, k+128) {
				n++
			}
		}, 0, "a range loop over Range must not allocate")
		assert.NotEqual(t, n, 0, "the test must measure the items of the range")
	})

	t.Run("Reset", func(t *testing.T) {
		var reused btree.Map[int, int]
		for _, k := range keys {
			reused.Set(k, k)
		}
		reused.Reset()
		expect.MaxAllocs(t, func() {
			reused.Reset()
			for _, k := range keys {
				reused.Set(k, k)
			}
		}, 0, "a refill after Reset must not allocate")
		assert.Equal(t, reused.Len(), benchKeys, "the test must measure a refill of every key")
	})

	t.Run("Clone", func(t *testing.T) {
		var c *btree.Map[int, int]
		expect.MaxAllocs(t, func() {
			c = m.Clone()
			c.Set(k, 0)
		}, cloneSetAllocs, "a Clone and a Set must allocate the new map and the copied path")
		assert.Equal(t, c.Len(), benchKeys, "the test must measure a clone of every key")
	})
}

// BenchmarkMap reports the cost of the operations of a Map of 65,536 int
// keys set in random order, and fails when an operation allocates more than
// the allocation contract of the package allows.
func BenchmarkMap(b *testing.B) {
	keys := evens(benchKeys)
	var m btree.Map[int, int]
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
				_, present = m.Set(keys[i], keys[i])
				i = (i + 1) % benchKeys
			}

			assert.True(b, present, "the benchmark must measure present keys")
		})

		b.Run("of 65,536 keys in random order", func(b *testing.B) {
			var fresh btree.Map[int, int]

			c := bench.Start(b).MaxAllocs(fillAllocs)
			defer c.End()

			for c.Loop() {
				fresh = btree.Map[int, int]{}
				for _, k := range keys {
					fresh.Set(k, k)
				}
			}

			assert.Equal(b, fresh.Len(), benchKeys, "the benchmark must measure a fill of every key")
		})

		b.Run("of 65,536 keys in ascending order", func(b *testing.B) {
			var fresh btree.Map[int, int]

			c := bench.Start(b).MaxAllocs(ascendingAllocs)
			defer c.End()

			for c.Loop() {
				fresh = btree.Map[int, int]{}
				for k := range benchKeys {
					fresh.Set(k, k)
				}
			}

			assert.Equal(b, fresh.Len(), benchKeys, "the benchmark must measure a fill of every key")
		})
	})

	b.Run("Delete", func(b *testing.B) {
		i := 0

		var present bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			_, present = m.Delete(keys[i])
			m.Set(keys[i], keys[i])
			i = (i + 1) % benchKeys
		}

		assert.True(b, present, "the benchmark must measure present keys")
	})

	b.Run("Reset", func(b *testing.B) {
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

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			reused.Reset()
			for _, k := range keys {
				reused.Set(k, k)
			}
		}

		assert.Equal(b, reused.Len(), benchKeys, "the benchmark must measure a refill of every key")
	})

	b.Run("All", func(b *testing.B) {
		sum := 0

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			for _, v := range m.All() {
				sum += v
			}
		}

		assert.NotEqual(b, sum, 0, "the benchmark must measure every item")
	})

	b.Run("Range", func(b *testing.B) {
		i, sum := 0, 0

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			for _, v := range m.Range(keys[i], keys[i]+128) {
				sum += v
			}
			i = (i + 1) % benchKeys
		}

		assert.NotEqual(b, sum, 0, "the benchmark must measure the items of the ranges")
	})

	b.Run("Floor", func(b *testing.B) {
		i, sum := 0, 0

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			k, _, _ := m.Floor(keys[i] + 1)
			sum += k
			i = (i + 1) % benchKeys
		}

		assert.NotEqual(b, sum, 0, "the benchmark must measure present keys")
	})

	b.Run("Rank", func(b *testing.B) {
		i, sum := 0, 0

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			sum += m.Rank(keys[i])
			i = (i + 1) % benchKeys
		}

		assert.NotEqual(b, sum, 0, "the benchmark must measure ranks")
	})

	b.Run("At", func(b *testing.B) {
		i, sum := 0, 0

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			k, _, _ := m.At(i)
			sum += k
			i = (i + 1) % benchKeys
		}

		assert.NotEqual(b, sum, 0, "the benchmark must measure present keys")
	})

	b.Run("Clone", func(b *testing.B) {
		i := 0

		var clone *btree.Map[int, int]

		c := bench.Start(b).MaxAllocs(cloneSetAllocs)
		defer c.End()

		for c.Loop() {
			clone = m.Clone()
			clone.Set(keys[i], 0)
			i = (i + 1) % benchKeys
		}

		assert.Equal(b, clone.Len(), benchKeys, "the benchmark must measure a clone of every key")
	})
}

// evens returns the keys 0, 2, ..., 2(n-1) in an order that a permutation
// seeded with n fixes, so no odd key is present and every run sees the
// same order.
func evens(n int) []int {
	keys := rand.New(rand.NewPCG(uint64(n), 0)).Perm(n)
	for i := range keys {
		keys[i] *= 2
	}

	return keys
}

// filled returns a map of the keys of evens(n), set in that order with the
// negation of each key as its value, and its keys in ascending order.
func filled(n int) (*btree.Map[int, int], []int) {
	var m btree.Map[int, int]
	keys := evens(n)
	for _, k := range keys {
		m.Set(k, -k)
	}
	slices.Sort(keys)

	return &m, keys
}

// items returns the keys that seq yields, and fails the test when a value is
// not the negation of its key.
func items(tb assert.TB, seq iter.Seq2[int, int]) []int {
	tb.Helper()
	var keys, negated []int
	for k, v := range seq {
		keys, negated = append(keys, k), append(negated, -v)
	}
	assert.Equal(tb, negated, keys, "the iterator must yield the value of each key")

	return keys
}

// firstPairs returns the first n keys that seq yields to a loop that breaks
// after n keys.
func firstPairs(seq iter.Seq2[int, int], n int) []int {
	var keys []int
	for k := range seq {
		if keys = append(keys, k); len(keys) == n {
			break
		}
	}

	return keys
}

// requireWrites ranges over seq, an iterator over m in the direction ahead:
// 1 for ascending key order and -1 for descending. At every key that is a
// multiple of 6, the loop sets the key 1 ahead, deletes the key 4 ahead,
// sets the key 3 behind, and clones m. The test fails unless the iteration
// yields its keys in order, yields every key set ahead, and yields no key
// deleted ahead or set behind.
//
// An iteration in order yields each key of m and each key set ahead once,
// which is at most twice the keys of m. The loop stops after one key more,
// so an iteration that yields keys again fails in bounded memory.
func requireWrites(tb testing.TB, m *btree.Map[int, int], seq iter.Seq2[int, int], ahead int) {
	tb.Helper()
	bound := 2 * m.Len()
	var yielded []int
	seen, setAhead, deleted := map[int]bool{}, map[int]bool{}, map[int]bool{}
	for k := range seq {
		if yielded = append(yielded, k); len(yielded) > bound {
			break
		}
		seen[k] = true
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
	assert.Pairwise(tb, yielded, func(earlier, later int) bool { return (later-earlier)*ahead > 0 },
		"each key must be past the key before it")
	assert.InRange(tb, len(yielded), 0, float64(bound), "the iteration must yield each key once")
	var missing, unexpected []int
	for k := range setAhead {
		if !seen[k] {
			missing = append(missing, k)
		}
	}
	for _, k := range yielded {
		if deleted[k] || (k%2 != 0 && !setAhead[k]) {
			unexpected = append(unexpected, k)
		}
	}
	assert.Empty(tb, missing, "every key set ahead must be yielded")
	assert.Empty(tb, unexpected, "no key deleted ahead or set behind may be yielded")
}

// requireEnds fails the test unless a loop over seq that clears m ends after
// one key. The loop stops at the second key, so an iteration that goes on
// fails at once.
func requireEnds(tb testing.TB, m *btree.Map[int, int], seq iter.Seq2[int, int]) {
	tb.Helper()
	n := 0
	for range seq {
		if n++; n > 1 {
			break
		}
		m.Clear()
	}
	assert.Equal(tb, n, 1, "the iteration must end once the map is empty")
}
