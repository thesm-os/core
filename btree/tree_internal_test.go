// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package btree

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"testing"

	"go.thesmos.sh/testkit"
)

// Sizes of the model tests.
const (
	// operations is the number of random operations that TestTreeModel
	// applies to each tree.
	operations = 1_000_000

	// keySpace is the number of keys that the operations draw from. The
	// model test inserts three quarters of them first, which gives every
	// tree two levels of internal nodes.
	keySpace = 6_000

	// phase is the number of operations between two switches of the mix
	// of operations, from 80% inserts to 20% and back. A tree grows to two
	// levels of internal nodes in one phase and shrinks to one in the
	// next, so its root splits and merges.
	phase = 50_000

	// cloneEvery is the number of operations between two clones. A clone
	// must keep its items through the operations until the next clone.
	cloneEvery = 1_000

	// checkEvery is the number of operations between two comparisons of a
	// tree with its model.
	checkEvery = 10_000
)

// errInvariant is the error that check returns for a broken invariant.
var errInvariant = errors.New("btree: invariant")

// ints is the tree of a Map[int, int], the tree that build returns.
type ints = tree[int, int, natural[int]]

// checker walks a tree for check.
type checker[K, V any, O order[K]] struct {
	t         *tree[K, V, O]
	leafDepth int
}

// node checks the subtree of in, or of l when in is nil, and returns its
// number of items. The keys of the subtree must sort at or after lo and
// before hi, and its first key must be lo when lo is not nil. A node on
// the right edge of the tree may have fewer than minItems items or
// separators, but not none.
func (c *checker[K, V, O]) node(in *inner[K, V], l *leaf[K, V], depth int, lo, hi *K, edge bool) (int, error) {
	if in == nil {
		if c.leafDepth == -1 {
			c.leafDepth = depth
		} else if c.leafDepth != depth {
			return 0, fmt.Errorf("%w: leaves at depths %d and %d", errInvariant, c.leafDepth, depth)
		}
		if l.n < 1 || (!edge && l.n < minItems) {
			return 0, fmt.Errorf("%w: a leaf with %d items", errInvariant, l.n)
		}
		if !zeroFrom(l.keys[:], l.n) || !zeroFrom(l.vals[:], l.n) {
			return 0, fmt.Errorf("%w: a leaf with a slot past its items that is not zero", errInvariant)
		}
		// The separator before a subtree is its first key, so it lies in the
		// leftmost leaf of the subtree.
		if lo != nil && c.t.order.less(*lo, l.keys[0]) {
			return 0, fmt.Errorf("%w: a separator that is not the first key of the subtree after it", errInvariant)
		}

		return l.n, c.order(l.keys[:l.n], lo, hi)
	}
	if in.n < 1 || (!edge && in.n < minItems) {
		return 0, fmt.Errorf("%w: an internal node with %d separators", errInvariant, in.n)
	}
	if (in.leaves == nil) == (in.inners == nil) {
		return 0, fmt.Errorf("%w: an internal node without exactly one child array", errInvariant)
	}
	if !zeroFrom(in.keys[:], in.n) ||
		(in.leaves != nil && !zeroFrom(in.leaves[:], in.n+1)) || (in.inners != nil && !zeroFrom(in.inners[:], in.n+1)) {

		return 0, fmt.Errorf("%w: an internal node with a slot past its children that is not zero", errInvariant)
	}
	if err := c.order(in.keys[:in.n], lo, hi); err != nil {
		return 0, err
	}
	sum := 0
	for j := 0; j <= in.n; j++ {
		l, h := lo, hi
		if j > 0 {
			l = &in.keys[j-1]
		}
		if j < in.n {
			h = &in.keys[j]
		}
		var n int
		var err error
		if in.leaves != nil {
			n, err = c.node(nil, in.leaves[j], depth+1, l, h, edge && j == in.n)
		} else {
			n, err = c.node(in.inners[j], nil, depth+1, l, h, edge && j == in.n)
		}
		if err != nil {
			return 0, err
		}
		sum += n
	}
	if in.count != sum {
		return 0, fmt.Errorf("%w: an internal node counts %d items and has %d", errInvariant, in.count, sum)
	}

	return sum, nil
}

// order returns an error unless keys increase strictly, sort at or after
// lo, and sort before hi.
func (c *checker[K, V, O]) order(keys []K, lo, hi *K) error {
	less := c.t.order.less
	for i := 1; i < len(keys); i++ {
		if !less(keys[i-1], keys[i]) {
			return fmt.Errorf("%w: keys out of order", errInvariant)
		}
	}
	if lo != nil && less(keys[0], *lo) {
		return fmt.Errorf("%w: a key before the separator above it", errInvariant)
	}
	if hi != nil && !less(keys[len(keys)-1], *hi) {
		return fmt.Errorf("%w: a key at or after the separator above it", errInvariant)
	}

	return nil
}

