// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package btree_test

import (
	"iter"
	"slices"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/btree"
)

func TestSet(t *testing.T) {
	t.Parallel()

	// top is the largest key of a set from filledSet(small).
	const top = 2 * (small - 1)

	t.Run("Add", func(t *testing.T) {
		t.Parallel()

		t.Run("reports whether the key was absent", func(t *testing.T) {
			t.Parallel()
			var s btree.Set[int]
			assert.True(t, s.Add(3), "the first Add must report a new key")
			assert.False(t, s.Add(3), "the second Add must report a present key")
			assert.Equal(t, s.Len(), 1, "the set must contain the key once")
		})

		t.Run("keeps every key in order", func(t *testing.T) {
			t.Parallel()
			s, keys := filledSet(big)
			assert.Equal(t, slices.Collect(s.All()), keys, "the set must contain every key in order")
		})
	})

	t.Run("Has", func(t *testing.T) {
		t.Parallel()

		t.Run("reports whether each key is present", func(t *testing.T) {
			t.Parallel()
			s, keys := filledSet(small)
			var got, want []bool
			for p := -3; p < 2*small+3; p++ {
				_, found := slices.BinarySearch(keys, p)
				got, want = append(got, s.Has(p)), append(want, found)
			}
			assert.Equal(t, got, want, "Has must report whether each key is present")
		})
	})

	t.Run("Delete", func(t *testing.T) {
		t.Parallel()

		t.Run("reports whether the key was present", func(t *testing.T) {
			t.Parallel()
			s, _ := filledSet(small)
			assert.True(t, s.Delete(4), "Delete must report a present key")
			assert.False(t, s.Delete(4), "Delete must report an absent key")
		})

		t.Run("removes the key", func(t *testing.T) {
			t.Parallel()
			s, keys := filledSet(small)
			s.Delete(4)
			assert.Equal(t, slices.Collect(s.All()), slices.DeleteFunc(keys, func(k int) bool { return k == 4 }),
				"Delete must remove the key")
		})
	})

	t.Run("DeleteRange", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the number of keys in [lo, hi)", func(t *testing.T) {
			t.Parallel()
			s, _ := filledSet(big)
			assert.Equal(t, s.DeleteRange(100, 1_001), 451, "DeleteRange must count 100 up to 1,000")
		})

		t.Run("removes exactly the keys in [lo, hi)", func(t *testing.T) {
			t.Parallel()
			s, keys := filledSet(big)
			s.DeleteRange(100, 1_001)
			want := slices.DeleteFunc(keys, func(k int) bool { return k >= 100 && k < 1_001 })
			assert.Equal(t, slices.Collect(s.All()), want, "DeleteRange must remove exactly the range")
		})
	})

	t.Run("Len", func(t *testing.T) {
		t.Parallel()

		t.Run("counts the keys", func(t *testing.T) {
			t.Parallel()
			s, _ := filledSet(small)
			assert.Equal(t, s.Len(), small, "Len must count the keys")
		})
	})

	t.Run("Clear", func(t *testing.T) {
		t.Parallel()

		t.Run("removes every key", func(t *testing.T) {
			t.Parallel()
			s, _ := filledSet(small)
			s.Clear()
			assert.Equal(t, s.Len(), 0, "Clear must remove every key")
			assert.False(t, s.Has(0), "Clear must remove the smallest key")
		})
	})

	t.Run("Reset", func(t *testing.T) {
		t.Parallel()

		t.Run("removes every key", func(t *testing.T) {
			t.Parallel()
			s, _ := filledSet(small)
			s.Reset()
			assert.Equal(t, s.Len(), 0, "Reset must remove every key")
			assert.False(t, s.Has(0), "Reset must remove the smallest key")
		})

		t.Run("leaves a set that refills with every key in order", func(t *testing.T) {
			t.Parallel()
			s, keys := filledSet(small)
			s.Reset()
			for _, k := range evens(small) {
				s.Add(k)
			}
			assert.Equal(t, slices.Collect(s.All()), keys, "the refilled set must contain every key in order")
		})
	})

	t.Run("Min", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the smallest key", func(t *testing.T) {
			t.Parallel()
			s, _ := filledSet(small)
			k, ok := s.Min()
			assert.True(t, ok, "Min must find a key")
			assert.Equal(t, k, 0, "Min must return the smallest key")
		})

		t.Run("reports false for an empty set", func(t *testing.T) {
			t.Parallel()
			k, ok := new(btree.Set[int]).Min()
			expect.False(t, ok, "an empty set has no smallest key")
			expect.Equal(t, k, 0, "a miss must return the zero key")
		})
	})

	t.Run("Max", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the largest key", func(t *testing.T) {
			t.Parallel()
			s, _ := filledSet(small)
			k, ok := s.Max()
			assert.True(t, ok, "Max must find a key")
			assert.Equal(t, k, top, "Max must return the largest key")
		})

		t.Run("reports false for an empty set", func(t *testing.T) {
			t.Parallel()
			k, ok := new(btree.Set[int]).Max()
			expect.False(t, ok, "an empty set has no largest key")
			expect.Equal(t, k, 0, "a miss must return the zero key")
		})
	})

	t.Run("Floor", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the largest key at or before the probe", func(t *testing.T) {
			t.Parallel()
			s, _ := filledSet(small)
			k, ok := s.Floor(7)
			assert.True(t, ok, "Floor must find a key")
			assert.Equal(t, k, 6, "Floor must return the nearest key below")
		})

		t.Run("reports false when no key sorts at or before the probe", func(t *testing.T) {
			t.Parallel()
			s, _ := filledSet(small)
			k, ok := s.Floor(-1)
			expect.False(t, ok, "no key sorts at or before -1")
			expect.Equal(t, k, 0, "a miss must return the zero key")
		})
	})

	t.Run("Ceil", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the smallest key at or after the probe", func(t *testing.T) {
			t.Parallel()
			s, _ := filledSet(small)
			k, ok := s.Ceil(7)
			assert.True(t, ok, "Ceil must find a key")
			assert.Equal(t, k, 8, "Ceil must return the nearest key above")
		})

		t.Run("reports false when no key sorts at or after the probe", func(t *testing.T) {
			t.Parallel()
			s, _ := filledSet(small)
			k, ok := s.Ceil(top + 1)
			expect.False(t, ok, "no key sorts at or after the largest plus one")
			expect.Equal(t, k, 0, "a miss must return the zero key")
		})
	})

	t.Run("PopMin", func(t *testing.T) {
		t.Parallel()

		t.Run("removes the smallest key", func(t *testing.T) {
			t.Parallel()
			s, _ := filledSet(small)
			k, ok := s.PopMin()
			assert.True(t, ok, "PopMin must find a key")
			expect.Equal(t, k, 0, "PopMin must return the smallest key")
			expect.False(t, s.Has(0), "PopMin must remove the smallest key")
		})

		t.Run("reports false for an empty set", func(t *testing.T) {
			t.Parallel()
			k, ok := new(btree.Set[int]).PopMin()
			expect.False(t, ok, "an empty set has no key to remove")
			expect.Equal(t, k, 0, "a miss must return the zero key")
		})
	})

	t.Run("PopMax", func(t *testing.T) {
		t.Parallel()

		t.Run("removes the largest key", func(t *testing.T) {
			t.Parallel()
			s, _ := filledSet(small)
			k, ok := s.PopMax()
			assert.True(t, ok, "PopMax must find a key")
			expect.Equal(t, k, top, "PopMax must return the largest key")
			expect.False(t, s.Has(top), "PopMax must remove the largest key")
		})

		t.Run("reports false for an empty set", func(t *testing.T) {
			t.Parallel()
			k, ok := new(btree.Set[int]).PopMax()
			expect.False(t, ok, "an empty set has no key to remove")
			expect.Equal(t, k, 0, "a miss must return the zero key")
		})
	})

	t.Run("At", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the key at an index", func(t *testing.T) {
			t.Parallel()
			s, keys := filledSet(small)
			k, ok := s.At(17)
			assert.True(t, ok, "At must find a key at an index below Len")
			assert.Equal(t, k, keys[17], "At must return the key at the index")
		})

		t.Run("reports false for an index past the last key", func(t *testing.T) {
			t.Parallel()
			s, _ := filledSet(small)
			k, ok := s.At(small)
			expect.False(t, ok, "At must report an index past the last key")
			expect.Equal(t, k, 0, "a miss must return the zero key")
		})
	})

	t.Run("Rank", func(t *testing.T) {
		t.Parallel()

		t.Run("counts the keys before the probe", func(t *testing.T) {
			t.Parallel()
			s, _ := filledSet(small)
			assert.Equal(t, s.Rank(7), 4, "Rank must count 0, 2, 4 and 6")
		})
	})

	t.Run("All", func(t *testing.T) {
		t.Parallel()

		t.Run("yields every key in ascending order", func(t *testing.T) {
			t.Parallel()
			s, keys := filledSet(big)
			assert.Equal(t, slices.Collect(s.All()), keys, "All must yield every key in order")
		})

		t.Run("stops when the loop breaks", func(t *testing.T) {
			t.Parallel()
			s, keys := filledSet(big)
			assert.Equal(t, firstOf(s.All(), 9), keys[:9], "All must stop at the break")
		})
	})

	t.Run("Backward", func(t *testing.T) {
		t.Parallel()

		t.Run("yields every key in descending order", func(t *testing.T) {
			t.Parallel()
			s, keys := filledSet(big)
			slices.Reverse(keys)
			assert.Equal(t, slices.Collect(s.Backward()), keys, "Backward must yield every key in reverse order")
		})

		t.Run("stops when the loop breaks", func(t *testing.T) {
			t.Parallel()
			s, keys := filledSet(big)
			slices.Reverse(keys)
			assert.Equal(t, firstOf(s.Backward(), 7), keys[:7], "Backward must stop at the break")
		})
	})

	t.Run("Range", func(t *testing.T) {
		t.Parallel()

		t.Run("yields the keys in [lo, hi)", func(t *testing.T) {
			t.Parallel()
			s, keys := filledSet(big)
			assert.Equal(t, slices.Collect(s.Range(101, 1_001)), keys[51:501], "Range must yield 102 up to 1,000")
		})

		t.Run("stops when the loop breaks", func(t *testing.T) {
			t.Parallel()
			s, keys := filledSet(big)
			assert.Equal(t, firstOf(s.Range(0, 1_000), 5), keys[:5], "Range must stop at the break")
		})
	})

	t.Run("Ascend", func(t *testing.T) {
		t.Parallel()

		t.Run("yields the keys at or after the probe", func(t *testing.T) {
			t.Parallel()
			s, keys := filledSet(big)
			assert.Equal(t, slices.Collect(s.Ascend(1_001)), keys[501:], "Ascend must yield the keys from 1,002 up")
		})

		t.Run("stops when the loop breaks", func(t *testing.T) {
			t.Parallel()
			s, keys := filledSet(big)
			assert.Equal(t, firstOf(s.Ascend(0), 9), keys[:9], "Ascend must stop at the break")
		})
	})

	t.Run("Descend", func(t *testing.T) {
		t.Parallel()

		t.Run("yields the keys at or before the probe", func(t *testing.T) {
			t.Parallel()
			s, keys := filledSet(big)
			below := slices.Clone(keys[:501])
			slices.Reverse(below)
			assert.Equal(t, slices.Collect(s.Descend(1_001)), below, "Descend must yield the keys from 1,000 down")
		})

		t.Run("stops when the loop breaks", func(t *testing.T) {
			t.Parallel()
			s, keys := filledSet(big)
			slices.Reverse(keys)
			assert.Equal(t, firstOf(s.Descend(2*big), 9), keys[:9], "Descend must stop at the break")
		})
	})

	t.Run("Clone", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a set with the same keys that keeps its own writes", func(t *testing.T) {
			t.Parallel()
			s, keys := filledSet(big)
			c := s.Clone()
			c.Add(1)
			s.Delete(0)
			assert.Equal(t, slices.Collect(c.All()), slices.Insert(slices.Clone(keys), 1, 1),
				"the clone must contain its keys")
			assert.Equal(t, slices.Collect(s.All()), keys[1:], "the set must contain its keys")
		})
	})
}

