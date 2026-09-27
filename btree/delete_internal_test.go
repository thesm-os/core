// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package btree

import (
	"slices"
	"testing"

	"go.thesmos.sh/testkit"
)

// TestDelete is in package btree because the refills and merges that it
// pins change the shape of a tree and nothing that the exported methods
// return.
func TestDelete(t *testing.T) {
	t.Parallel()

	// A spare leaf or internal node has one item or separator more than
	// the fewest, minItems, so it can give one to a sibling.
	const lean, spare = minItems, minItems + 1
	// merged is the size of a leaf that merges a leaf of lean items with
	// one that a removal left underfull.
	const merged = 2*lean - 1
	// A bottom node has leaves as children. A lean bottom node has spare
	// lean leaves, so minItems separators.
	leanBottom := leavesOf(slices.Repeat([]int{lean}, spare)...)

	t.Run("remove", func(t *testing.T) {
		t.Parallel()

		// Each tree has the keys 0, 2, 4 and so on, so the key at index
		// i in key order is 2i.
		for name, tc := range map[string]struct {
			tree   shape
			key    int
			leaves []int
			levels [][]int
		}{
			"leaves a leaf of minItems items alone": {
				leavesOf(spare, spare), 0, []int{lean, spare}, [][]int{{1}},
			},
			"moves the last item of the left leaf into an underfull leaf": {
				leavesOf(spare, lean), 2 * spare, []int{lean, lean}, [][]int{{1}},
			},
			"moves the first item of the right leaf into an underfull leaf": {
				leavesOf(lean, spare), 0, []int{lean, lean}, [][]int{{1}},
			},
			"takes an item from the left leaf before the right": {
				leavesOf(spare, lean, spare), 2 * spare, []int{lean, lean, spare}, [][]int{{2}},
			},
			"merges an underfull leaf with its right sibling": {
				leavesOf(lean, lean, lean), 0, []int{merged, lean}, [][]int{{1}},
			},
			"merges an underfull leaf into its left sibling": {
				leavesOf(lean, lean, lean), 2 * 2 * lean, []int{lean, merged}, [][]int{{1}},
			},
			"merges the only two leaves into a root leaf": {
				leavesOf(lean, lean), 2 * lean, []int{merged}, nil,
			},
			"empties a root leaf": {
				shape{n: 1}, 0, nil, nil,
			},
			"moves the last leaf of the left internal node into an underfull one": {
				nodesOf(leavesOf(slices.Repeat([]int{lean}, spare+1)...), leanBottom),
				2 * lean * (spare + 1),
				slices.Concat(slices.Repeat([]int{lean}, spare+1), []int{merged}, slices.Repeat([]int{lean}, spare-2)),
				[][]int{{1}, {minItems, minItems}},
			},
			"moves the first leaf of the right internal node into an underfull one": {
				nodesOf(leanBottom, leavesOf(slices.Repeat([]int{lean}, spare+1)...)),
				0,
				slices.Concat([]int{merged}, slices.Repeat([]int{lean}, 2*spare-1)),
				[][]int{{1}, {minItems, minItems}},
			},
			"merges an underfull internal node with its right sibling": {
				nodesOf(leanBottom, leanBottom, leanBottom),
				0,
				slices.Concat([]int{merged}, slices.Repeat([]int{lean}, 3*spare-2)),
				[][]int{{1}, {2 * minItems, minItems}},
			},
			"merges an underfull internal node into its left sibling, which becomes the root": {
				nodesOf(leanBottom, leanBottom),
				2 * lean * spare,
				slices.Concat(slices.Repeat([]int{lean}, spare), []int{merged}, slices.Repeat([]int{lean}, spare-2)),
				[][]int{{2 * minItems}},
			},
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				tr := build(tc.tree)
				want := slices.DeleteFunc(keysOf(tr), func(k int) bool { return k == tc.key })
				if len(want) == 0 {
					want = nil
				}
				v, present := tr.delete(tc.key)
				testkit.True(t, present && v == -tc.key, "delete must return the value of the key")
				requireValid(t, tr)
				leaves, levels := sizes(tr)
				testkit.Equal(t, leaves, tc.leaves, "the delete must refill or merge the leaves as documented")
				testkit.Equal(t, levels, tc.levels, "the delete must refill or merge the internal nodes as documented")
				testkit.Equal(t, keysOf(tr), want, "the tree must hold the other keys in order")
			})
		}

		// Each upper node has the given number of lean bottom nodes. The
		// key is the first key of the second upper node, whose removal
		// merges two leaves, then two bottom nodes, and leaves the second
		// upper node underfull.
		for name, tc := range map[string]struct {
			uppers []int
			levels [][]int
		}{
			"moves the last internal node of the left upper node into an underfull one": {
				[]int{spare + 1, spare}, [][]int{{1}, {minItems, minItems}},
			},
			"merges an underfull upper node into its left sibling, which becomes the root": {
				[]int{spare, spare}, [][]int{{2 * minItems}},
			},
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				tr := build(nodesOf(nodesOf(slices.Repeat([]shape{leanBottom}, tc.uppers[0])...),
					nodesOf(slices.Repeat([]shape{leanBottom}, tc.uppers[1])...)))
				key := 2 * lean * spare * tc.uppers[0]
				want := slices.DeleteFunc(keysOf(tr), func(k int) bool { return k == key })
				tr.delete(key)
				requireValid(t, tr)
				_, levels := sizes(tr)
				testkit.Equal(
					t,
					levels[:len(tc.levels)],
					tc.levels,
					"the upper levels must refill or merge as documented",
				)
				testkit.Equal(t, keysOf(tr), want, "the tree must hold the other keys in order")
			})
		}

		t.Run("moves the first internal node of the right upper node into an underfull one", func(t *testing.T) {
			t.Parallel()
			tr := build(nodesOf(nodesOf(slices.Repeat([]shape{leanBottom}, spare)...),
				nodesOf(slices.Repeat([]shape{leanBottom}, spare+1)...)))
			want := keysOf(tr)[1:]
			tr.delete(0)
			requireValid(t, tr)
			_, levels := sizes(tr)
			testkit.Equal(
				t,
				levels[:2],
				[][]int{{1}, {minItems, minItems}},
				"the upper level must refill as documented",
			)
			testkit.Equal(t, keysOf(tr), want, "the tree must hold the other keys in order")
		})

		t.Run("puts the leaf that a merge frees on the free list", func(t *testing.T) {
			t.Parallel()
			tr := build(leavesOf(lean, lean, lean))
			freed := tr.root.leaves[1]
			tr.delete(0)
			testkit.True(t, len(tr.freeLeaves) == 1 && tr.freeLeaves[0] == freed, "the merged leaf must be free")
			requireValid(t, tr)
		})

		t.Run("puts the root that a merge frees on the free list", func(t *testing.T) {
			t.Parallel()
			tr := build(leavesOf(lean, lean))
			root := tr.root
			tr.delete(0)
			testkit.True(t, len(tr.freeInners) == 1 && tr.freeInners[0] == root, "the old root must be free")
			requireValid(t, tr)
		})
	})

	for name, write := range map[string]func(tr *ints){
		"delete":      func(tr *ints) { tr.delete(0) },
		"deleteRange": func(tr *ints) { tr.deleteRange(0, 10) },
		"popMin":      func(tr *ints) { tr.popMin() },
		"popMax":      func(tr *ints) { tr.popMax() },
		"clear":       func(tr *ints) { tr.clear() },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			t.Run("counts one write", func(t *testing.T) {
				t.Parallel()
				tr := build(leavesOf(lean, lean))
				write(tr)
				testkit.Equal(t, tr.writes, uint64(1), name+" must add one to the count")
			})
		})
	}
}
