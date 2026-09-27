// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package btree

import (
	"testing"

	"go.thesmos.sh/testkit"
)

// shape is the shape of a subtree: a leaf of n items, or an internal node
// over kids.
type shape struct {
	n    int
	kids []shape
}

// leavesOf returns the shape of an internal node over leaves with the
// numbers of items in counts.
func leavesOf(counts ...int) shape {
	kids := make([]shape, len(counts))
	for i, n := range counts {
		kids[i] = shape{n: n}
	}

	return shape{kids: kids}
}

// nodesOf returns the shape of an internal node over kids.
func nodesOf(kids ...shape) shape {
	return shape{kids: kids}
}

// builder gives the keys 0, 2, 4 and so on to the leaves that it builds.
type builder struct {
	next int
}

// leafOf returns a leaf with the next n keys.
func (b *builder) leafOf(n int) *leaf[int, int] {
	l := &leaf[int, int]{n: n}
	for i := range n {
		l.keys[i], l.vals[i] = b.next, -b.next
		b.next += 2
	}

	return l
}

// innerOf returns the internal node of shape s with its subtree.
func (b *builder) innerOf(s shape) *inner[int, int] {
	in := &inner[int, int]{n: len(s.kids) - 1}
	if s.kids[0].kids == nil {
		in.leaves = new([maxChildren]*leaf[int, int])
	} else {
		in.inners = new([maxChildren]*inner[int, int])
	}
	for i, kid := range s.kids {
		if i > 0 {
			in.keys[i-1] = b.next
		}
		if in.leaves != nil {
			in.leaves[i] = b.leafOf(kid.n)
			in.count += kid.n
		} else {
			in.inners[i] = b.innerOf(kid)
			in.count += in.inners[i].count
		}
	}

	return in
}

// build returns a tree of shape s whose keys are 0, 2, 4 and so on in
// order, with the negation of each key as its value. The separator before
// each child is the smallest key under it, and the odd keys are free for
// inserts at known places.
func build(s shape) *ints {
	var b builder
	if s.kids == nil {
		return &ints{leaf: b.leafOf(s.n), len: s.n}
	}
	root := b.innerOf(s)

	return &ints{root: root, len: root.count}
}

