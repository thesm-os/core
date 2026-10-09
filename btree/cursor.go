// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package btree

import "slices"

// cursor is a position in a tree: the item at index i of leaf, and the
// path of internal nodes from the root to the leaf. A tree write can
// change or replace the nodes of a path, so a cursor is valid only until
// the next write.
type cursor[K, V any] struct {
	leaf  *leaf[K, V]
	path  [maxDepth]step[K, V]
	depth int
	i     int
}

// nextLeaf moves c to the first item of the next leaf, and reports whether
// there is one.
func (c *cursor[K, V]) nextLeaf() bool {
	for d, s := range slices.Backward(c.path[:c.depth]) {
		if s.i < s.n.n {
			c.depth = d
			c.downFirst(s.n, s.i+1)

			return true
		}
	}

	return false
}

// prevLeaf moves c to the last item of the previous leaf, and reports
// whether there is one.
func (c *cursor[K, V]) prevLeaf() bool {
	for d, s := range slices.Backward(c.path[:c.depth]) {
		if s.i > 0 {
			c.depth = d
			c.downLast(s.n, s.i-1)

			return true
		}
	}

	return false
}

// downFirst extends the path of c from child i of in down the first child
// of every level, and moves c to the first item of the leaf it ends at.
func (c *cursor[K, V]) downFirst(in *inner[K, V], i int) {
	for {
		c.path[c.depth] = step[K, V]{in, i}
		c.depth++
		if in.leaves != nil {
			c.leaf, c.i = in.leaves[i], 0

			return
		}
		in, i = in.inners[i], 0
	}
}

// downLast extends the path of c from child i of in down the last child of
// every level, and moves c to the last item of the leaf it ends at.
func (c *cursor[K, V]) downLast(in *inner[K, V], i int) {
	for {
		c.path[c.depth] = step[K, V]{in, i}
		c.depth++
		if in.leaves != nil {
			c.leaf = in.leaves[i]
			c.i = c.leaf.n - 1

			return
		}
		in = in.inners[i]
		i = in.n
	}
}

// floor returns the largest key at or before key and its value, and
// reports whether there is one.
func (t *tree[K, V, O]) floor(key K) (K, V, bool) {
	var c cursor[K, V]
	if t.seekLE(&c, key) {
		return c.leaf.keys[c.i], c.leaf.vals[c.i], true
	}
	var zk K
	var zv V

	return zk, zv, false
}

// ceil returns the smallest key at or after key and its value, and reports
// whether there is one.
func (t *tree[K, V, O]) ceil(key K) (K, V, bool) {
	var c cursor[K, V]
	if t.seekGE(&c, key) {
		return c.leaf.keys[c.i], c.leaf.vals[c.i], true
	}
	var zk K
	var zv V

	return zk, zv, false
}

// walk calls yield for every item in ascending key order, and continues
// after the last key it yielded when the tree changes.
func (t *tree[K, V, O]) walk(yield func(K, V) bool) {
	var c cursor[K, V]
	for ok := t.first(&c); ok; {
		last, changed := t.forward(&c, nil, 0, yield)
		if !changed {
			return
		}
		ok = t.seekGT(&c, last)
	}
}

// walkBack calls yield for every item in descending key order, and
// continues before the last key it yielded when the tree changes.
func (t *tree[K, V, O]) walkBack(yield func(K, V) bool) {
	var c cursor[K, V]
	for ok := t.last(&c); ok; {
		last, changed := t.backward(&c, yield)
		if !changed {
			return
		}
		ok = t.seekLT(&c, last)
	}
}

// walkRange calls yield for the items with keys in [lo, hi) in ascending
// key order, and continues after the last key it yielded when the tree
// changes.
func (t *tree[K, V, O]) walkRange(lo, hi K, yield func(K, V) bool) {
	if !t.order.less(lo, hi) {
		return
	}
	var c, end cursor[K, V]
	for ok := t.seekGE(&c, lo); ok; {
		// The walk ends at the first key at or after hi, or at the last key
		// when there is none. That position is at or after c, because lo
		// sorts before hi.
		var endLeaf *leaf[K, V]
		if t.seekGE(&end, hi) {
			endLeaf = end.leaf
		}
		last, changed := t.forward(&c, endLeaf, end.i, yield)
		if !changed {
			return
		}
		ok = t.seekGT(&c, last)
	}
}

// walkFrom calls yield for the items with keys at or after from in
// ascending key order, and continues after the last key it yielded when
// the tree changes.
func (t *tree[K, V, O]) walkFrom(from K, yield func(K, V) bool) {
	var c cursor[K, V]
	for ok := t.seekGE(&c, from); ok; {
		last, changed := t.forward(&c, nil, 0, yield)
		if !changed {
			return
		}
		ok = t.seekGT(&c, last)
	}
}

