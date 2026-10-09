// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package btree

import (
	"slices"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
)

// TestInsert is in package btree because the splits that it pins change
// the shape of a tree and nothing that the exported methods return.
func TestInsert(t *testing.T) {
	t.Parallel()

	t.Run("insert", func(t *testing.T) {
		t.Parallel()

		// A full leaf has maxItems items, and a lean leaf has minItems.
		const full, lean = maxItems, minItems
		// fullAt returns the leaf sizes of a full internal node whose
		// leaves are lean but the one at index at, which is full.
		fullAt := func(at int) []int {
			counts := slices.Repeat([]int{lean}, maxChildren)
			counts[at] = full
			return counts
		}
		// halvedAt returns the leaf sizes that fullAt(at) has after the
		// full leaf splits into halves.
		halvedAt := func(at int) []int {
			return slices.Insert(slices.Repeat([]int{lean}, maxChildren-1), at, splitAt, splitAt)
		}
		// Each tree has the keys 0, 2, 4 and so on, so an odd key is new,
		// and the key at index i in key order is 2i.
		for name, tc := range map[string]struct {
			tree   shape
			key    int
			leaves []int
			levels [][]int
		}{
			"splits a full root leaf into halves of splitAt items under a new root": {
				shape{n: full}, 1, []int{splitAt, splitAt}, [][]int{{1}},
			},
			"puts the key into the right half when its index is splitAt": {
				shape{n: full}, 2*splitAt - 1, []int{splitAt, splitAt}, [][]int{{1}},
			},
			"starts a leaf beside a full root leaf when the key appends to it": {
				shape{n: full}, 2 * full, []int{full, 1}, [][]int{{1}},
			},
			"splits a full leaf into halves when the key appends to it away from the right edge": {
				leavesOf(full, lean), 2*full - 1, []int{splitAt, splitAt, lean}, [][]int{{2}},
			},
			"starts a leaf after a full last leaf when the key appends to it": {
				leavesOf(lean, full), 2 * (lean + full), []int{lean, full, 1}, [][]int{{2}},
			},
			"splits a full internal node below its middle into splitAt and minItems separators": {
				leavesOf(fullAt(5)...), 2*lean*5 + 1, halvedAt(5), [][]int{{1}, {splitAt, minItems}},
			},
			"splits a full internal node at its middle into splitAt and minItems separators": {
				leavesOf(fullAt(splitAt)...), 2*lean*splitAt + 1, halvedAt(splitAt), [][]int{{1}, {splitAt, minItems}},
			},
			"splits a full internal node above its middle into splitAt and minItems separators": {
				leavesOf(fullAt(40)...), 2*lean*40 + 1, halvedAt(40), [][]int{{1}, {splitAt, minItems}},
			},
			"keeps maxItems-1 separators in a full internal node when the key appends to it": {
				leavesOf(fullAt(maxItems)...), 2 * (lean*maxItems + full), append(fullAt(maxItems), 1),
				[][]int{{1}, {maxItems - 1, 1}},
			},
			"inserts a separator into an internal parent with room": {
				nodesOf(leavesOf(slices.Repeat([]int{lean}, splitAt)...), leavesOf(fullAt(maxItems)...)),
				2*lean*(splitAt+maxItems) + 1,
				slices.Concat(slices.Repeat([]int{lean}, splitAt), halvedAt(maxItems)),
				[][]int{{2}, {minItems, splitAt, minItems}},
			},
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				tr := build(tc.tree)
				want := append(keysOf(tr), tc.key)
				slices.Sort(want)
				_, present := tr.set(tc.key, -tc.key)
				assert.False(t, present, "the key must be new")
				assert.NoError(t, check(tr), "the tree must keep its invariants")
				leaves, levels := sizes(tr)
				expect.Equal(t, leaves, tc.leaves, "the insert must split the leaf as documented")
				expect.Equal(t, levels, tc.levels, "the insert must split the internal nodes as documented")
				expect.Equal(t, keysOf(tr), want, "the tree must contain every key in order")
			})
		}

		t.Run("splits a full root above internal nodes under a new root", func(t *testing.T) {
			t.Parallel()
			// Every bottom node has splitAt lean leaves, but the one at
			// index 10, which is full and ends with a full leaf. The root
			// is full.
			bottoms := slices.Repeat([]shape{leavesOf(slices.Repeat([]int{lean}, splitAt)...)}, maxChildren)
			bottoms[10] = leavesOf(fullAt(maxItems)...)
			tr := build(nodesOf(bottoms...))
			key := 2*lean*splitAt*10 + 2*lean*maxItems + 1
			tr.set(key, -key)
			assert.NoError(t, check(tr), "the tree must keep its invariants")
			_, levels := sizes(tr)
			expect.Equal(t, levels[:2], [][]int{{1}, {splitAt, minItems}}, "the root must split under a new root")
			expect.NotNil(t, tr.root.inners, "the new root must be above internal nodes")
		})

		t.Run("leaves every leaf full when keys arrive in order", func(t *testing.T) {
			t.Parallel()
			var tr ints
			for k := range maxItems * maxItems * 3 {
				tr.set(k, -k)
			}
			assert.NoError(t, check(&tr), "the tree must keep its invariants")
			leaves, _ := sizes(&tr)
			assert.Equal(t, leaves, slices.Repeat([]int{maxItems}, maxItems*3), "every leaf must be full")
		})

		t.Run("leaves maxItems-1 separators in every internal node when keys arrive in order", func(t *testing.T) {
			t.Parallel()
			var tr ints
			for k := range maxItems * maxItems * 3 {
				tr.set(k, -k)
			}
			_, levels := sizes(&tr)
			assert.Equal(t, levels, [][]int{{2}, slices.Repeat([]int{maxItems - 1}, 3)},
				"an internal node that splits at the right edge must keep maxItems-1 separators")
		})
	})

	t.Run("set", func(t *testing.T) {
		t.Parallel()

		t.Run("counts one write per call", func(t *testing.T) {
			t.Parallel()
			tr := build(leavesOf(minItems, minItems))
			tr.set(1, -1)
			tr.set(0, 1)
			assert.Equal(t, tr.writes, uint64(2), "each set must add one to the count")
		})
	})

	t.Run("update", func(t *testing.T) {
		t.Parallel()

		t.Run("counts one write when fn does not write", func(t *testing.T) {
			t.Parallel()
			tr := build(leavesOf(minItems, minItems))
			tr.update(0, func(v int, _ bool) int { return v })
			assert.Equal(t, tr.writes, uint64(1), "update must add one to the count")
		})

		t.Run("stores the result with a second descent after fn writes", func(t *testing.T) {
			t.Parallel()
			tr := build(leavesOf(minItems, minItems))
			tr.update(1, func(int, bool) int {
				tr.set(3, -3)
				return -1
			})
			assert.NoError(t, check(tr), "the tree must keep its invariants")
			expect.Equal(t, tr.writes, uint64(3), "update, the set in fn and the second descent must count")
			v, ok := tr.get(1)
			assert.True(t, ok, "the second descent must store the key")
			expect.Equal(t, v, -1, "the second descent must store the result")
		})

		t.Run("returns the result of fn after fn writes", func(t *testing.T) {
			t.Parallel()
			tr := build(leavesOf(minItems, minItems))
			got := tr.update(1, func(int, bool) int {
				tr.set(3, -3)
				return -1
			})
			assert.Equal(t, got, -1, "update must return the result of fn")
		})
	})
}