// check returns an error when t breaks an invariant of its tree: an empty
// tree without nodes, keys and separators in the order of t and between
// the separators above them, each separator the first key of the subtree
// after it, node fill between minItems and maxItems
// outside the root and the right edge, correct subtree counts and length,
// the child array of each level, every leaf at the same depth, zero slots
// past the used ones, and free nodes that are zeroed and on the free list
// of their kind.
func check[K, V any, O order[K]](t *tree[K, V, O]) error {
	for _, l := range t.freeLeaves {
		if l.n != 0 || l.owner != 0 || !zeroFrom(l.keys[:], 0) || !zeroFrom(l.vals[:], 0) {
			return fmt.Errorf("%w: a free leaf that is not zeroed", errInvariant)
		}
	}
	for _, in := range t.freeLeafParents {
		if in.inners != nil || in.leaves == nil || !zeroFrom(in.leaves[:], 0) || !zeroInner(in) {
			return fmt.Errorf("%w: a free parent of leaves that is not zeroed", errInvariant)
		}
	}
	for _, in := range t.freeInnerParents {
		if in.leaves != nil || in.inners == nil || !zeroFrom(in.inners[:], 0) || !zeroInner(in) {
			return fmt.Errorf("%w: a free parent of internal nodes that is not zeroed", errInvariant)
		}
	}
	if t.root != nil && t.leaf != nil {
		return fmt.Errorf("%w: both roots are set", errInvariant)
	}
	if t.root == nil && t.leaf == nil {
		if t.len != 0 {
			return fmt.Errorf("%w: an empty tree with length %d", errInvariant, t.len)
		}

		return nil
	}
	c := checker[K, V, O]{t: t, leafDepth: -1}
	n, err := c.node(t.root, t.leaf, 0, nil, nil, true)
	if err != nil {
		return err
	}
	if n != t.len {
		return fmt.Errorf("%w: the tree has %d items and length %d", errInvariant, n, t.len)
	}

	return nil
}

// zeroFrom reports whether every element of s from index i on is the zero
// value. It compares int elements directly and others through reflection.
func zeroFrom[T any](s []T, i int) bool {
	if xs, ok := any(s[i:]).([]int); ok {
		for _, v := range xs {
			if v != 0 {
				return false
			}
		}

		return true
	}
	for _, v := range s[i:] {
		if !reflect.ValueOf(&v).Elem().IsZero() {
			return false
		}
	}

	return true
}

// zeroInner reports whether in has no separator, count or ID.
func zeroInner[K, V any](in *inner[K, V]) bool {
	return in.n == 0 && in.count == 0 && in.owner == 0 && zeroFrom(in.keys[:], 0)
}

// requireValid fails the test when t breaks an invariant of its tree.
func requireValid[K, V any, O order[K]](tb testing.TB, t *tree[K, V, O]) {
	tb.Helper()
	testkit.NoError(tb, check(t), "the tree must keep its invariants")
}

// keysOf returns the keys of t in order, or nil when t is empty.
func keysOf[K, V any, O order[K]](t *tree[K, V, O]) []K {
	var keys []K
	for k := range t.walk {
		keys = append(keys, k)
	}

	return keys
}

// sizes returns the number of items of every leaf of t from left to right,
// and for each level of internal nodes from the root down, the number of
// separators of every node of the level.
func sizes[K, V any, O order[K]](t *tree[K, V, O]) (leaves []int, levels [][]int) {
	if t.root == nil {
		if t.leaf != nil {
			leaves = []int{t.leaf.n}
		}

		return leaves, nil
	}
	for level := []*inner[K, V]{t.root}; len(level) > 0; {
		var next []*inner[K, V]
		seps := make([]int, len(level))
		for i, in := range level {
			seps[i] = in.n
			if in.leaves != nil {
				for _, l := range in.leaves[:in.n+1] {
					leaves = append(leaves, l.n)
				}
			} else {
				next = append(next, in.inners[:in.n+1]...)
			}
		}
		levels = append(levels, seps)
		level = next
	}

	return leaves, levels
}

// reverse orders ints from largest to smallest.
func reverse(a, b int) int {
	return cmp.Compare(b, a)
}

