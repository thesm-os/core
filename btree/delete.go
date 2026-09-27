// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package btree

import "slices"

// nextSeparator returns the separator before the leaf that follows the
// leaf at the end of path: the separator after the child of the deepest
// step that has a later child. Every key of the following leaf sorts at or
// after it. It reports whether path has such a step.
func nextSeparator[K, V any](path []step[K, V]) (K, bool) {
	for _, s := range slices.Backward(path) {
		if s.i < s.n.n {
			return s.n.keys[s.i], true
		}
	}
	var zero K

	return zero, false
}

// removeSeparator removes the separator at index i of in and the child
// after it in kids, the child array of in, and zeroes the slots that the
// last separator and the last child leave.
func removeSeparator[K, V any, C sized](in *inner[K, V], kids *[maxChildren]C, i int) {
	copy(in.keys[i:], in.keys[i+1:in.n])
	copy(kids[i+1:], kids[i+2:in.n+1])
	in.n--
	clear(in.keys[in.n : in.n+1])
	clear(kids[in.n+1 : in.n+2])
}

// rotateRight moves the last child of left to the front of right, its
// right sibling under separator j of p. The separator moves down to right,
// and the last separator of left moves up in its place. lk and rk are the
// child arrays of left and right.
func rotateRight[K, V any, C sized](p *inner[K, V], j int, left, right *inner[K, V], lk, rk *[maxChildren]C) {
	moved := lk[left.n]
	var zero C
	lk[left.n] = zero
	copy(rk[1:right.n+2], rk[:right.n+1])
	rk[0] = moved
	copy(right.keys[1:right.n+1], right.keys[:right.n])
	right.keys[0] = p.keys[j]
	right.n++
	p.keys[j] = left.keys[left.n-1]
	left.n--
	clear(left.keys[left.n : left.n+1])
	right.count += moved.size()
	left.count -= moved.size()
}

// rotateLeft moves the first child of right to the end of left, its left
// sibling under separator j of p. The separator moves down to left, and
// the first separator of right moves up in its place. lk and rk are the
// child arrays of left and right.
func rotateLeft[K, V any, C sized](p *inner[K, V], j int, left, right *inner[K, V], lk, rk *[maxChildren]C) {
	moved := rk[0]
	copy(rk[:], rk[1:right.n+1])
	var zero C
	rk[right.n] = zero
	lk[left.n+1] = moved
	left.keys[left.n] = p.keys[j]
	left.n++
	p.keys[j] = right.keys[0]
	copy(right.keys[:], right.keys[1:right.n])
	right.n--
	clear(right.keys[right.n : right.n+1])
	left.count += moved.size()
	right.count -= moved.size()
}

// merge appends separator j of p and the separators and children of right
// to left, its left sibling. The caller removes the separator and right
// from p. lk and rk are the child arrays of left and right.
func merge[K, V any, C sized](p *inner[K, V], j int, left, right *inner[K, V], lk, rk *[maxChildren]C) {
	copy(lk[left.n+1:], rk[:right.n+1])
	left.keys[left.n] = p.keys[j]
	copy(left.keys[left.n+1:], right.keys[:right.n])
	left.n += right.n + 1
	left.count += right.count
}

// delete removes key, and returns its value and whether key was present.
func (t *tree[K, V, O]) delete(key K) (V, bool) {
	t.writes++
	var zero V
	if t.len == 0 {
		return zero, false
	}
	var path [maxDepth]step[K, V]
	depth, l, i, found := t.writePath(&path, key)
	if !found {
		return zero, false
	}
	old := l.vals[i]
	t.remove(path[:depth], l, i)

	return old, true
}

// deleteRange removes every key in [lo, hi), and returns the number of
// keys it removed. It removes nothing when hi sorts at or before lo. It
// descends once for each key it removes, and once more each time the next
// key of the range starts a new leaf.
func (t *tree[K, V, O]) deleteRange(lo, hi K) int {
	t.writes++
	removed := 0
	if !t.order.less(lo, hi) {
		return removed
	}
	for t.len > 0 {
		var path [maxDepth]step[K, V]
		depth, l, i, _ := t.writePath(&path, lo)
		if i == l.n {
			// Every key of the leaf sorts before lo, so the next key at or
			// after lo is the first key of the next leaf, at or after the
			// separator before that leaf.
			sep, ok := nextSeparator(path[:depth])
			if !ok {
				break
			}
			lo = sep

			continue
		}
		if !t.order.less(l.keys[i], hi) {
			break
		}
		t.remove(path[:depth], l, i)
		removed++
	}

	return removed
}

// popMin removes the smallest key, and returns it with its value and
// whether the tree had a key.
func (t *tree[K, V, O]) popMin() (K, V, bool) {
	t.writes++
	if t.len == 0 {
		var zk K
		var zv V

		return zk, zv, false
	}
	var path [maxDepth]step[K, V]
	depth := 0
	var l *leaf[K, V]
	if t.root == nil {
		l = t.mutLeaf(&t.leaf)
	} else {
		in := t.mutInner(&t.root)
		for {
			path[depth] = step[K, V]{in, 0}
			depth++
			if in.leaves != nil {
				l = t.mutLeaf(&in.leaves[0])

				break
			}
			in = t.mutInner(&in.inners[0])
		}
	}
	k, v := l.keys[0], l.vals[0]
	t.remove(path[:depth], l, 0)

	return k, v, true
}

