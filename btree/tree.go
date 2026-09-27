// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package btree

import "sync/atomic"

// Node capacities and the bounds that follow from them.
const (
	// maxItems is the capacity of a leaf in items and of an internal node
	// in separators.
	maxItems = 63

	// maxChildren is the capacity of an internal node in children.
	maxChildren = 64

	// minItems is the fewest items in a leaf, and the fewest separators in
	// an internal node, other than the root and the nodes at the right edge
	// of the tree.
	minItems = 31

	// splitAt is the number of items that a split away from the right edge
	// keeps in the node it splits: half of a full node and the new item.
	splitAt = 32

	// maxFree is the capacity of each of a tree's two free lists.
	maxFree = 64

	// maxDepth is the capacity of a path in internal nodes. A node other
	// than the root and the right edge has at least 32 children, so a tree
	// of fewer than 2^63 keys has at most 12 internal levels.
	maxDepth = 16
)

// owners issues the IDs that a clone gives to trees. The IDs start at 1.
// A tree that was never cloned has ID 0. A node records the ID of the tree
// that may change it in place. A tree copies a node with another ID before
// it changes that node. A clone gives both trees new IDs, so a shared node
// has the ID of neither tree. Trees with ID 0 do not share nodes, because
// only a clone shares nodes.
var owners atomic.Uint64

// order is the order of the keys of a tree. Map and Set order their keys
// with natural, and MapFunc with custom. A descent calls its order once
// for each node that it visits: route in an internal node and search in
// a leaf. The order compares keys inside the node.
type order[K any] interface {
	// search returns the index of the first of keys at or after key, and
	// reports whether that key equals key. keys must be in order.
	search(keys []K, key K) (int, bool)

	// route returns the number of keys at or before key. For the
	// separators of an internal node, that is the index of the child whose
	// subtree can contain key. keys must be in order.
	route(keys []K, key K) int

	// less reports whether a sorts before b.
	less(a, b K) bool
}

// tree is a B+ tree of keys of type K in order O, with values of type V.
// Map, MapFunc and Set keep their items in a tree. A tree is empty exactly
// when root and leaf are both nil, and at most one of them is set. The
// zero tree is an empty tree, ready to use when the zero O is an order.
type tree[K, V any, O order[K]] struct {
	order      O
	root       *inner[K, V] // the root when the tree has internal nodes
	leaf       *leaf[K, V]  // the root when the tree is one leaf
	freeLeaves []*leaf[K, V]
	freeInners []*inner[K, V]
	owner      uint64 // the ID of the nodes the tree may change in place
	len        int
	writes     uint64 // incremented by every write, for iterators
}

// get returns the value of key, and reports whether key is present.
func (t *tree[K, V, O]) get(key K) (V, bool) {
	l := t.leaf
	if in := t.root; in != nil {
		for in.leaves == nil {
			in = in.inners[t.order.route(in.keys[:in.n], key)]
		}
		l = in.leaves[t.order.route(in.keys[:in.n], key)]
	}
	if l != nil {
		if i, found := t.order.search(l.keys[:l.n], key); found {
			return l.vals[i], true
		}
	}
	var zero V

	return zero, false
}

// rank returns the number of keys that sort before key. It adds the item
// counts of the children before the path at each level.
func (t *tree[K, V, O]) rank(key K) int {
	rank := 0
	l := t.leaf
	if in := t.root; in != nil {
		for in.leaves == nil {
			i := t.order.route(in.keys[:in.n], key)
			rank += total(in.inners[:i])
			in = in.inners[i]
		}
		i := t.order.route(in.keys[:in.n], key)
		rank += total(in.leaves[:i])
		l = in.leaves[i]
	}
	if l != nil {
		i, _ := t.order.search(l.keys[:l.n], key)
		rank += i
	}

	return rank
}

// at returns the key at index i in key order and its value, and reports
// whether 0 <= i < t.len. It subtracts the item counts of the children
// before the path at each level.
func (t *tree[K, V, O]) at(i int) (K, V, bool) {
	if i < 0 || i >= t.len {
		var zk K
		var zv V

		return zk, zv, false
	}
	l := t.leaf
	if in := t.root; in != nil {
		for in.leaves == nil {
			j := 0
			for ; i >= in.inners[j].count; j++ {
				i -= in.inners[j].count
			}
			in = in.inners[j]
		}
		j := 0
		for ; i >= in.leaves[j].n; j++ {
			i -= in.leaves[j].n
		}
		l = in.leaves[j]
	}

	return l.keys[i], l.vals[i], true
}

// min returns the smallest key and its value, and reports whether the tree
// has a key.
func (t *tree[K, V, O]) min() (K, V, bool) {
	l := t.leaf
	if in := t.root; in != nil {
		for in.leaves == nil {
			in = in.inners[0]
		}
		l = in.leaves[0]
	}
	if l == nil {
		var zk K
		var zv V

		return zk, zv, false
	}

	return l.keys[0], l.vals[0], true
}

// max returns the largest key and its value, and reports whether the tree
// has a key.
func (t *tree[K, V, O]) max() (K, V, bool) {
	l := t.leaf
	if in := t.root; in != nil {
		for in.leaves == nil {
			in = in.inners[in.n]
		}
		l = in.leaves[in.n]
	}
	if l == nil {
		var zk K
		var zv V

		return zk, zv, false
	}

	return l.keys[l.n-1], l.vals[l.n-1], true
}

// clone gives t a new ID and returns a tree with the order of t, another
// new ID and every node of t. A shared node then has the ID of neither
// tree, so each tree copies a node before its first change to it. The free
// lists stay with t.
func (t *tree[K, V, O]) clone() tree[K, V, O] {
	t.owner = owners.Add(1)

	return tree[K, V, O]{order: t.order, root: t.root, leaf: t.leaf, len: t.len, owner: owners.Add(1)}
}

// writePath records in path the internal nodes from the root to the leaf
// where key is or would be, after replacing every node on the way that has
// another tree's ID with a copy. It returns the number of nodes on path,
// the leaf, the index of the first key in it at or after key, and whether
// that key equals key. The tree must not be empty.
func (t *tree[K, V, O]) writePath(path *[maxDepth]step[K, V], key K) (int, *leaf[K, V], int, bool) {
	depth := 0
	var l *leaf[K, V]
	if t.root == nil {
		l = t.mutLeaf(&t.leaf)
	} else {
		in := t.mutInner(&t.root)
		for {
			i := t.order.route(in.keys[:in.n], key)
			path[depth] = step[K, V]{in, i}
			depth++
			if in.leaves != nil {
				l = t.mutLeaf(&in.leaves[i])

				break
			}
			in = t.mutInner(&in.inners[i])
		}
	}
	i, found := t.order.search(l.keys[:l.n], key)

	return depth, l, i, found
}