// churn applies random operations to tr and to a Go map, its model. It
// fails the test when an operation returns other than the model predicts,
// when tr differs from its model at an interval of checkEvery operations,
// or when a clone of tr differs from the model at the time of the clone
// after the next cloneEvery operations. It also fails unless tr had one
// level of internal nodes at one of those checks and two at another.
// churn resets tr at the start of every phase of inserts after the first,
// so the tree refills from the nodes that it kept. value returns the value
// that operation op sets.
func churn[V comparable, O order[int]](t *testing.T, tr *tree[int, V, O], value func(op int) V) {
	t.Helper()
	r := testkit.SeededRand(t)
	model := map[int]V{}
	for _, k := range r.Perm(keySpace)[:keySpace*3/4] {
		tr.set(k, value(k))
		model[k] = value(k)
	}
	ahead := direction(tr)
	heights := map[int]bool{}
	var clone tree[int, V, O]
	var cloned map[int]V
	for op := range operations {
		if op > 0 && op%(2*phase) == 0 {
			tr.reset()
			clear(model)
		}
		k := r.IntN(keySpace)
		sets := 80
		if op/phase%2 == 1 {
			sets = 20
		}
		switch n := r.IntN(100); {
		case n < sets:
			old, ok := tr.set(k, value(op))
			if want, present := model[k]; ok != present || old != want {
				t.Fatalf("operation %d: set(%d) returned %v, %t and not %v, %t", op, k, old, ok, want, present)
			}
			model[k] = value(op)
		case n < sets+6:
			tr.update(k, func(old V, exists bool) V {
				if want, present := model[k]; exists != present || old != want {
					t.Fatalf("operation %d: update(%d) passed %v, %t and not %v, %t", op, k, old, exists, want, present)
				}
				return value(op)
			})
			model[k] = value(op)
		case n < 92:
			old, ok := tr.delete(k)
			if want, present := model[k]; ok != present || old != want {
				t.Fatalf("operation %d: delete(%d) returned %v, %t and not %v, %t", op, k, old, ok, want, present)
			}
			delete(model, k)
		case n < 96:
			lo, hi := k, k+ahead*r.IntN(9)
			removed := tr.deleteRange(lo, hi)
			for key := lo; key != hi; key += ahead {
				if _, ok := model[key]; ok {
					delete(model, key)
					removed--
				}
			}
			if removed != 0 {
				t.Fatalf("operation %d: deleteRange(%d, %d) miscounted by %d", op, lo, hi, removed)
			}
		case n < 98:
			pop(t, op, model, tr.popMin, tr.min, func(k, rest int) bool { return tr.order.less(k, rest) })
		default:
			pop(t, op, model, tr.popMax, tr.max, func(k, rest int) bool { return tr.order.less(rest, k) })
		}
		if op%cloneEvery == 0 {
			if cloned != nil {
				requireModel(t, &clone, cloned)
			}
			clone, cloned = tr.clone(), maps.Clone(model)
		}
		if op%checkEvery == 0 {
			requireModel(t, tr, model)
			_, levels := sizes(tr)
			heights[len(levels)] = true
		}
	}
	requireModel(t, tr, model)
	testkit.True(t, heights[1] && heights[2], "the tree must have had one level of internal nodes and two")
}

// pop calls remove, which is popMin or popMax of a tree, and removes the
// key k that it returns from model. It fails the test unless the item was
// in model, and outside(k, rest) holds for the key rest that peek, min or
// max of the tree, then returns.
func pop[V comparable](t *testing.T, op int, model map[int]V,
	remove, peek func() (int, V, bool), outside func(k, rest int) bool,
) {
	t.Helper()
	k, v, ok := remove()
	if !ok {
		if len(model) != 0 {
			t.Fatalf("operation %d: a pop found no key in a tree of %d", op, len(model))
		}

		return
	}
	if want, present := model[k]; !present || v != want {
		t.Fatalf("operation %d: a pop returned %d, %v, which the tree did not hold", op, k, v)
	}
	delete(model, k)
	if rest, _, ok := peek(); ok && !outside(k, rest) {
		t.Fatalf("operation %d: a pop removed %d, and the tree still holds %d beyond it", op, k, rest)
	}
}

// replay applies the operations that ops encodes to tr and to a Go map,
// and fails the test when tr breaks an invariant after an operation or
// differs from the map at the end.
func replay[O order[int]](t *testing.T, tr *tree[int, int, O], ops []byte) {
	t.Helper()
	model := map[int]int{}
	ahead := direction(tr)
	for i := 0; i+2 < len(ops); i += 3 {
		k := int(ops[i+1])<<8 | int(ops[i+2])
		n := int(ops[i]) / 7
		switch ops[i] % 7 {
		case 0:
			tr.set(k, i)
			model[k] = i
		case 1:
			tr.delete(k)
			delete(model, k)
		case 2:
			tr.deleteRange(k, k+ahead*n)
			for key := k; key != k+ahead*n; key += ahead {
				delete(model, key)
			}
		case 3:
			for j := range 8 * n {
				tr.set(k+ahead*j, i)
				model[k+ahead*j] = i
			}
		case 4:
			if k, _, ok := tr.popMin(); ok {
				delete(model, k)
			}
		case 5:
			if k, _, ok := tr.popMax(); ok {
				delete(model, k)
			}
		default:
			tr.reset()
			clear(model)
		}
		if err := check(tr); err != nil {
			t.Fatalf("operation %d: %v", i/3, err)
		}
	}
	requireModel(t, tr, model)
}

