// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package btree_test

import (
	"iter"
	"slices"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/btree"
)

// filledSet returns a set of the keys 0, 2, ..., 2(n-1), added in a random
// order, and its keys in ascending order.
func filledSet(tb testing.TB, n int) (*btree.Set[int], []int) {
	tb.Helper()
	var s btree.Set[int]
	keys := evens(tb, n)
	for _, k := range keys {
		s.Add(k)
	}
	slices.Sort(keys)

	return &s, keys
}

// firstOf returns the first n keys that seq yields to a loop that breaks
// after n keys.
func firstOf(seq iter.Seq[int], n int) []int {
	var keys []int
	for k := range seq {
		if keys = append(keys, k); len(keys) == n {
			break
		}
	}

	return keys
}

func TestSet(t *testing.T) {
	t.Parallel()

	// top is the largest key of a set from filledSet(t, small).
	const top = 2 * (small - 1)

	t.Run("Add", func(t *testing.T) {
		t.Parallel()

		t.Run("reports whether the key was absent", func(t *testing.T) {
			t.Parallel()
			var s btree.Set[int]
			testkit.True(t, s.Add(3), "the first Add must report a new key")
			testkit.False(t, s.Add(3), "the second Add must report a present key")
			testkit.Equal(t, s.Len(), 1, "the set must hold the key once")
		})

		t.Run("keeps every key in order", func(t *testing.T) {
			t.Parallel()
			s, keys := filledSet(t, big)
			testkit.Equal(t, slices.Collect(s.All()), keys, "the set must hold every key in order")
		})
	})

	t.Run("Has", func(t *testing.T) {
		t.Parallel()

		t.Run("reports whether each key is present", func(t *testing.T) {
			t.Parallel()
			s, keys := filledSet(t, small)
			for p := -3; p < 2*small+3; p++ {
				_, want := slices.BinarySearch(keys, p)
				testkit.Equal(t, s.Has(p), want, "Has must report whether the key is present")
			}
		})
	})

	t.Run("Delete", func(t *testing.T) {
		t.Parallel()

		t.Run("reports whether the key was present and removes it", func(t *testing.T) {
			t.Parallel()
			s, keys := filledSet(t, small)
			testkit.True(t, s.Delete(4), "Delete must report a present key")
			testkit.False(t, s.Delete(4), "Delete must report an absent key")
			testkit.Equal(t, slices.Collect(s.All()), slices.DeleteFunc(keys, func(k int) bool { return k == 4 }),
				"Delete must remove the key")
		})
	})

	t.Run("DeleteRange", func(t *testing.T) {
		t.Parallel()

		t.Run("removes the keys in [lo, hi) and returns how many", func(t *testing.T) {
			t.Parallel()
			s, keys := filledSet(t, big)
			testkit.Equal(t, s.DeleteRange(100, 1_001), 451, "DeleteRange must remove 100 up to 1,000")
			testkit.Equal(
				t,
				slices.Collect(s.All()),
				slices.DeleteFunc(keys, func(k int) bool { return k >= 100 && k < 1_001 }),
				"DeleteRange must remove exactly the range",
			)
		})
	})

	t.Run("Len", func(t *testing.T) {
		t.Parallel()

		t.Run("counts the keys", func(t *testing.T) {
			t.Parallel()
			s, _ := filledSet(t, small)
			testkit.Equal(t, s.Len(), small, "Len must count the keys")
		})
	})

	t.Run("Clear", func(t *testing.T) {
		t.Parallel()

		t.Run("removes every key", func(t *testing.T) {
			t.Parallel()
			s, _ := filledSet(t, small)
			s.Clear()
			testkit.True(t, s.Len() == 0 && !s.Has(0), "Clear must remove every key")
		})
	})

	t.Run("Reset", func(t *testing.T) {
		t.Parallel()

		t.Run("removes every key and leaves a set that refills with every key in order", func(t *testing.T) {
			t.Parallel()
			s, keys := filledSet(t, small)
			s.Reset()
			testkit.True(t, s.Len() == 0 && !s.Has(0), "Reset must remove every key")
			for _, k := range evens(t, small) {
				s.Add(k)
			}
			testkit.Equal(t, slices.Collect(s.All()), keys, "the refilled set must hold every key in order")
		})
	})

	t.Run("Min", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the smallest key, or false for an empty set", func(t *testing.T) {
			t.Parallel()
			s, _ := filledSet(t, small)
			k, ok := s.Min()
			testkit.True(t, ok && k == 0, "Min must return the smallest key")
			k, ok = new(btree.Set[int]).Min()
			testkit.True(t, !ok && k == 0, "an empty set has no smallest key")
		})
	})

	t.Run("Max", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the largest key, or false for an empty set", func(t *testing.T) {
			t.Parallel()
			s, _ := filledSet(t, small)
			k, ok := s.Max()
			testkit.True(t, ok && k == top, "Max must return the largest key")
			k, ok = new(btree.Set[int]).Max()
			testkit.True(t, !ok && k == 0, "an empty set has no largest key")
		})
	})

	t.Run("Floor", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the largest key at or before the probe, or false for none", func(t *testing.T) {
			t.Parallel()
			s, _ := filledSet(t, small)
			k, ok := s.Floor(7)
			testkit.True(t, ok && k == 6, "Floor must return the nearest key below")
			k, ok = s.Floor(-1)
			testkit.True(t, !ok && k == 0, "no key sorts at or before -1")
		})
	})

	t.Run("Ceil", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the smallest key at or after the probe, or false for none", func(t *testing.T) {
			t.Parallel()
			s, _ := filledSet(t, small)
			k, ok := s.Ceil(7)
			testkit.True(t, ok && k == 8, "Ceil must return the nearest key above")
			k, ok = s.Ceil(top + 1)
			testkit.True(t, !ok && k == 0, "no key sorts at or after the largest plus one")
		})
	})

	t.Run("PopMin", func(t *testing.T) {
		t.Parallel()

		t.Run("removes and returns the smallest key, or false for an empty set", func(t *testing.T) {
			t.Parallel()
			s, _ := filledSet(t, small)
			k, ok := s.PopMin()
			testkit.True(t, ok && k == 0 && !s.Has(0), "PopMin must remove the smallest key")
			k, ok = new(btree.Set[int]).PopMin()
			testkit.True(t, !ok && k == 0, "an empty set has no key to remove")
		})
	})

	t.Run("PopMax", func(t *testing.T) {
		t.Parallel()

		t.Run("removes and returns the largest key, or false for an empty set", func(t *testing.T) {
			t.Parallel()
			s, _ := filledSet(t, small)
			k, ok := s.PopMax()
			testkit.True(t, ok && k == top && !s.Has(top), "PopMax must remove the largest key")
			k, ok = new(btree.Set[int]).PopMax()
			testkit.True(t, !ok && k == 0, "an empty set has no key to remove")
		})
	})

	t.Run("At", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the key at an index, or false outside [0, Len)", func(t *testing.T) {
			t.Parallel()
			s, keys := filledSet(t, small)
			k, ok := s.At(17)
			testkit.True(t, ok && k == keys[17], "At must return the key at the index")
			k, ok = s.At(small)
			testkit.True(t, !ok && k == 0, "At must reject an index past the last key")
		})
	})

	t.Run("Rank", func(t *testing.T) {
		t.Parallel()

		t.Run("counts the keys before the probe", func(t *testing.T) {
			t.Parallel()
			s, _ := filledSet(t, small)
			testkit.Equal(t, s.Rank(7), 4, "Rank must count 0, 2, 4 and 6")
		})
	})

	t.Run("All", func(t *testing.T) {
		t.Parallel()

		t.Run("yields every key in ascending order and stops when the loop breaks", func(t *testing.T) {
			t.Parallel()
			s, keys := filledSet(t, big)
			testkit.Equal(t, slices.Collect(s.All()), keys, "All must yield every key in order")
			testkit.Equal(t, firstOf(s.All(), 9), keys[:9], "All must stop at the break")
		})
	})

	t.Run("Backward", func(t *testing.T) {
		t.Parallel()

		t.Run("yields every key in descending order and stops when the loop breaks", func(t *testing.T) {
			t.Parallel()
			s, keys := filledSet(t, big)
			slices.Reverse(keys)
			testkit.Equal(t, slices.Collect(s.Backward()), keys, "Backward must yield every key in reverse order")
			testkit.Equal(t, firstOf(s.Backward(), 7), keys[:7], "Backward must stop at the break")
		})
	})

	t.Run("Range", func(t *testing.T) {
		t.Parallel()

		t.Run("yields the keys in [lo, hi) and stops when the loop breaks", func(t *testing.T) {
			t.Parallel()
			s, keys := filledSet(t, big)
			testkit.Equal(t, slices.Collect(s.Range(101, 1_001)), keys[51:501], "Range must yield 102 up to 1,000")
			testkit.Equal(t, firstOf(s.Range(0, 1_000), 5), keys[:5], "Range must stop at the break")
		})
	})

	t.Run("Ascend", func(t *testing.T) {
		t.Parallel()

		t.Run("yields the keys at or after the probe and stops when the loop breaks", func(t *testing.T) {
			t.Parallel()
			s, keys := filledSet(t, big)
			testkit.Equal(t, slices.Collect(s.Ascend(1_001)), keys[501:], "Ascend must yield the keys from 1,002 up")
			testkit.Equal(t, firstOf(s.Ascend(0), 9), keys[:9], "Ascend must stop at the break")
		})
	})

	t.Run("Descend", func(t *testing.T) {
		t.Parallel()

		t.Run("yields the keys at or before the probe and stops when the loop breaks", func(t *testing.T) {
			t.Parallel()
			s, keys := filledSet(t, big)
			below := slices.Clone(keys[:501])
			slices.Reverse(below)
			testkit.Equal(t, slices.Collect(s.Descend(1_001)), below, "Descend must yield the keys from 1,000 down")
			slices.Reverse(keys)
			testkit.Equal(t, firstOf(s.Descend(2*big), 9), keys[:9], "Descend must stop at the break")
		})
	})

	t.Run("Clone", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a set with the same keys that keeps its own writes", func(t *testing.T) {
			t.Parallel()
			s, keys := filledSet(t, big)
			c := s.Clone()
			c.Add(1)
			s.Delete(0)
			testkit.Equal(
				t,
				slices.Collect(c.All()),
				slices.Insert(slices.Clone(keys), 1, 1),
				"the clone must hold its keys",
			)
			testkit.Equal(t, slices.Collect(s.All()), keys[1:], "the set must hold its keys")
		})
	})
}

// BenchmarkSet reports the cost of Add, Has and All on a Set of 65,536 int
// keys. Has and All allocate nothing.
func BenchmarkSet(b *testing.B) {
	keys := evens(b, benchKeys)
	var s btree.Set[int]
	for _, k := range keys {
		s.Add(k)
	}

	b.Run("Has", func(b *testing.B) {
		b.ReportAllocs()
		i, found := 0, 0
		for b.Loop() {
			if s.Has(keys[i]) {
				found++
			}
			i = (i + 1) % benchKeys
		}
		sinkInt = found
	})

	b.Run("Add of 65,536 keys in random order", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			var fresh btree.Set[int]
			for _, k := range keys {
				fresh.Add(k)
			}
		}
	})

	b.Run("All", func(b *testing.B) {
		b.ReportAllocs()
		sum := 0
		for b.Loop() {
			for k := range s.All() {
				sum += k
			}
		}
		sinkInt = sum
	})
}
