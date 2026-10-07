// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package btree

import (
	"slices"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
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
			"gives the separator before a leaf the next key of the leaf": {
				leavesOf(spare, spare), 2 * spare, []int{spare, lean}, [][]int{{1}},
			},
			"gives the separator above the first leaf of an internal node the next key of the leaf": {
				nodesOf(leavesOf(slices.Repeat([]int{spare}, spare)...), leavesOf(slices.Repeat([]int{spare}, spare)...)),
				2 * spare * spare,
				slices.Concat(slices.Repeat([]int{spare}, spare), []int{lean}, slices.Repeat([]int{spare}, spare-1)),
				[][]int{{1}, {minItems, minItems}},
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
			"leaves an internal node of minItems separators alone": {
				nodesOf(leavesOf(slices.Repeat([]int{lean}, spare+1)...), leavesOf(slices.Repeat([]int{lean}, spare+1)...)),
				0,
				slices.Concat([]int{merged}, slices.Repeat([]int{lean}, 2*spare)),
				[][]int{{1}, {minItems, spare}},
			},
			"merges an underfull internal node with its right sibling": {
				nodesOf(leanBottom, leanBottom, leanBottom),
				0,
				slices.Concat([]int{merged}, slices.Repeat([]int{lean}, 3*spare-2)),
				[][]int{{1}, {2 * minItems, minItems}},
			},
			"merges the only two internal nodes into a new root": {
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
				assert.True(t, present, "delete must find the key")
				assert.Equal(t, v, -tc.key, "delete must return the value of the key")
				assert.NoError(t, check(tr), "the tree must keep its invariants")
				leaves, levels := sizes(tr)
				expect.Equal(t, leaves, tc.leaves, "the delete must refill or merge the leaves as documented")
				expect.Equal(t, levels, tc.levels, "the delete must refill or merge the internal nodes as documented")
				expect.Equal(t, keysOf(tr), want, "the tree must contain the other keys in order")
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
			"merges the only two upper nodes into a new root": {
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
				assert.NoError(t, check(tr), "the tree must keep its invariants")
				_, levels := sizes(tr)
				assert.Equal(
					t,
					levels[:len(tc.levels)],
					tc.levels,
					"the upper levels must refill or merge as documented",
				)
				assert.Equal(t, keysOf(tr), want, "the tree must contain the other keys in order")
			})
		}

		t.Run("moves the first internal node of the right upper node into an underfull one", func(t *testing.T) {
			t.Parallel()
			tr := build(nodesOf(nodesOf(slices.Repeat([]shape{leanBottom}, spare)...),
				nodesOf(slices.Repeat([]shape{leanBottom}, spare+1)...)))
			want := keysOf(tr)[1:]
			tr.delete(0)
			assert.NoError(t, check(tr), "the tree must keep its invariants")
			_, levels := sizes(tr)
			assert.Equal(
				t,
				levels[:2],
				[][]int{{1}, {minItems, minItems}},
				"the upper level must refill as documented",
			)
			assert.Equal(t, keysOf(tr), want, "the tree must contain the other keys in order")
		})

		t.Run("puts the leaf that a merge frees on the free list", func(t *testing.T) {
			t.Parallel()
			tr := build(leavesOf(lean, lean, lean))
			freed := tr.root.leaves[1]
			tr.delete(0)
			assert.NoError(t, check(tr), "the tree must keep its invariants")
			assert.Length(t, tr.freeLeaves, 1, "the merge must free one leaf")
			assert.Equal(t, tr.freeLeaves[0], freed, "the free leaf must be the merged one", assert.ByIdentity())
		})

		t.Run("puts a root leaf that the removal empties on the free list", func(t *testing.T) {
			t.Parallel()
			tr := build(shape{n: 1})
			root := tr.leaf
			tr.delete(0)
			assert.Length(t, tr.freeLeaves, 1, "the removal must free one leaf")
			assert.Equal(t, tr.freeLeaves[0], root, "the free leaf must be the empty root leaf", assert.ByIdentity())
		})

		t.Run("puts the root that a merge frees on the free list", func(t *testing.T) {
			t.Parallel()
			tr := build(leavesOf(lean, lean))
			root := tr.root
			tr.delete(0)
			assert.NoError(t, check(tr), "the tree must keep its invariants")
			assert.Length(t, tr.freeLeafParents, 1, "the merge must free one parent of leaves")
			assert.Equal(t, tr.freeLeafParents[0], root, "the free node must be the old root", assert.ByIdentity())
		})

		t.Run("puts the internal node that a merge frees on the free list", func(t *testing.T) {
			t.Parallel()
			tr := build(nodesOf(leanBottom, leanBottom, leanBottom))
			freed := tr.root.inners[1]
			tr.delete(0)
			assert.NoError(t, check(tr), "the tree must keep its invariants")
			assert.Length(t, tr.freeLeafParents, 1, "the merge must free one parent of leaves")
			assert.Equal(t, tr.freeLeafParents[0], freed, "the free node must be the merged one", assert.ByIdentity())
		})
	})

	t.Run("deleteRange", func(t *testing.T) {
		t.Parallel()

		t.Run("counts one write", func(t *testing.T) {
			t.Parallel()
			tr := build(leavesOf(lean, lean))
			tr.deleteRange(0, 10)
			assert.Equal(t, tr.writes, uint64(1), "deleteRange must add one to the count")
		})

		t.Run("copies no node of a clone for a range that is empty", func(t *testing.T) {
			t.Parallel()
			tr := build(leavesOf(lean, lean))
			c := tr.clone()
			assert.Equal(t, tr.deleteRange(10, 10), 0, "an empty range must remove nothing")
			assert.Equal(t, tr.root, c.root, "the tree must share its root with the clone", assert.ByIdentity())
		})
	})

	// threeLevels returns a tree with a root over three bottom nodes of
	// spare lean leaves each.
	threeLevels := func() *ints {
		return build(nodesOf(leanBottom, leanBottom, leanBottom))
	}

	t.Run("clear", func(t *testing.T) {
		t.Parallel()

		t.Run("counts one write", func(t *testing.T) {
			t.Parallel()
			tr := build(leavesOf(lean, lean))
			tr.clear()
			assert.Equal(t, tr.writes, uint64(1), "clear must add one to the count")
		})

		t.Run("keeps at most maxFree free nodes of each kind", func(t *testing.T) {
			t.Parallel()
			var tr ints
			for range maxFree + 1 {
				tr.freeLeaves = append(tr.freeLeaves, &leaf[int, int]{})
				tr.freeLeafParents = append(tr.freeLeafParents,
					&inner[int, int]{leaves: new([maxChildren]*leaf[int, int])})
				tr.freeInnerParents = append(tr.freeInnerParents,
					&inner[int, int]{inners: new([maxChildren]*inner[int, int])})
			}
			tr.clear()
			expect.Length(t, tr.freeLeaves, maxFree, "clear must keep maxFree leaves")
			expect.Length(t, tr.freeLeafParents, maxFree, "clear must keep maxFree parents of leaves")
			expect.Length(t, tr.freeInnerParents, maxFree, "clear must keep maxFree parents of internal nodes")
		})

		t.Run("keeps a free list shorter than maxFree whole", func(t *testing.T) {
			t.Parallel()
			tr := threeLevels()
			tr.reset()
			tr.clear()
			expect.Length(t, tr.freeLeafParents, 3, "clear must keep the three parents of leaves")
			expect.Length(t, tr.freeInnerParents, 1, "clear must keep the root")
			expect.NoError(t, check(tr), "the tree must keep its invariants")
		})

		t.Run("zeroes the slots of the free leaves that it drops", func(t *testing.T) {
			t.Parallel()
			tr := threeLevels()
			tr.reset()
			leaves := tr.freeLeaves
			tr.clear()
			assert.Equal(t, leaves[maxFree:], make([]*leaf[int, int], 3*spare-maxFree),
				"clear must zero the slot of every leaf that it drops")
		})
	})

	t.Run("reset", func(t *testing.T) {
		t.Parallel()

		t.Run("counts one write", func(t *testing.T) {
			t.Parallel()
			tr := threeLevels()
			tr.reset()
			expect.Equal(t, tr.writes, uint64(1), "reset must add one to the count")
			var empty ints
			empty.reset()
			expect.Equal(t, empty.writes, uint64(1), "reset of an empty tree must add one to the count")
		})

		t.Run("leaves an empty tree", func(t *testing.T) {
			t.Parallel()
			tr := threeLevels()
			tr.reset()
			expect.Nil(t, tr.root, "reset must remove the root")
			expect.Nil(t, tr.leaf, "reset must leave no root leaf")
			expect.Equal(t, tr.len, 0, "reset must remove every item")
			var empty ints
			empty.reset()
			expect.Empty(t, empty.freeLeaves, "reset of an empty tree must keep nothing")
		})

		t.Run("keeps every zeroed node on the free list of its kind", func(t *testing.T) {
			t.Parallel()
			tr := threeLevels()
			tr.reset()
			expect.Length(t, tr.freeLeaves, 3*spare, "reset must keep every leaf")
			expect.Length(t, tr.freeLeafParents, 3, "reset must keep every parent of leaves")
			expect.Length(t, tr.freeInnerParents, 1, "reset must keep the root")
			expect.NoError(t, check(tr), "every kept node must be zeroed")
		})

		t.Run("keeps a root leaf on the free list", func(t *testing.T) {
			t.Parallel()
			tr := build(shape{n: 3})
			leaf := tr.leaf
			tr.reset()
			assert.Length(t, tr.freeLeaves, 1, "reset must keep the root leaf")
			expect.Equal(t, tr.freeLeaves[0], leaf, "the kept leaf must be the root leaf", expect.ByIdentity())
			expect.NoError(t, check(tr), "the kept leaf must be zeroed")
		})

		t.Run("keeps only the nodes that the tree wrote after a clone", func(t *testing.T) {
			t.Parallel()
			tr := threeLevels()
			want := keysOf(tr)
			c := tr.clone()
			tr.set(1, -1)
			tr.reset()
			expect.Length(t, tr.freeLeaves, 1, "reset must keep the leaf that the write copied")
			expect.Length(t, tr.freeLeafParents, 1, "reset must keep the parent of leaves that the write copied")
			expect.Length(t, tr.freeInnerParents, 1, "reset must keep the root that the write copied")
			expect.NoError(t, check(&c), "the clone must keep its invariants")
			expect.Equal(t, keysOf(&c), want, "the clone must keep its items")
		})

		for _, tt := range []struct {
			name string
			tree shape
		}{
			{
				name: "keeps no node of a tree of internal nodes right after a clone",
				tree: nodesOf(leanBottom, leanBottom, leanBottom),
			},
			{name: "keeps no root leaf right after a clone", tree: shape{n: 3}},
		} {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				tr := build(tt.tree)
				want := keysOf(tr)
				c := tr.clone()
				tr.reset()
				expect.Empty(t, tr.freeLeaves, "every leaf belongs to the clone too")
				expect.Empty(t, tr.freeLeafParents, "every parent of leaves belongs to the clone too")
				expect.Empty(t, tr.freeInnerParents, "every parent of internal nodes belongs to the clone too")
				expect.NoError(t, check(&c), "the clone must keep its invariants")
				expect.Equal(t, keysOf(&c), want, "the clone must keep its items")
			})
		}

		t.Run("gives the kept nodes to the inserts that follow", func(t *testing.T) {
			t.Parallel()
			tr := threeLevels()
			tr.reset()
			kept := map[any]bool{}
			for _, l := range tr.freeLeaves {
				kept[l] = true
			}
			for _, in := range slices.Concat(tr.freeLeafParents, tr.freeInnerParents) {
				kept[in] = true
			}
			// One key more than maxChildren full leaves splits the root, so
			// the refill takes nodes of every kind.
			for k := range maxItems*maxChildren + 1 {
				tr.set(k, -k)
			}
			assert.NotNil(t, tr.root.inners, "the refill must have a root over internal nodes")
			// fresh counts the nodes of the refill that the reset did not keep.
			fresh := 0
			var walk func(in *inner[int, int])
			walk = func(in *inner[int, int]) {
				if !kept[in] {
					fresh++
				}
				if in.leaves != nil {
					for _, l := range in.leaves[:in.n+1] {
						if !kept[l] {
							fresh++
						}
					}

					return
				}
				for _, c := range in.inners[:in.n+1] {
					walk(c)
				}
			}
			walk(tr.root)
			expect.Equal(t, fresh, 0, "every node of the refill must be a kept one")
			expect.NoError(t, check(tr), "the tree must keep its invariants")
		})
	})

	for name, write := range map[string]func(tr *ints){
		"delete": func(tr *ints) { tr.delete(0) },
		"popMin": func(tr *ints) { tr.popMin() },
		"popMax": func(tr *ints) { tr.popMax() },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			t.Run("counts one write", func(t *testing.T) {
				t.Parallel()
				tr := build(leavesOf(lean, lean))
				write(tr)
				assert.Equal(t, tr.writes, uint64(1), name+" must add one to the count")
			})
		})
	}
}