// direction returns 1 when the order of tr is ascending and -1 when it is
// descending: the step from a key to the next larger key in that order.
func direction[V any, O order[int]](tr *tree[int, V, O]) int {
	if tr.order.less(0, 1) {
		return 1
	}

	return -1
}

// requireModel fails the test unless tr keeps its invariants and yields
// exactly the items of model, in its order.
func requireModel[V comparable, O order[int]](tb testing.TB, tr *tree[int, V, O], model map[int]V) {
	tb.Helper()
	requireValid(tb, tr)
	n, match := 0, true
	var prev int
	for k, v := range tr.walk {
		want, ok := model[k]
		match = match && ok && v == want && (n == 0 || tr.order.less(prev, k))
		prev = k
		n++
	}
	testkit.True(tb, match, "the tree must yield the items of its model in order")
	testkit.Equal(tb, n, len(model), "the tree must hold as many items as its model")
}

// TestTree is in package btree because node IDs and shared nodes are
// unexported.
func TestTree(t *testing.T) {
	t.Parallel()

	t.Run("clone", func(t *testing.T) {
		t.Parallel()

		t.Run("gives both trees new IDs", func(t *testing.T) {
			t.Parallel()
			tr := build(leavesOf(minItems, minItems))
			before := tr.owner
			c := tr.clone()
			testkit.True(t, tr.owner != before && c.owner != before && tr.owner != c.owner,
				"the tree and its clone must have distinct new IDs")
		})

		t.Run("shares every node and the order, and keeps the free lists with the tree", func(t *testing.T) {
			t.Parallel()
			tr := &tree[int, int, custom[int]]{order: reverse}
			for k := range maxItems + 1 {
				tr.set(k, -k)
			}
			tr.releaseLeaf(tr.newLeaf())
			c := tr.clone()
			testkit.True(t, c.root == tr.root && c.len == tr.len, "the clone must share the root")
			testkit.True(t, c.order.less(2, 1), "the clone must have the order of the tree")
			testkit.True(t, len(tr.freeLeaves) == 1 && len(c.freeLeaves) == 0, "the free list must stay with the tree")
		})

		t.Run("makes a write copy each shared node on its path once", func(t *testing.T) {
			t.Parallel()
			tr := build(leavesOf(minItems, minItems))
			root, first, second := tr.root, tr.root.leaves[0], tr.root.leaves[1]
			c := tr.clone()
			tr.set(1, -1)
			testkit.True(t, tr.root != root && tr.root.leaves[0] != first, "the first write must copy its path")
			testkit.True(t, c.root == root && root.leaves[0] == first && first.n == minItems,
				"the clone must keep the old nodes unchanged")
			copied := tr.root.leaves[0]
			tr.set(3, -3)
			testkit.True(t, tr.root.leaves[0] == copied, "the second write must change the copy in place")
			testkit.True(t, tr.root.leaves[1] == second, "a leaf off the path must stay shared")
			requireValid(t, tr)
			requireValid(t, &c)
		})
	})
}

func TestTreeModel(t *testing.T) {
	t.Parallel()

	t.Run("the tree of a Map matches a Go map through a million random operations", func(t *testing.T) {
		t.Parallel()
		churn(t, &ints{}, func(op int) int { return op })
	})

	t.Run("the tree of a MapFunc in reverse order matches a Go map through a million random operations",
		func(t *testing.T) {
			t.Parallel()
			churn(t, &tree[int, int, custom[int]]{order: reverse}, func(op int) int { return op })
		})

	t.Run("the tree of a Set matches a Go map through a million random operations", func(t *testing.T) {
		t.Parallel()
		churn(t, &tree[int, struct{}, natural[int]]{}, func(int) struct{} { return struct{}{} })
	})
}

// FuzzTreeModel applies the operations that its input encodes to the tree
// of a Map and to the tree of a MapFunc in reverse order. It checks the
// invariants of each tree after every operation, and its items against a
// Go map at the end. An operation is 3 bytes: the operation and its count,
// and a key of 2 bytes.
func FuzzTreeModel(f *testing.F) {
	f.Add([]byte{
		255, 0, 0, 255, 1, 80, 255, 2, 160, 255, 3, 240, 240, 0, 100, 1, 0, 50, 0, 0, 51, 4, 0, 0, 5, 0, 0,
		6, 0, 0, 255, 0, 0,
	})
	f.Fuzz(func(t *testing.T, ops []byte) {
		replay(t, &ints{}, ops)
		replay(t, &tree[int, int, custom[int]]{order: reverse}, ops)
	})
}