// walkBackFrom calls yield for the items with keys at or before from in
// descending key order, and continues before the last key it yielded when
// the tree changes.
func (t *tree[K, V, O]) walkBackFrom(from K, yield func(K, V) bool) {
	var c cursor[K, V]
	for ok := t.seekLE(&c, from); ok; {
		last, changed := t.backward(&c, yield)
		if !changed {
			return
		}
		ok = t.seekLT(&c, last)
	}
}

// first moves c to the smallest key of t, and reports whether t has a key.
func (t *tree[K, V, O]) first(c *cursor[K, V]) bool {
	c.depth = 0
	if t.root != nil {
		c.downFirst(t.root, 0)

		return true
	}
	c.leaf, c.i = t.leaf, 0

	return t.leaf != nil
}

// last moves c to the largest key of t, and reports whether t has a key.
func (t *tree[K, V, O]) last(c *cursor[K, V]) bool {
	c.depth = 0
	if t.root != nil {
		c.downLast(t.root, t.root.n)

		return true
	}
	if t.leaf == nil {
		return false
	}
	c.leaf, c.i = t.leaf, t.leaf.n-1

	return true
}

// locate records in c the path from the root to the leaf where key is or
// would be, and returns the leaf, or nil for an empty tree.
func (t *tree[K, V, O]) locate(c *cursor[K, V], key K) *leaf[K, V] {
	c.depth = 0
	in := t.root
	if in == nil {
		return t.leaf
	}
	for {
		i := t.order.route(in.keys[:in.n], key)
		c.path[c.depth] = step[K, V]{in, i}
		c.depth++
		if in.leaves != nil {
			return in.leaves[i]
		}
		in = in.inners[i]
	}
}

// seekGE moves c to the first key at or after key, and reports whether
// there is one.
func (t *tree[K, V, O]) seekGE(c *cursor[K, V], key K) bool {
	l := t.locate(c, key)
	if l == nil {
		return false
	}
	c.leaf = l
	c.i, _ = t.order.search(l.keys[:l.n], key)

	return c.i < l.n || c.nextLeaf()
}

// seekGT moves c past the keys at or before key in the leaf where key is
// or would be, and reports whether t has a key. c can end past the last
// item of the leaf, where forward continues at the next leaf.
func (t *tree[K, V, O]) seekGT(c *cursor[K, V], key K) bool {
	l := t.locate(c, key)
	if l == nil {
		return false
	}
	c.leaf, c.i = l, t.order.route(l.keys[:l.n], key)

	return true
}

// seekLE moves c to the last key at or before key, and reports whether
// there is one.
func (t *tree[K, V, O]) seekLE(c *cursor[K, V], key K) bool {
	l := t.locate(c, key)
	if l == nil {
		return false
	}
	i, found := t.order.search(l.keys[:l.n], key)
	if !found {
		i--
	}
	c.leaf, c.i = l, i

	// Every separator is the first key of the subtree after it, so the
	// leaf where key would be has a key at or before key unless it is the
	// first leaf.
	return i >= 0
}

// seekLT moves c to the last key before key in the leaf where key is or
// would be, and reports whether t has a key. c can end before the first
// item of the leaf, where backward continues at the previous leaf.
func (t *tree[K, V, O]) seekLT(c *cursor[K, V], key K) bool {
	l := t.locate(c, key)
	if l == nil {
		return false
	}
	i, _ := t.order.search(l.keys[:l.n], key)
	c.leaf, c.i = l, i-1

	return true
}

// forward calls yield for the items from c in ascending key order. It
// stops before index end of leaf endLeaf, or at the last item when
// endLeaf is nil. It returns the last key it yielded, and reports whether
// it stopped because t changed after that yield, so the caller continues
// after the key.
func (t *tree[K, V, O]) forward(c *cursor[K, V], endLeaf *leaf[K, V], end int, yield func(K, V) bool) (K, bool) {
	writes := t.writes
	for {
		l := c.leaf
		n := l.n
		if l == endLeaf {
			n = end
		}
		for i := c.i; i < n; i++ {
			// A write in yield can shift the keys of l, so the key is
			// read before the call.
			k := l.keys[i]
			if !yield(k, l.vals[i]) {
				return k, false
			}
			if t.writes != writes {
				return k, true
			}
		}
		if l == endLeaf || !c.nextLeaf() {
			var zero K

			return zero, false
		}
	}
}

// backward calls yield for the items from c in descending key order, down
// to the smallest key. It returns the last key it yielded, and reports
// whether it stopped because t changed after that yield, so the caller
// continues before the key.
func (t *tree[K, V, O]) backward(c *cursor[K, V], yield func(K, V) bool) (K, bool) {
	writes := t.writes
	for {
		l := c.leaf
		for i := c.i; i >= 0; i-- {
			k := l.keys[i]
			if !yield(k, l.vals[i]) {
				return k, false
			}
			if t.writes != writes {
				return k, true
			}
		}
		if !c.prevLeaf() {
			var zero K

			return zero, false
		}
	}
}