// TestSetAllocs checks the allocation contract of Add, Has and a range loop
// over All. MaxAllocs counts the allocations of the whole process, so the
// test does not run in parallel.
//
//nolint:paralleltest // see above
func TestSetAllocs(t *testing.T) {
	s, keys := filledSet(benchKeys)
	k := keys[benchKeys/2]

	t.Run("Add", func(t *testing.T) {
		var added bool
		expect.MaxAllocs(t, func() { added = s.Add(k) }, 0, "Add of a present key must not allocate")
		assert.False(t, added, "the test must measure a present key")

		order := evens(benchKeys)
		var fresh btree.Set[int]
		expect.MaxAllocs(t, func() {
			fresh = btree.Set[int]{}
			for _, k := range order {
				fresh.Add(k)
			}
		}, fillAllocs, "a fill in random order must allocate only the nodes of its tree")
		assert.Equal(t, fresh.Len(), benchKeys, "the test must measure a fill of every key")
	})

	t.Run("Has", func(t *testing.T) {
		var got bool
		expect.MaxAllocs(t, func() { got = s.Has(k) }, 0, "Has must not allocate")
		assert.True(t, got, "the test must measure a present key")
	})

	t.Run("All", func(t *testing.T) {
		var sum int
		expect.MaxAllocs(t, func() {
			for k := range s.All() {
				sum += k
			}
		}, 0, "a range loop over All must not allocate")
		assert.NotEqual(t, sum, 0, "the test must measure every key")
	})
}

