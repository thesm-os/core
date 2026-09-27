// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package btree

import (
	"slices"
	"testing"

	"go.thesmos.sh/testkit"
)

// TestCursor is in package btree because a cursor and its moves are
// unexported.
func TestCursor(t *testing.T) {
	t.Parallel()

	// varied returns a tree with two levels of internal nodes whose leaves
	// have different sizes, so a walk that skips or repeats a leaf yields
	// other keys.
	varied := func() *ints {
		return build(nodesOf(
			leavesOf(slices.Concat([]int{minItems, minItems + 9}, slices.Repeat([]int{minItems}, 30))...),
			leavesOf(slices.Concat(slices.Repeat([]int{minItems}, 31), []int{minItems + 2})...),
			leavesOf(minItems, 2),
		))
	}
	// firstKeys returns the first n keys of a tree from build: 0, 2, 4 and
	// so on.
	firstKeys := func(n int) []int {
		keys := make([]int, n)
		for i := range keys {
			keys[i] = 2 * i
		}
		return keys
	}
	// collect calls walk with a yield that records each key until it has
	// been called stop times, and returns the keys and the result of walk.
	collect := func(stop int, walk func(yield func(int, int) bool) (int, bool)) ([]int, int, bool) {
		var keys []int
		last, changed := walk(func(k, _ int) bool {
			keys = append(keys, k)
			return len(keys) != stop
		})
		return keys, last, changed
	}

	t.Run("first", func(t *testing.T) {
		t.Parallel()

		t.Run("reports false for an empty tree", func(t *testing.T) {
			t.Parallel()
			var c cursor[int, int]
			testkit.False(t, (&ints{}).first(&c), "an empty tree has no first key")
		})

		t.Run("moves to the first key of a root leaf with an empty path", func(t *testing.T) {
			t.Parallel()
			tr := build(shape{n: 3})
			c := cursor[int, int]{depth: 5, i: 2}
			testkit.True(t, tr.first(&c), "the tree has a first key")
			testkit.True(t, c.leaf == tr.leaf && c.i == 0 && c.depth == 0, "the cursor must be at index 0 of the root")
		})

		t.Run("moves to the first key of the leftmost leaf with a path through every level", func(t *testing.T) {
			t.Parallel()
			tr := varied()
			var c cursor[int, int]
			testkit.True(t, tr.first(&c), "the tree has a first key")
			testkit.True(t, c.leaf == tr.root.inners[0].leaves[0] && c.i == 0, "the cursor must be at the first key")
			testkit.Equal(t, c.depth, 2, "the path must hold both internal levels")
		})
	})

	t.Run("last", func(t *testing.T) {
		t.Parallel()

		t.Run("reports false for an empty tree", func(t *testing.T) {
			t.Parallel()
			var c cursor[int, int]
			testkit.False(t, (&ints{}).last(&c), "an empty tree has no last key")
		})

		t.Run("moves to the last key of a root leaf with an empty path", func(t *testing.T) {
			t.Parallel()
			tr := build(shape{n: 3})
			c := cursor[int, int]{depth: 5}
			testkit.True(t, tr.last(&c), "the tree has a last key")
			testkit.True(
				t,
				c.leaf == tr.leaf && c.i == 2 && c.depth == 0,
				"the cursor must be at the last index of the root",
			)
		})

		t.Run("moves to the last key of the rightmost leaf with a path through every level", func(t *testing.T) {
			t.Parallel()
			tr := varied()
			var c cursor[int, int]
			testkit.True(t, tr.last(&c), "the tree has a last key")
			testkit.True(t, c.leaf == tr.root.inners[2].leaves[1] && c.i == 1, "the cursor must be at the last key")
			testkit.Equal(t, c.depth, 2, "the path must hold both internal levels")
		})
	})

	t.Run("nextLeaf", func(t *testing.T) {
		t.Parallel()

		t.Run("visits every leaf once in order and then reports false", func(t *testing.T) {
			t.Parallel()
			tr := varied()
			var c cursor[int, int]
			tr.first(&c)
			counts, firsts := []int{c.leaf.n}, []int{c.leaf.keys[0]}
			for c.nextLeaf() {
				testkit.Equal(t, c.i, 0, "the cursor must start each leaf at its first item")
				counts, firsts = append(counts, c.leaf.n), append(firsts, c.leaf.keys[0])
			}
			leaves, _ := sizes(tr)
			testkit.Equal(t, counts, leaves, "the cursor must visit the leaves in order")
			testkit.True(t, slices.IsSorted(firsts) && len(slices.Compact(firsts)) == len(leaves),
				"the cursor must visit each leaf once")
		})

		t.Run("reports false for a root leaf", func(t *testing.T) {
			t.Parallel()
			tr := build(shape{n: 3})
			var c cursor[int, int]
			tr.first(&c)
			testkit.False(t, c.nextLeaf(), "a root leaf has no next leaf")
		})
	})

	t.Run("prevLeaf", func(t *testing.T) {
		t.Parallel()

		t.Run("visits every leaf once in reverse order and then reports false", func(t *testing.T) {
			t.Parallel()
			tr := varied()
			var c cursor[int, int]
			tr.last(&c)
			counts, lasts := []int{c.leaf.n}, []int{c.leaf.keys[c.i]}
			for c.prevLeaf() {
				testkit.Equal(t, c.i, c.leaf.n-1, "the cursor must start each leaf at its last item")
				counts, lasts = append(counts, c.leaf.n), append(lasts, c.leaf.keys[c.i])
			}
			slices.Reverse(counts)
			slices.Reverse(lasts)
			leaves, _ := sizes(tr)
			testkit.Equal(t, counts, leaves, "the cursor must visit the leaves in reverse order")
			testkit.True(t, slices.IsSorted(lasts) && len(slices.Compact(lasts)) == len(leaves),
				"the cursor must visit each leaf once")
		})

		t.Run("reports false for a root leaf", func(t *testing.T) {
			t.Parallel()
			tr := build(shape{n: 3})
			var c cursor[int, int]
			tr.last(&c)
			testkit.False(t, c.prevLeaf(), "a root leaf has no previous leaf")
		})
	})

	t.Run("forward", func(t *testing.T) {
		t.Parallel()

		t.Run("yields every item from the cursor on", func(t *testing.T) {
			t.Parallel()
			tr := varied()
			var c cursor[int, int]
			tr.first(&c)
			c.i = 1
			keys, _, changed := collect(
				-1,
				func(y func(int, int) bool) (int, bool) { return tr.forward(&c, nil, 0, y) },
			)
			testkit.Equal(t, keys, firstKeys(tr.len)[1:], "forward must yield every later item in order")
			testkit.False(t, changed, "the tree did not change")
		})

		for name, tc := range map[string]struct {
			bottom, leaf, end int
		}{
			"stops before an index of a later leaf":   {1, 3, 7},
			"stops before the first item of a leaf":   {1, 3, 0},
			"stops at the end of a leaf":              {0, 1, minItems + 9},
			"stops before an index of the first leaf": {0, 0, 5},
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				tr := varied()
				index := tc.leaf
				for _, bottom := range tr.root.inners[:tc.bottom] {
					index += bottom.n + 1
				}
				leaves, _ := sizes(tr)
				before := 0
				for _, n := range leaves[:index] {
					before += n
				}
				var c cursor[int, int]
				tr.first(&c)
				keys, _, changed := collect(-1, func(y func(int, int) bool) (int, bool) {
					return tr.forward(&c, tr.root.inners[tc.bottom].leaves[tc.leaf], tc.end, y)
				})
				testkit.Equal(t, keys, firstKeys(before+tc.end), "forward must stop at the end position")
				testkit.False(t, changed, "the tree did not change")
			})
		}

		t.Run("stops when yield returns false and returns the key it yielded last", func(t *testing.T) {
			t.Parallel()
			tr := varied()
			var c cursor[int, int]
			tr.first(&c)
			keys, last, changed := collect(
				35,
				func(y func(int, int) bool) (int, bool) { return tr.forward(&c, nil, 0, y) },
			)
			testkit.Equal(t, keys, firstKeys(35), "forward must stop at the first false")
			testkit.Equal(t, last, keys[34], "forward must return the key it yielded last")
			testkit.False(t, changed, "a stop is not a change")
		})

		t.Run("stops after a write and returns the key that it yielded before the write", func(t *testing.T) {
			t.Parallel()
			tr := varied()
			var c cursor[int, int]
			tr.first(&c)
			n := 0
			last, changed := tr.forward(&c, nil, 0, func(k, _ int) bool {
				if n++; n == 40 {
					tr.set(k+1, -k-1)
				}
				return true
			})
			testkit.True(t, changed, "forward must report the write")
			testkit.Equal(t, last, 2*39, "forward must return the key it yielded before the write")
		})
	})

	t.Run("backward", func(t *testing.T) {
		t.Parallel()

		t.Run("yields every item from the cursor down to the smallest key", func(t *testing.T) {
			t.Parallel()
			tr := varied()
			var c cursor[int, int]
			tr.last(&c)
			c.i = 0
			keys, _, changed := collect(-1, func(y func(int, int) bool) (int, bool) { return tr.backward(&c, y) })
			want := firstKeys(tr.len - 1)
			slices.Reverse(want)
			testkit.Equal(t, keys, want, "backward must yield every earlier item in reverse order")
			testkit.False(t, changed, "the tree did not change")
		})

		t.Run("stops when yield returns false and returns the key it yielded last", func(t *testing.T) {
			t.Parallel()
			tr := varied()
			var c cursor[int, int]
			tr.last(&c)
			keys, last, changed := collect(35, func(y func(int, int) bool) (int, bool) { return tr.backward(&c, y) })
			testkit.Len(t, keys, 35, "backward must stop at the first false")
			testkit.Equal(t, last, keys[34], "backward must return the key it yielded last")
			testkit.False(t, changed, "a stop is not a change")
		})

		t.Run("stops after a write and returns the key that it yielded before the write", func(t *testing.T) {
			t.Parallel()
			tr := varied()
			fortieth := 2 * (tr.len - 40)
			var c cursor[int, int]
			tr.last(&c)
			n := 0
			last, changed := tr.backward(&c, func(k, _ int) bool {
				if n++; n == 40 {
					tr.delete(k - 2)
				}
				return true
			})
			testkit.True(t, changed, "backward must report the write")
			testkit.Equal(t, last, fortieth, "backward must return the key it yielded before the write")
		})
	})
}
