// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package btree

import (
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
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
// unexported. Equal under ByIdentity checks that a call returns or keeps
// one particular node, which no comparison by value shows.
func TestNode(t *testing.T) {
	t.Parallel()

	t.Run("newLeaf", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a free leaf with the ID of the tree", func(t *testing.T) {
			t.Parallel()
			free := &leaf[int, int]{}
			tr := ints{owner: 7, freeLeaves: []*leaf[int, int]{free}}
			got := tr.newLeaf()
			expect.Equal(t, got, free, "newLeaf must return the free leaf", expect.ByIdentity())
			expect.Equal(t, got.owner, uint64(7), "the leaf must have the ID of the tree")
			expect.Empty(t, tr.freeLeaves, "the free list must give up the leaf")
		})

		t.Run("zeroes the slot of the free list that the leaf leaves", func(t *testing.T) {
			t.Parallel()
			tr := ints{freeLeaves: []*leaf[int, int]{{}, {}}}
			backing := tr.freeLeaves
			tr.newLeaf()
			assert.Nil(t, backing[1], "the free list must not keep the leaf reachable")
		})

		t.Run("allocates a leaf with the ID of the tree when the free list is empty", func(t *testing.T) {
			t.Parallel()
			tr := ints{owner: 7}
			l := tr.newLeaf()
			expect.Equal(t, l.owner, uint64(7), "the leaf must have the ID of the tree")
			expect.Equal(t, l.n, 0, "the leaf must be empty")
		})
	})

	t.Run("newLeafParent", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a free parent of leaves with the ID of the tree", func(t *testing.T) {
			t.Parallel()
			free := &inner[int, int]{leaves: new([maxChildren]*leaf[int, int])}
			tr := ints{owner: 7, freeLeafParents: []*inner[int, int]{free}}
			got := tr.newLeafParent()
			expect.Equal(t, got, free, "newLeafParent must return the free node", expect.ByIdentity())
			expect.Equal(t, got.owner, uint64(7), "the node must have the ID of the tree")
			expect.Empty(t, tr.freeLeafParents, "the free list must give up the node")
		})

		t.Run("allocates a node with an array of leaves when its free list is empty", func(t *testing.T) {
			t.Parallel()
			tr := ints{owner: 7, freeInnerParents: []*inner[int, int]{{inners: new([maxChildren]*inner[int, int])}}}
			in := tr.newLeafParent()
			expect.Equal(t, in.owner, uint64(7), "the node must have the ID of the tree")
			expect.NotNil(t, in.leaves, "the node must have an array of leaves")
			expect.Nil(t, in.inners, "the node must have no array of internal nodes")
			expect.Length(t, tr.freeInnerParents, 1, "a free parent of internal nodes must remain on its list")
		})
	})

	t.Run("newInnerParent", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a free parent of internal nodes with the ID of the tree", func(t *testing.T) {
			t.Parallel()
			free := &inner[int, int]{inners: new([maxChildren]*inner[int, int])}
			tr := ints{owner: 7, freeInnerParents: []*inner[int, int]{free}}
			got := tr.newInnerParent()
			expect.Equal(t, got, free, "newInnerParent must return the free node", expect.ByIdentity())
			expect.Equal(t, got.owner, uint64(7), "the node must have the ID of the tree")
			expect.Empty(t, tr.freeInnerParents, "the free list must give up the node")
		})

		t.Run("allocates a node with an array of internal nodes when its free list is empty", func(t *testing.T) {
			t.Parallel()
			tr := ints{owner: 7, freeLeafParents: []*inner[int, int]{{leaves: new([maxChildren]*leaf[int, int])}}}
			in := tr.newInnerParent()
			expect.Equal(t, in.owner, uint64(7), "the node must have the ID of the tree")
			expect.NotNil(t, in.inners, "the node must have an array of internal nodes")
			expect.Nil(t, in.leaves, "the node must have no array of leaves")
			expect.Length(t, tr.freeLeafParents, 1, "a free parent of leaves must remain on its list")
		})
	})

	t.Run("releaseLeaf", func(t *testing.T) {
		t.Parallel()

		t.Run("keeps a zeroed leaf of the tree on the free list", func(t *testing.T) {
			t.Parallel()
			tr := ints{owner: 3}
			l := &leaf[int, int]{owner: 3, n: 2, keys: [maxItems]int{5, 6}, vals: [maxItems]int{-5, -6}}
			tr.releaseLeaf(l)
			assert.Length(t, tr.freeLeaves, 1, "the free list must keep the leaf")
			expect.Equal(t, tr.freeLeaves[0], l, "the free list must keep that leaf", expect.ByIdentity())
			expect.NoError(t, check(&tr), "the free leaf must be zeroed")
		})

		t.Run("keeps at most maxFree leaves", func(t *testing.T) {
			t.Parallel()
			var tr ints
			for range maxFree + 1 {
				tr.releaseLeaf(&leaf[int, int]{n: 1, keys: [maxItems]int{7}})
			}
			expect.Length(t, tr.freeLeaves, maxFree, "the free list must stop at maxFree leaves")
			expect.NoError(t, check(&tr), "every free leaf must be zeroed")
		})

		t.Run("leaves a leaf of another tree as it is", func(t *testing.T) {
			t.Parallel()
			tr := ints{owner: 1}
			l := &leaf[int, int]{owner: 2, n: 1, keys: [maxItems]int{7}}
			tr.releaseLeaf(l)
			expect.Empty(t, tr.freeLeaves, "a leaf of another tree may be part of a clone")
			expect.Equal(t, l.n, 1, "a clone's leaf must keep its item count")
			expect.Equal(t, l.keys[0], 7, "a clone's leaf must keep its key")
		})
	})

	t.Run("releaseInner", func(t *testing.T) {
		t.Parallel()

		t.Run("keeps a zeroed internal node of the tree on the free list of its kind", func(t *testing.T) {
			t.Parallel()
			tr := ints{owner: 3}
			parent := &inner[int, int]{owner: 3, n: 1, count: 2, keys: [maxItems]int{4}}
			parent.leaves = &[maxChildren]*leaf[int, int]{{n: 1}, {n: 1}}
			upper := &inner[int, int]{owner: 3, n: 1, count: 2, keys: [maxItems]int{4}}
			upper.inners = &[maxChildren]*inner[int, int]{{}, {}}
			tr.releaseInner(parent)
			tr.releaseInner(upper)
			assert.Length(t, tr.freeLeafParents, 1, "the parent of leaves must be on its free list")
			assert.Length(t, tr.freeInnerParents, 1, "the parent of internal nodes must be on its free list")
			expect.Equal(t, tr.freeLeafParents[0], parent, "the list of parents of leaves must keep that node",
				expect.ByIdentity())
			expect.Equal(t, tr.freeInnerParents[0], upper, "the list of parents of internal nodes must keep that node",
				expect.ByIdentity())
			expect.NoError(t, check(&tr), "the free nodes must be zeroed")
		})

		t.Run("keeps at most maxFree internal nodes of each kind", func(t *testing.T) {
			t.Parallel()
			var tr ints
			for range maxFree + 1 {
				tr.releaseInner(&inner[int, int]{leaves: new([maxChildren]*leaf[int, int])})
				tr.releaseInner(&inner[int, int]{inners: new([maxChildren]*inner[int, int])})
			}
			expect.Length(t, tr.freeLeafParents, maxFree, "the list of parents of leaves must stop at maxFree")
			expect.Length(t, tr.freeInnerParents, maxFree, "the list of parents of internal nodes must stop at maxFree")
		})

		t.Run("keeps a parent of leaves while the list of parents of internal nodes is full", func(t *testing.T) {
			t.Parallel()
			var tr ints
			for range maxFree {
				tr.releaseInner(&inner[int, int]{inners: new([maxChildren]*inner[int, int])})
			}
			tr.releaseInner(&inner[int, int]{leaves: new([maxChildren]*leaf[int, int])})
			assert.Length(t, tr.freeLeafParents, 1, "a parent of leaves must count against its own free list")
		})

		t.Run("keeps a parent of internal nodes while the list of parents of leaves is full", func(t *testing.T) {
			t.Parallel()
			var tr ints
			for range maxFree {
				tr.releaseInner(&inner[int, int]{leaves: new([maxChildren]*leaf[int, int])})
			}
			tr.releaseInner(&inner[int, int]{inners: new([maxChildren]*inner[int, int])})
			assert.Length(t, tr.freeInnerParents, 1, "a parent of internal nodes must count against its own free list")
		})

		t.Run("leaves an internal node of another tree as it is", func(t *testing.T) {
			t.Parallel()
			tr := ints{owner: 1}
			in := &inner[int, int]{owner: 2, n: 1, keys: [maxItems]int{4}}
			in.leaves = &[maxChildren]*leaf[int, int]{{n: 1}, {n: 1}}
			tr.releaseInner(in)
			expect.Empty(t, tr.freeLeafParents, "a node of another tree may be part of a clone")
			expect.Equal(t, in.n, 1, "a clone's node must keep its separator")
			expect.NotNil(t, in.leaves[1], "a clone's node must keep its children")
		})
	})

	t.Run("mutLeaf", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a leaf of the tree as it is", func(t *testing.T) {
			t.Parallel()
			tr := ints{owner: 3}
			l := &leaf[int, int]{owner: 3}
			p := l
			expect.Equal(t, tr.mutLeaf(&p), l, "mutLeaf must return the leaf itself", expect.ByIdentity())
			expect.Equal(t, p, l, "mutLeaf must leave the pointer to the leaf as it is", expect.ByIdentity())
		})

		t.Run("replaces a leaf of another tree with a copy of its items", func(t *testing.T) {
			t.Parallel()
			tr := ints{owner: 3}
			l := &leaf[int, int]{owner: 4, n: 2, keys: [maxItems]int{5, 6}, vals: [maxItems]int{-5, -6}}
			p := l
			c := tr.mutLeaf(&p)
			expect.NotEqual(t, c, l, "mutLeaf must return a copy", expect.ByIdentity())
			expect.Equal(t, p, c, "the copy must replace the leaf", expect.ByIdentity())
			expect.Equal(t, c.owner, uint64(3), "the copy must have the ID of the tree")
			expect.Equal(t, c.keys, l.keys, "the copy must have the keys")
			expect.Equal(t, c.vals, l.vals, "the copy must have the values")
			expect.Equal(t, c.n, 2, "the copy must have the item count")
		})
	})

	t.Run("mutInner", func(t *testing.T) {
		t.Parallel()

		t.Run("returns an internal node of the tree as it is", func(t *testing.T) {
			t.Parallel()
			tr := ints{owner: 3}
			in := &inner[int, int]{owner: 3}
			p := in
			expect.Equal(t, tr.mutInner(&p), in, "mutInner must return the node itself", expect.ByIdentity())
			expect.Equal(t, p, in, "mutInner must leave the pointer to the node as it is", expect.ByIdentity())
		})

		t.Run("replaces a parent of leaves of another tree with a copy", func(t *testing.T) {
			t.Parallel()
			tr := ints{owner: 3}
			a, b := &leaf[int, int]{n: 1}, &leaf[int, int]{n: 1}
			in := &inner[int, int]{owner: 4, n: 1, count: 2, keys: [maxItems]int{8}}
			in.leaves = &[maxChildren]*leaf[int, int]{a, b}
			p := in
			c := tr.mutInner(&p)
			expect.NotEqual(t, c, in, "mutInner must return a copy", expect.ByIdentity())
			expect.Equal(t, p, c, "the copy must replace the node", expect.ByIdentity())
			expect.Equal(t, c.owner, uint64(3), "the copy must have the ID of the tree")
			expect.Equal(t, c.n, 1, "the copy must have the separator count")
			expect.Equal(t, c.count, 2, "the copy must have the item count")
			expect.Equal(t, c.keys, in.keys, "the copy must have the separators")
			expect.Nil(t, c.inners, "the copy must have no array of internal nodes")
			expect.Equal(t, c.leaves[0], a, "the copy must share the first leaf", expect.ByIdentity())
			expect.Equal(t, c.leaves[1], b, "the copy must share the second leaf", expect.ByIdentity())
		})

		t.Run("replaces a parent of internal nodes of another tree with a copy", func(t *testing.T) {
			t.Parallel()
			tr := ints{owner: 3}
			a, b := &inner[int, int]{count: 2}, &inner[int, int]{count: 2}
			in := &inner[int, int]{owner: 4, n: 1, count: 4, keys: [maxItems]int{8}}
			in.inners = &[maxChildren]*inner[int, int]{a, b}
			p := in
			c := tr.mutInner(&p)
			expect.NotEqual(t, c, in, "mutInner must return a copy", expect.ByIdentity())
			expect.Equal(t, p, c, "the copy must replace the node", expect.ByIdentity())
			expect.Equal(t, c.owner, uint64(3), "the copy must have the ID of the tree")
			expect.Equal(t, c.n, 1, "the copy must have the separator count")
			expect.Equal(t, c.count, 4, "the copy must have the item count")
			expect.Equal(t, c.keys, in.keys, "the copy must have the separators")
			expect.Nil(t, c.leaves, "the copy must have no array of leaves")
			expect.Equal(t, c.inners[0], a, "the copy must share the first node", expect.ByIdentity())
			expect.Equal(t, c.inners[1], b, "the copy must share the second node", expect.ByIdentity())
		})
	})
}