// BenchmarkSet reports the cost of Add, Has and All on a Set of 65,536 int
// keys, and fails when one of them allocates more than the allocation
// contract of the package allows.
func BenchmarkSet(b *testing.B) {
	keys := evens(benchKeys)
	var s btree.Set[int]
	for _, k := range keys {
		s.Add(k)
	}

	b.Run("Has", func(b *testing.B) {
		i, found := 0, 0

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			if s.Has(keys[i]) {
				found++
			}
			i = (i + 1) % benchKeys
		}

		assert.NotEqual(b, found, 0, "the benchmark must measure present keys")
	})

	b.Run("Add", func(b *testing.B) {
		b.Run("of 65,536 keys in random order", func(b *testing.B) {
			var fresh btree.Set[int]

			c := bench.Start(b).MaxAllocs(fillAllocs)
			defer c.End()

			for c.Loop() {
				fresh = btree.Set[int]{}
				for _, k := range keys {
					fresh.Add(k)
				}
			}

			assert.Equal(b, fresh.Len(), benchKeys, "the benchmark must measure a fill of every key")
		})
	})

	b.Run("All", func(b *testing.B) {
		sum := 0

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			for k := range s.All() {
				sum += k
			}
		}

		assert.NotEqual(b, sum, 0, "the benchmark must measure every key")
	})
}

// filledSet returns a set of the keys of evens(n), added in that order, and
// its keys in ascending order.
func filledSet(n int) (*btree.Set[int], []int) {
	var s btree.Set[int]
	keys := evens(n)
	for _, k := range keys {
		s.Add(k)
	}
	slices.Sort(keys)

	return &s, keys
}

// firstOf returns the first n keys that seq yields to a loop that breaks
// after n keys.
func firstOf[K any](seq iter.Seq[K], n int) []K {
	var keys []K
	for k := range seq {
		if keys = append(keys, k); len(keys) == n {
			break
		}
	}

	return keys
}
