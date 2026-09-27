// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package btree

// atRightEdge reports whether every step of path takes the last child, so
// the path ends at the rightmost leaf.
func atRightEdge[K, V any](path []step[K, V]) bool {
	for _, s := range path {
		if s.i != s.n.n {
			return false
		}
	}

	return true
}

// insertSeparator inserts separator key at index i of in, which has room
// for it, and child c after it in kids, the child array of in.
func insertSeparator[K, V any, C sized](in *inner[K, V], kids *[maxChildren]C, i int, key K, c C) {
	copy(in.keys[i+1:in.n+1], in.keys[i:in.n])
	in.keys[i] = key
	copy(kids[i+2:in.n+2], kids[i+1:in.n+1])
	kids[i+1] = c
	in.n++
}

// splitInner moves the upper separators and children of the full internal
// node p to the empty node r, inserts separator sep at index i and child c
// after it, recounts both nodes, and returns the separator that moves up to
// the parent. pk and rk are the child arrays of p and r. p ends with mid
// separators and r with the rest but one.
func splitInner[K, V any, C sized](p, r *inner[K, V], pk, rk *[maxChildren]C, i, mid int, sep K, c C) K {
	up := sep
	if i < mid {
		up = p.keys[mid-1]
		r.n = copy(r.keys[:], p.keys[mid:])
		copy(rk[:], pk[mid:])
		clear(p.keys[mid-1:])
		clear(pk[mid:])
		p.n = mid - 1
		insertSeparator(p, pk, i, sep, c)
	} else if i == mid {
		r.n = copy(r.keys[:], p.keys[mid:])
		rk[0] = c
		copy(rk[1:], pk[mid+1:])
		clear(p.keys[mid:])
		clear(pk[mid+1:])
		p.n = mid
	} else {
		up = p.keys[mid]
		r.n = copy(r.keys[:], p.keys[mid+1:])
		copy(rk[:], pk[mid+1:])
		clear(p.keys[mid:])
		clear(pk[mid+1:])
		p.n = mid
		insertSeparator(r, rk, i-mid-1, sep, c)
	}
	p.count = total(pk[:p.n+1])
	r.count = total(rk[:r.n+1])

	return up
}

// set sets the value of key, and returns the previous value and whether
// key was present. When the tree has a key equal to key, set replaces its
// value and keeps the stored key.
func (t *tree[K, V, O]) set(key K, val V) (V, bool) {
	t.writes++
	var zero V
	if t.len == 0 {
		t.start(key, val)

		return zero, false
	}
	var path [maxDepth]step[K, V]
	depth, l, i, found := t.writePath(&path, key)
	if found {
		old := l.vals[i]
		l.vals[i] = val

		return old, true
	}
	t.insert(path[:depth], l, i, key, val)

	return zero, false
}

// update sets the value of key to fn(old, exists), where old is the value
// of key and exists reports whether key is present, and returns the new
// value. A write by fn can replace the nodes of the path that update holds,
// so update then stores the new value with set.
func (t *tree[K, V, O]) update(key K, fn func(old V, exists bool) V) V {
	if t.len == 0 {
		var zero V
		v := fn(zero, false)
		t.set(key, v)

		return v
	}
	t.writes++
	writes := t.writes
	var path [maxDepth]step[K, V]
	depth, l, i, found := t.writePath(&path, key)
	var old V
	if found {
		old = l.vals[i]
	}
	v := fn(old, found)
	if t.writes != writes {
		t.set(key, v)

		return v
	}
	if found {
		l.vals[i] = v
	} else {
		t.insert(path[:depth], l, i, key, v)
	}

	return v
}

// start makes key and val the only item of the empty tree t.
func (t *tree[K, V, O]) start(key K, val V) {
	l := t.newLeaf()
	l.keys[0], l.vals[0], l.n = key, val, 1
	t.leaf, t.len = l, 1
}

// insert inserts key and val at index i of leaf l, at the end of path,
// splits every full node on the way up, and makes a new root when the root
// splits. Every node on path and l must have the ID of t.
//
// A split away from the right edge leaves splitAt items in each half. An
// insert that appends at the right edge of the tree keeps all maxItems
// items of a full leaf, and maxItems-1 separators of a full internal node,
// in the node it splits, because keys that arrive in order never return
// to that node.
func (t *tree[K, V, O]) insert(path []step[K, V], l *leaf[K, V], i int, key K, val V) {
	t.len++
	for _, s := range path {
		s.n.count++
	}
	if l.n < maxItems {
		l.insert(i, key, val)

		return
	}
	leafAt, innerAt := splitAt, splitAt
	if i == maxItems && atRightEdge(path) {
		leafAt, innerAt = maxItems, maxItems-1
	}
	r := t.newLeaf()
	l.split(r, i, leafAt, key, val)
	sep := r.keys[0]
	d := len(path) - 1
	if d < 0 {
		root := t.newLeafParent()
		root.keys[0], root.n = sep, 1
		root.leaves[0], root.leaves[1] = l, r
		root.count = l.n + r.n
		t.root, t.leaf = root, nil

		return
	}
	p, pi := path[d].n, path[d].i
	if p.n < maxItems {
		insertSeparator(p, p.leaves, pi, sep, r)

		return
	}
	right := t.newLeafParent()
	sep = splitInner(p, right, p.leaves, right.leaves, pi, innerAt, sep, r)
	for d--; d >= 0; d-- {
		p, pi = path[d].n, path[d].i
		if p.n < maxItems {
			insertSeparator(p, p.inners, pi, sep, right)

			return
		}
		next := t.newInnerParent()
		sep = splitInner(p, next, p.inners, next.inners, pi, innerAt, sep, right)
		right = next
	}
	root := t.newInnerParent()
	root.keys[0], root.n = sep, 1
	root.inners[0], root.inners[1] = t.root, right
	root.count = t.root.count + right.count
	t.root = root
}

// split moves the items of the full leaf l from index mid on to the empty
// leaf r, and inserts key and val at index i of the items of both. l ends
// with mid items and r with the rest.
func (l *leaf[K, V]) split(r *leaf[K, V], i, mid int, key K, val V) {
	if i < mid {
		r.n = copy(r.keys[:], l.keys[mid-1:])
		copy(r.vals[:], l.vals[mid-1:])
		clear(l.keys[mid-1:])
		clear(l.vals[mid-1:])
		l.n = mid - 1
		l.insert(i, key, val)

		return
	}

	r.n = copy(r.keys[:], l.keys[mid:])
	copy(r.vals[:], l.vals[mid:])
	clear(l.keys[mid:])
	clear(l.vals[mid:])
	l.n = mid
	r.insert(i-mid, key, val)
}
