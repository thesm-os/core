// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package btree

// leaf contains n items in key order: the keys in keys and their values at
// the same indexes of vals. Every slot at index n or above contains the
// zero key and value, so a removed item is unreachable from its old leaf.
//
// With no pointer field, a leaf of keys and values without pointers is an
// object that the garbage collector marks without scanning.
type leaf[K, V any] struct {
	keys  [maxItems]K
	vals  [maxItems]V
	owner uint64 // the ID of the tree that may change the leaf in place
	n     int
}

// size returns the number of items in the leaf.
func (l *leaf[K, V]) size() int {
	return l.n
}

// insert inserts key and val at index i of l, which has room for them.
func (l *leaf[K, V]) insert(i int, key K, val V) {
	copy(l.keys[i+1:l.n+1], l.keys[i:l.n])
	copy(l.vals[i+1:l.n+1], l.vals[i:l.n])
	l.keys[i], l.vals[i] = key, val
	l.n++
}

// inner routes a search to one of its n+1 children. Every key under child
// i sorts before keys[i], and every key under child i+1 sorts at or after
// keys[i]. Exactly one of leaves and inners is set: leaves in the level
// above the leaves, inners in every level above that. Every separator and
// child slot past the used ones is zero.
//
// The child arrays come first and the keys next, so the collector scans
// the child arrays of an internal node whose keys contain no pointers, and
// nothing after the keys.
type inner[K, V any] struct {
	leaves *[maxChildren]*leaf[K, V]
	inners *[maxChildren]*inner[K, V]
	keys   [maxItems]K
	count  int    // items in the subtree
	owner  uint64 // the ID of the tree that may change the node in place
	n      int    // separators; the node has n+1 children
}

// size returns the number of items under the internal node.
func (in *inner[K, V]) size() int {
	return in.count
}

// sized is a child of an internal node: a leaf or an internal node.
type sized interface {
	// size returns the number of items under the child.
	size() int
}

// step is an internal node of a path and the index of the child that the
// path continues through.
type step[K, V any] struct {
	n *inner[K, V]
	i int
}

// total returns the number of items under the children in kids.
func total[C sized](kids []C) int {
	n := 0
	for _, c := range kids {
		n += c.size()
	}

	return n
}

// take removes the last node of the free list at list and returns it, or
// returns nil when the list is empty. It zeroes the slot that the node
// leaves, so the list does not keep the node reachable.
func take[N any](list *[]*N) *N {
	k := len(*list) - 1
	if k < 0 {
		return nil
	}
	n := (*list)[k]
	(*list)[k] = nil
	*list = (*list)[:k]

	return n
}

// trim returns the first maxFree nodes of the free list list, and zeroes
// the slots past them, so the nodes it drops become garbage.
func trim[N any](list []*N) []*N {
	n := min(len(list), maxFree)
	clear(list[n:])

	return list[:n]
}

// newLeaf returns an empty leaf with the ID of t, from the free list when
// the list has one.
func (t *tree[K, V, O]) newLeaf() *leaf[K, V] {
	if l := take(&t.freeLeaves); l != nil {
		l.owner = t.owner

		return l
	}

	return &leaf[K, V]{owner: t.owner}
}

// newLeafParent returns an empty internal node with the ID of t and an
// array of leaves, from its free list when the list has one.
func (t *tree[K, V, O]) newLeafParent() *inner[K, V] {
	if in := take(&t.freeLeafParents); in != nil {
		in.owner = t.owner

		return in
	}

	return &inner[K, V]{leaves: new([maxChildren]*leaf[K, V]), owner: t.owner}
}

// newInnerParent returns an empty internal node with the ID of t and an
// array of internal nodes, from its free list when the list has one.
func (t *tree[K, V, O]) newInnerParent() *inner[K, V] {
	if in := take(&t.freeInnerParents); in != nil {
		in.owner = t.owner

		return in
	}

	return &inner[K, V]{inners: new([maxChildren]*inner[K, V]), owner: t.owner}
}

// newSibling returns an empty internal node with the ID of t, for children
// of the kind that the children of in are.
func (t *tree[K, V, O]) newSibling(in *inner[K, V]) *inner[K, V] {
	if in.leaves != nil {
		return t.newLeafParent()
	}

	return t.newInnerParent()
}

// releaseLeaf keeps a leaf that the tree dropped, in a merge or when the
// root leaf lost its last item, when the leaf has the ID of t and the free
// list has fewer than maxFree leaves. A leaf with another ID may still be
// part of a clone, so releaseLeaf leaves it as it is.
func (t *tree[K, V, O]) releaseLeaf(l *leaf[K, V]) {
	if l.owner == t.owner && len(t.freeLeaves) < maxFree {
		t.keepLeaf(l)
	}
}

// releaseInner keeps an internal node that a merge or the shrink of the
// root removed from the tree when it has the ID of t and the free list of
// its kind has fewer than maxFree nodes.
func (t *tree[K, V, O]) releaseInner(in *inner[K, V]) {
	if in.owner != t.owner {
		return
	}
	free := t.freeInnerParents
	if in.leaves != nil {
		free = t.freeLeafParents
	}
	if len(free) < maxFree {
		t.keepInner(in)
	}
}

// keepLeaf zeroes the items of l and puts it on the free list.
func (t *tree[K, V, O]) keepLeaf(l *leaf[K, V]) {
	clear(l.keys[:l.n])
	clear(l.vals[:l.n])
	l.n, l.owner = 0, 0
	t.freeLeaves = append(t.freeLeaves, l)
}

// keepInner zeroes the separators and children of in, and puts it with
// its child array on the free list of its kind.
func (t *tree[K, V, O]) keepInner(in *inner[K, V]) {
	clear(in.keys[:in.n])
	if in.leaves != nil {
		clear(in.leaves[:in.n+1])
		t.freeLeafParents = append(t.freeLeafParents, in)
	} else {
		clear(in.inners[:in.n+1])
		t.freeInnerParents = append(t.freeInnerParents, in)
	}
	in.n, in.count, in.owner = 0, 0, 0
}

// mutLeaf returns the leaf that *p points to, after replacing it with a
// copy with the ID of t when it has another ID. The copy has only the
// occupied slots of the leaf written.
func (t *tree[K, V, O]) mutLeaf(p **leaf[K, V]) *leaf[K, V] {
	l := *p
	if l.owner == t.owner {
		return l
	}
	c := t.newLeaf()
	c.n = l.n
	copy(c.keys[:], l.keys[:l.n])
	copy(c.vals[:], l.vals[:l.n])
	*p = c

	return c
}

// mutInner returns the internal node that *p points to, after replacing it
// with a copy with the ID of t when it has another ID.
func (t *tree[K, V, O]) mutInner(p **inner[K, V]) *inner[K, V] {
	in := *p
	if in.owner == t.owner {
		return in
	}
	c := t.newSibling(in)
	c.n, c.count = in.n, in.count
	copy(c.keys[:], in.keys[:in.n])
	if in.leaves != nil {
		copy(c.leaves[:], in.leaves[:in.n+1])
	} else {
		copy(c.inners[:], in.inners[:in.n+1])
	}
	*p = c

	return c
}