// TestNode is in package btree because nodes, free lists and node IDs are
// unexported.
func TestNode(t *testing.T) {
	t.Parallel()

	t.Run("newLeaf", func(t *testing.T) {
		t.Parallel()

		t.Run("takes a free leaf and gives it the ID of the tree", func(t *testing.T) {
			t.Parallel()
			free := &leaf[int, int]{}
			tr := ints{owner: 7, freeLeaves: []*leaf[int, int]{free}}
			testkit.True(t, tr.newLeaf() == free && free.owner == 7, "newLeaf must take the free leaf")
			testkit.Len(t, tr.freeLeaves, 0, "the free list must give up the leaf")
		})

		t.Run("allocates a leaf with the ID of the tree when the free list is empty", func(t *testing.T) {
			t.Parallel()
			tr := ints{owner: 7}
			l := tr.newLeaf()
			testkit.True(t, l.owner == 7 && l.n == 0, "newLeaf must allocate an empty leaf with the tree's ID")
		})
	})

	t.Run("newLeafParent", func(t *testing.T) {
		t.Parallel()

		t.Run("replaces the array of internal nodes of a free node with an array of leaves", func(t *testing.T) {
			t.Parallel()
			tr := ints{freeInners: []*inner[int, int]{{inners: new([maxChildren]*inner[int, int])}}}
			in := tr.newLeafParent()
			testkit.True(
				t,
				in.leaves != nil && in.inners == nil,
				"a parent of leaves must have only an array of leaves",
			)
		})

		t.Run("keeps the array of leaves of a free node", func(t *testing.T) {
			t.Parallel()
			kept := new([maxChildren]*leaf[int, int])
			tr := ints{freeInners: []*inner[int, int]{{leaves: kept}}}
			testkit.True(t, tr.newLeafParent().leaves == kept, "a free node must keep an array of leaves")
		})
	})

	t.Run("newInnerParent", func(t *testing.T) {
		t.Parallel()

		t.Run("replaces the array of leaves of a free node with an array of internal nodes", func(t *testing.T) {
			t.Parallel()
			tr := ints{freeInners: []*inner[int, int]{{leaves: new([maxChildren]*leaf[int, int])}}}
			in := tr.newInnerParent()
			testkit.True(
				t,
				in.inners != nil && in.leaves == nil,
				"a parent of internal nodes must have only their array",
			)
		})

		t.Run("keeps the array of internal nodes of a free node", func(t *testing.T) {
			t.Parallel()
			kept := new([maxChildren]*inner[int, int])
			tr := ints{freeInners: []*inner[int, int]{{inners: kept}}}
			testkit.True(t, tr.newInnerParent().inners == kept, "a free node must keep an array of internal nodes")
		})
	})

	t.Run("newInner", func(t *testing.T) {
		t.Parallel()

		t.Run("takes a free node and gives it the ID of the tree", func(t *testing.T) {
			t.Parallel()
			free := &inner[int, int]{}
			tr := ints{owner: 7, freeInners: []*inner[int, int]{free}}
			testkit.True(t, tr.newInner() == free && free.owner == 7, "newInner must take the free node")
			testkit.Len(t, tr.freeInners, 0, "the free list must give up the node")
		})

		t.Run("allocates a node without a child array when the free list is empty", func(t *testing.T) {
			t.Parallel()
			tr := ints{owner: 7}
			in := tr.newInner()
			testkit.True(t, in.owner == 7 && in.leaves == nil && in.inners == nil, "newInner must allocate a bare node")
		})
	})

	t.Run("releaseLeaf", func(t *testing.T) {
		t.Parallel()

		t.Run("zeroes a leaf of the tree and keeps it", func(t *testing.T) {
			t.Parallel()
			tr := ints{owner: 3}
			l := &leaf[int, int]{owner: 3, n: 2, keys: [maxItems]int{5, 6}, vals: [maxItems]int{-5, -6}}
			tr.releaseLeaf(l)
			testkit.True(t, len(tr.freeLeaves) == 1 && tr.freeLeaves[0] == l, "the leaf must be on the free list")
			requireValid(t, &tr)
		})

		t.Run("keeps at most maxFree leaves", func(t *testing.T) {
			t.Parallel()
			var tr ints
			for range maxFree + 1 {
				tr.releaseLeaf(&leaf[int, int]{n: 1, keys: [maxItems]int{7}})
			}
			testkit.Len(t, tr.freeLeaves, maxFree, "the free list must stop at maxFree leaves")
			requireValid(t, &tr)
		})

		t.Run("leaves a leaf of another tree as it is", func(t *testing.T) {
			t.Parallel()
			tr := ints{owner: 1}
			l := &leaf[int, int]{owner: 2, n: 1, keys: [maxItems]int{7}}
			tr.releaseLeaf(l)
			testkit.Len(t, tr.freeLeaves, 0, "a leaf of another tree may be part of a clone")
			testkit.True(t, l.n == 1 && l.keys[0] == 7, "a clone's leaf must keep its items")
		})
	})

	t.Run("releaseInner", func(t *testing.T) {
		t.Parallel()

		t.Run("zeroes an internal node of the tree with either child array and keeps it", func(t *testing.T) {
			t.Parallel()
			tr := ints{owner: 3}
			parent := &inner[int, int]{owner: 3, n: 1, count: 2, keys: [maxItems]int{4}}
			parent.leaves = &[maxChildren]*leaf[int, int]{{n: 1}, {n: 1}}
			upper := &inner[int, int]{owner: 3, n: 1, count: 2, keys: [maxItems]int{4}}
			upper.inners = &[maxChildren]*inner[int, int]{{}, {}}
			tr.releaseInner(parent)
			tr.releaseInner(upper)
			testkit.Len(t, tr.freeInners, 2, "both nodes must be on the free list")
			requireValid(t, &tr)
		})

		t.Run("keeps at most maxFree internal nodes", func(t *testing.T) {
			t.Parallel()
			var tr ints
			for range maxFree + 1 {
				tr.releaseInner(&inner[int, int]{leaves: new([maxChildren]*leaf[int, int])})
			}
			testkit.Len(t, tr.freeInners, maxFree, "the free list must stop at maxFree internal nodes")
		})

		t.Run("leaves an internal node of another tree as it is", func(t *testing.T) {
			t.Parallel()
			tr := ints{owner: 1}
			in := &inner[int, int]{owner: 2, n: 1, keys: [maxItems]int{4}}
			in.leaves = &[maxChildren]*leaf[int, int]{{n: 1}, {n: 1}}
			tr.releaseInner(in)
			testkit.Len(t, tr.freeInners, 0, "a node of another tree may be part of a clone")
			testkit.True(t, in.n == 1 && in.leaves[1] != nil, "a clone's node must keep its children")
		})
	})

	t.Run("mutLeaf", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a leaf of the tree as it is", func(t *testing.T) {
			t.Parallel()
			tr := ints{owner: 3}
			l := &leaf[int, int]{owner: 3}
			p := l
			testkit.True(t, tr.mutLeaf(&p) == l && p == l, "a leaf of the tree must not be copied")
		})

		t.Run("replaces a leaf of another tree with a copy of its items", func(t *testing.T) {
			t.Parallel()
			tr := ints{owner: 3}
			l := &leaf[int, int]{owner: 4, n: 2, keys: [maxItems]int{5, 6}, vals: [maxItems]int{-5, -6}}
			p := l
			c := tr.mutLeaf(&p)
			testkit.True(t, c != l && p == c && c.owner == 3, "the copy must replace the leaf and have the tree's ID")
			testkit.Equal(t, c.keys, l.keys, "the copy must have the keys")
			testkit.Equal(t, c.vals, l.vals, "the copy must have the values")
			testkit.Equal(t, c.n, 2, "the copy must have the item count")
		})
	})

	t.Run("mutInner", func(t *testing.T) {
		t.Parallel()

		t.Run("returns an internal node of the tree as it is", func(t *testing.T) {
			t.Parallel()
			tr := ints{owner: 3}
			in := &inner[int, int]{owner: 3}
			p := in
			testkit.True(t, tr.mutInner(&p) == in && p == in, "a node of the tree must not be copied")
		})

		t.Run("replaces a parent of leaves of another tree with a copy", func(t *testing.T) {
			t.Parallel()
			tr := ints{owner: 3}
			a, b := &leaf[int, int]{n: 1}, &leaf[int, int]{n: 1}
			in := &inner[int, int]{owner: 4, n: 1, count: 2, keys: [maxItems]int{8}}
			in.leaves = &[maxChildren]*leaf[int, int]{a, b}
			p := in
			c := tr.mutInner(&p)
			testkit.True(t, c != in && p == c && c.owner == 3, "the copy must replace the node and have the tree's ID")
			testkit.True(t, c.n == 1 && c.count == 2 && c.keys[0] == 8, "the copy must have the separator and count")
			testkit.True(t, c.inners == nil && c.leaves[0] == a && c.leaves[1] == b, "the copy must have the leaves")
		})

		t.Run("replaces a parent of internal nodes of another tree with a copy", func(t *testing.T) {
			t.Parallel()
			tr := ints{owner: 3}
			a, b := &inner[int, int]{count: 2}, &inner[int, int]{count: 2}
			in := &inner[int, int]{owner: 4, n: 1, count: 4, keys: [maxItems]int{8}}
			in.inners = &[maxChildren]*inner[int, int]{a, b}
			p := in
			c := tr.mutInner(&p)
			testkit.True(t, c != in && p == c && c.owner == 3, "the copy must replace the node and have the tree's ID")
			testkit.True(t, c.n == 1 && c.count == 4 && c.keys[0] == 8, "the copy must have the separator and count")
			testkit.True(t, c.leaves == nil && c.inners[0] == a && c.inners[1] == b, "the copy must have the nodes")
		})
	})
}