// popMax removes the largest key, and returns it with its value and
// whether the tree had a key.
func (t *tree[K, V, O]) popMax() (K, V, bool) {
	t.writes++
	if t.len == 0 {
		var zk K
		var zv V

		return zk, zv, false
	}
	var path [maxDepth]step[K, V]
	depth := 0
	var l *leaf[K, V]
	if t.root == nil {
		l = t.mutLeaf(&t.leaf)
	} else {
		in := t.mutInner(&t.root)
		for {
			path[depth] = step[K, V]{in, in.n}
			depth++
			if in.leaves != nil {
				l = t.mutLeaf(&in.leaves[in.n])

				break
			}
			in = t.mutInner(&in.inners[in.n])
		}
	}
	last := l.n - 1
	k, v := l.keys[last], l.vals[last]
	t.remove(path[:depth], l, last)

	return k, v, true
}

// clear removes every item. The free lists keep their nodes. The tree drops
// its other nodes, which become garbage unless a clone shares them.
func (t *tree[K, V, O]) clear() {
	t.writes++
	t.root, t.leaf, t.len = nil, nil, 0
}

// remove removes the item at index i of leaf l, at the end of path, and
// refills or merges every node that the removal leaves underfull. Every
// node on path and l must have the ID of t.
func (t *tree[K, V, O]) remove(path []step[K, V], l *leaf[K, V], i int) {
	l.remove(i)
	t.len--
	d := len(path) - 1
	if d < 0 {
		if l.n == 0 {
			t.releaseLeaf(l)
			t.leaf = nil
		}

		return
	}
	for _, s := range path {
		s.n.count--
	}
	if l.n < minItems {
		t.fixLeaf(path[d].n, path[d].i)
	}
	for d--; d >= 0 && path[d+1].n.n < minItems; d-- {
		t.fixInner(path[d].n, path[d].i)
	}
	if r := t.root; r.n == 0 {
		if r.leaves != nil {
			t.leaf, t.root = r.leaves[0], nil
		} else {
			t.root = r.inners[0]
		}
		t.releaseInner(r)
	}
}

// fixLeaf refills the underfull leaf at index ci of p with an item of a
// sibling that has more than minItems, the left sibling first, or merges
// it with a sibling. It copies a sibling with another ID only when it
// changes the sibling.
func (t *tree[K, V, O]) fixLeaf(p *inner[K, V], ci int) {
	c := p.leaves[ci]
	if ci > 0 && p.leaves[ci-1].n > minItems {
		left := t.mutLeaf(&p.leaves[ci-1])
		c.insert(0, left.keys[left.n-1], left.vals[left.n-1])
		left.remove(left.n - 1)
		p.keys[ci-1] = c.keys[0]

		return
	}
	if ci < p.n && p.leaves[ci+1].n > minItems {
		right := t.mutLeaf(&p.leaves[ci+1])
		c.insert(c.n, right.keys[0], right.vals[0])
		right.remove(0)
		p.keys[ci] = right.keys[0]

		return
	}
	if ci > 0 {
		ci--
	}
	left := t.mutLeaf(&p.leaves[ci])
	right := p.leaves[ci+1]
	copy(left.keys[left.n:], right.keys[:right.n])
	copy(left.vals[left.n:], right.vals[:right.n])
	left.n += right.n
	removeSeparator(p, p.leaves, ci)
	t.releaseLeaf(right)
}

// fixInner refills the underfull internal node at index ci of p with a
// child of a sibling that has more than minItems separators, the left
// sibling first, or merges it with a sibling. It copies a sibling with
// another ID only when it changes the sibling.
func (t *tree[K, V, O]) fixInner(p *inner[K, V], ci int) {
	c := p.inners[ci]
	if ci > 0 && p.inners[ci-1].n > minItems {
		left := t.mutInner(&p.inners[ci-1])
		if c.leaves != nil {
			rotateRight(p, ci-1, left, c, left.leaves, c.leaves)
		} else {
			rotateRight(p, ci-1, left, c, left.inners, c.inners)
		}

		return
	}
	if ci < p.n && p.inners[ci+1].n > minItems {
		right := t.mutInner(&p.inners[ci+1])
		if c.leaves != nil {
			rotateLeft(p, ci, c, right, c.leaves, right.leaves)
		} else {
			rotateLeft(p, ci, c, right, c.inners, right.inners)
		}

		return
	}
	if ci > 0 {
		ci--
	}
	left := t.mutInner(&p.inners[ci])
	right := p.inners[ci+1]
	if left.leaves != nil {
		merge(p, ci, left, right, left.leaves, right.leaves)
	} else {
		merge(p, ci, left, right, left.inners, right.inners)
	}
	removeSeparator(p, p.inners, ci)
	t.releaseInner(right)
}

// remove removes the item at index i of l, and zeroes the slot that the
// last item leaves.
func (l *leaf[K, V]) remove(i int) {
	copy(l.keys[i:], l.keys[i+1:l.n])
	copy(l.vals[i:], l.vals[i+1:l.n])
	l.n--
	clear(l.keys[l.n : l.n+1])
	clear(l.vals[l.n : l.n+1])
}
