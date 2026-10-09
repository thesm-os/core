// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package btree

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"math/rand/v2"
	"reflect"
	"strconv"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"
)

// Sizes of the churn tests.
const (
	// operations is the number of random operations that churn applies to
	// each tree.
	operations = 1_000_000

	// keySpace is the number of keys that the operations choose from. churn
	// inserts three quarters of them first, which gives every tree two
	// levels of internal nodes.
	keySpace = 6_000

	// phase is the number of operations between two switches of the mix of
	// operations, from 80% inserts to 20% and back. A tree grows to two
	// levels of internal nodes in one phase and shrinks to one in the next,
	// so its root splits and merges.
	phase = 50_000

	// cloneEvery is the number of operations between two clones. A clone
	// must keep its items through the operations until the next clone.
	cloneEvery = 1_000

	// checkEvery is the number of operations between two comparisons of a
	// tree with its model.
	checkEvery = 10_000

	// churnSeed seeds the operations of churn, so every run applies the
	// same operations.
	churnSeed = 0x6274726565
)

// Sizes of the machine of writes.
const (
	// writeContract is the contract of writeSteps, which TestTreeModel and
	// FuzzTreeModel check.
	writeContract = "every tree must keep its invariants and the items of its model under any sequence of writes"

	// writeKeys is the number of keys that the steps of writeSteps choose
	// from, few enough that a fill or a range covers keys that earlier steps
	// wrote.
	writeKeys = 1 << 12

	// writeSpan is the largest count of a fill or a range of writeSteps. A
	// fill sets 8 keys for each unit of its count, so one fill of the
	// largest count splits several leaves.
	writeSpan = 36
)

// errInvariant is the error that check returns for a broken invariant.
var errInvariant = errors.New("btree: invariant")

// outcome is what one operation of churn returned, or what its model
// predicts: the value and the presence of a key, or the number of keys that
// a deleteRange removed.
type outcome[V comparable] struct {
	value   V
	present bool
	count   int
}

// ints is the tree of a Map[int, int], the tree that build returns.
type ints = tree[int, int, natural[int]]

// checker walks a tree for check.
type checker[K, V any, O order[K]] struct {
	t         *tree[K, V, O]
	leafDepth int
}

// node checks the subtree of in, or of l when in is nil, and returns its
// number of items. The keys of the subtree must sort at or after lo and
// before hi, and its first key must be lo when lo is not nil. A node on the
// right edge of the tree may have fewer than minItems items or separators,
// but not none.
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

// mirror is a tree that writeSteps writes to, with a Go map as its model,
// and the clone and the model of the last clone step. ahead is the step from
// a key to the next larger key in the order of the tree.
type mirror[O order[int]] struct {
	tr     *tree[int, int, O]
	model  map[int]int
	clone  tree[int, int, O]
	cloned map[int]int
	ahead  int
}

// set sets k to v in the tree and in the model, and fails the case unless
// the tree returns the value that the model contained.
func (m *mirror[O]) set(c *prop.Case, k, v int) {
	old, ok := m.tr.set(k, v)
	want, present := m.model[k]
	assert.Equal(c, ok, present, "set must report whether the model contains the key")
	assert.Equal(c, old, want, "set must return the value of the model")
	m.model[k] = v
}

// update stores v under k through update, and fails the case unless update
// passes fn the value that the model contained.
func (m *mirror[O]) update(c *prop.Case, k, v int) {
	want, present := m.model[k]
	m.tr.update(k, func(old int, exists bool) int {
		assert.Equal(c, exists, present, "update must tell fn whether the model contains the key")
		assert.Equal(c, old, want, "update must pass fn the value of the model")

		return v
	})
	m.model[k] = v
}

// delete removes k, and fails the case unless the tree returns the value
// that the model contained.
func (m *mirror[O]) delete(c *prop.Case, k int) {
	old, ok := m.tr.delete(k)
	want, present := m.model[k]
	assert.Equal(c, ok, present, "delete must report whether the model contains the key")
	assert.Equal(c, old, want, "delete must return the value of the model")
	delete(m.model, k)
}

// deleteRange removes the n keys from k on in the order of the tree, and
// fails the case unless the tree counts the keys that the model contained.
func (m *mirror[O]) deleteRange(c *prop.Case, k, n int) {
	removed := m.tr.deleteRange(k, k+m.ahead*n)
	want := 0
	for key := k; key != k+m.ahead*n; key += m.ahead {
		if _, ok := m.model[key]; ok {
			delete(m.model, key)
			want++
		}
	}
	assert.Equal(c, removed, want, "deleteRange must count the keys that it removed")
}

// pop calls remove, which is popMin or popMax of the tree, and removes the
// key k that it returns from the model. It fails the case unless the item
// was in the model, and outside(k, rest) is true for the key rest that
// peek, min or max of the tree, then returns.
func (m *mirror[O]) pop(c *prop.Case, remove, peek func() (int, int, bool), outside func(k, rest int) bool) {
	k, v, ok := remove()
	assert.Equal(c, ok, len(m.model) != 0, "a pop must find a key exactly when the model has one")
	if !ok {
		return
	}
	assert.Contains(c, m.model, k, "a pop must return a key of the model")
	assert.Equal(c, v, m.model[k], "a pop must return the value of the model")
	delete(m.model, k)
	if rest, _, ok := peek(); ok {
		assert.True(c, outside(k, rest), "a pop must remove the key at the end of the order")
	}
}

// check returns an error when t breaks an invariant of its tree: an empty
// tree without nodes, keys and separators in the order of t and between the
// separators above them, each separator the first key of the subtree after
// it, node fill between minItems and maxItems outside the root and the
// right edge, correct subtree counts and length, the child array of each
// level, every leaf at the same depth, zero slots past the used ones, and
// free nodes that are zeroed and on the free list of their kind.
func check[K, V any, O order[K]](t *tree[K, V, O]) error {
	for _, l := range t.freeLeaves {
		if l.n != 0 || l.owner != 0 || !zeroFrom(l.keys[:], 0) || !zeroFrom(l.vals[:], 0) {
			return fmt.Errorf("%w: a free leaf that is not zeroed", errInvariant)
		}
	}
	for _, in := range t.freeLeafParents {
		if in.inners != nil || in.leaves == nil || !zeroFrom(in.leaves[:], 0) ||
			in.n != 0 || in.count != 0 || in.owner != 0 || !zeroFrom(in.keys[:], 0) {

			return fmt.Errorf("%w: a free parent of leaves that is not zeroed", errInvariant)
		}
	}
	for _, in := range t.freeInnerParents {
		if in.leaves != nil || in.inners == nil || !zeroFrom(in.inners[:], 0) ||
			in.n != 0 || in.count != 0 || in.owner != 0 || !zeroFrom(in.keys[:], 0) {

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

// churn applies random operations to tr and to a Go map, its model. It
// records what each operation returns and what the model predicts, and
// fails the test when the two records of the operations since the last
// check differ, at an interval of checkEvery operations. It also fails when
// a pop removes a key after a key of the tree, when tr differs from its
// model at a check, or when a clone of tr differs from the model at the
// time of the clone after the next cloneEvery operations, and unless tr had
// one level of internal nodes at one check and two at another. churn
// resets tr at the start of every phase of inserts after the first, so the
// tree refills from the nodes that it kept. value returns the value that
// operation op sets.
func churn[V comparable, O order[int]](t *testing.T, tr *tree[int, V, O], value func(op int) V) {
	t.Helper()
	r := rand.New(rand.NewPCG(churnSeed, keySpace))
	model := map[int]V{}
	for _, k := range r.Perm(keySpace)[:keySpace*3/4] {
		tr.set(k, value(k))
		model[k] = value(k)
	}
	ahead := direction(tr)
	before := func(k, rest int) bool { return tr.order.less(k, rest) }
	after := func(k, rest int) bool { return tr.order.less(rest, k) }
	heights := map[int]bool{}
	var clone tree[int, V, O]
	var cloned map[int]V
	got := make([]outcome[V], 0, checkEvery)
	want := make([]outcome[V], 0, checkEvery)
	from := 0
	//dokimi:lint-skip for-all: a fixed churn of the free lists, which prop.ForAll would run 100 times
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
		was, present := model[k]
		switch n := r.IntN(100); {
		case n < sets:
			old, ok := tr.set(k, value(op))
			got = append(got, outcome[V]{value: old, present: ok})
			want = append(want, outcome[V]{value: was, present: present})
			model[k] = value(op)
		case n < sets+6:
			tr.update(k, func(old V, exists bool) V {
				got = append(got, outcome[V]{value: old, present: exists})

				return value(op)
			})
			want = append(want, outcome[V]{value: was, present: present})
			model[k] = value(op)
		case n < 92:
			old, ok := tr.delete(k)
			got = append(got, outcome[V]{value: old, present: ok})
			want = append(want, outcome[V]{value: was, present: present})
			delete(model, k)
		case n < 96:
			lo, hi := k, k+ahead*r.IntN(9)
			got = append(got, outcome[V]{count: tr.deleteRange(lo, hi)})
			removed := 0
			for key := lo; key != hi; key += ahead {
				if _, ok := model[key]; ok {
					delete(model, key)
					removed++
				}
			}
			want = append(want, outcome[V]{count: removed})
		case n < 98:
			got, want = pop(t, got, want, model, tr.popMin, tr.min, before)
		default:
			got, want = pop(t, got, want, model, tr.popMax, tr.max, after)
		}
		if op%cloneEvery == 0 {
			if cloned != nil {
				requireModel(t, &clone, cloned)
			}
			clone, cloned = tr.clone(), maps.Clone(model)
		}
		if op%checkEvery == 0 {
			predicted := "the operations from " + strconv.Itoa(from) + " on must return what the model predicts"
			assert.Equal(t, got, want, predicted)
			got, want, from = got[:0], want[:0], op+1
			requireModel(t, tr, model)
			_, levels := sizes(tr)
			heights[len(levels)] = true
		}
	}
	predicted := "the operations from " + strconv.Itoa(from) + " on must return what the model predicts"
	assert.Equal(t, got, want, predicted)
	requireModel(t, tr, model)
	expect.True(t, heights[1], "the tree must have had one level of internal nodes")
	expect.True(t, heights[2], "the tree must have had two levels of internal nodes")
}

// pop calls remove, which is popMin or popMax of a tree, and removes the key
// k that it returns from model. It appends what remove returned to got and
// the item of k in model to want, or for an empty tree whether model has an
// item. It fails the test unless outside(k, rest) is true for the key rest
// that peek, min or max of the tree, then returns. It returns got and want.
func pop[V comparable](tb assert.TB, got, want []outcome[V], model map[int]V,
	remove, peek func() (int, V, bool), outside func(k, rest int) bool,
) ([]outcome[V], []outcome[V]) {
	tb.Helper()
	k, v, ok := remove()
	if !ok {
		return append(got, outcome[V]{}), append(want, outcome[V]{present: len(model) != 0})
	}
	was, present := model[k]
	delete(model, k)
	if rest, _, more := peek(); more {
		assert.True(tb, outside(k, rest), "a pop must remove the key before every key of the tree")
	}

	return append(got, outcome[V]{value: v, present: true}), append(want, outcome[V]{value: was, present: present})
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
func requireModel[V comparable, O order[int]](tb assert.TB, tr *tree[int, V, O], model map[int]V) {
	tb.Helper()
	assert.NoError(tb, check(tr), "the tree must keep its invariants")
	var keys []int
	items := make(map[int]V, len(model))
	for k, v := range tr.walk {
		keys = append(keys, k)
		items[k] = v
	}
	assert.Pairwise(tb, keys, tr.order.less, "the tree must yield its keys in its order")
	assert.Equal(tb, items, model, "the tree must yield the items of its model")
}

// writeSteps runs the steps of a machine of writes over the tree of a Map
// and over the tree of a MapFunc in reverse order, each with a Go map as its
// model. Every step writes to both trees. A clone step keeps a clone of
// each tree with a copy of its model, which every later step checks. After
// each step both trees must keep their invariants and the items of their
// models.
func writeSteps(c *prop.Case) {
	asc := mirror[natural[int]]{tr: &ints{}, model: map[int]int{}, ahead: 1}
	desc := mirror[custom[int]]{
		tr:    &tree[int, int, custom[int]]{order: func(a, b int) int { return cmp.Compare(b, a) }},
		model: map[int]int{},
		ahead: -1,
	}

	// value is the value of the latest write, so every write stores a value
	// of its own.
	value := 0
	key := func(c *prop.Case, _ struct{}) any { return c.Draw(prop.Integer(0, writeKeys-1), "key") }
	span := func(c *prop.Case, _ struct{}) any {
		return [2]int{c.Draw(prop.Integer(0, writeKeys-1), "key"), c.Draw(prop.Integer(0, writeSpan), "count")}
	}

	stateful.Steps(c, stateful.Machine[struct{}]{
		Actions: []stateful.Action[struct{}]{
			{
				Name:  "set",
				Input: key,
				Run: func(c *prop.Case, _ int, in any) {
					value++
					asc.set(c, in.(int), value)
					desc.set(c, in.(int), value)
				},
			},
			{
				Name:  "update",
				Input: key,
				Run: func(c *prop.Case, _ int, in any) {
					value++
					asc.update(c, in.(int), value)
					desc.update(c, in.(int), value)
				},
			},
			{
				Name:  "delete",
				Input: key,
				Run: func(c *prop.Case, _ int, in any) {
					asc.delete(c, in.(int))
					desc.delete(c, in.(int))
				},
			},
			{
				Name:  "deleteRange",
				Input: span,
				Run: func(c *prop.Case, _ int, in any) {
					s := in.([2]int)
					asc.deleteRange(c, s[0], s[1])
					desc.deleteRange(c, s[0], s[1])
				},
			},
			{
				Name:   "fill",
				Weight: 2,
				Input:  span,
				Run: func(c *prop.Case, _ int, in any) {
					s := in.([2]int)
					value++
					for j := range 8 * s[1] {
						asc.set(c, s[0]+asc.ahead*j, value)
						desc.set(c, s[0]+desc.ahead*j, value)
					}
				},
			},
			{
				Name: "popMin",
				Run: func(c *prop.Case, _ int, _ any) {
					asc.pop(c, asc.tr.popMin, asc.tr.min, func(k, rest int) bool { return k < rest })
					desc.pop(c, desc.tr.popMin, desc.tr.min, func(k, rest int) bool { return k > rest })
				},
			},
			{
				Name: "popMax",
				Run: func(c *prop.Case, _ int, _ any) {
					asc.pop(c, asc.tr.popMax, asc.tr.max, func(k, rest int) bool { return k > rest })
					desc.pop(c, desc.tr.popMax, desc.tr.max, func(k, rest int) bool { return k < rest })
				},
			},
			{
				Name: "reset",
				Run: func(*prop.Case, int, any) {
					asc.tr.reset()
					clear(asc.model)
					desc.tr.reset()
					clear(desc.model)
				},
			},
			{
				Name: "clone",
				Run: func(*prop.Case, int, any) {
					asc.clone, asc.cloned = asc.tr.clone(), maps.Clone(asc.model)
					desc.clone, desc.cloned = desc.tr.clone(), maps.Clone(desc.model)
				},
			},
		},
		Invariant: func(c *prop.Case, _ struct{}) {
			requireModel(c, asc.tr, asc.model)
			requireModel(c, desc.tr, desc.model)
			if asc.cloned != nil {
				requireModel(c, &asc.clone, asc.cloned)
				requireModel(c, &desc.clone, desc.cloned)
			}
		},
	})
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
			expect.NotEqual(t, tr.owner, before, "the tree must have a new ID")
			expect.NotEqual(t, c.owner, before, "the clone must have a new ID")
			expect.NotEqual(t, c.owner, tr.owner, "the tree and its clone must have distinct IDs")
		})

		t.Run("shares every node with the tree", func(t *testing.T) {
			t.Parallel()
			tr := build(leavesOf(minItems, minItems))
			c := tr.clone()
			expect.Equal(t, c.root, tr.root, "the clone must share the root", expect.ByIdentity())
			expect.Equal(t, c.len, tr.len, "the clone must have the length of the tree")
		})

		t.Run("keeps the order of the tree", func(t *testing.T) {
			t.Parallel()
			tr := &tree[int, int, custom[int]]{order: func(a, b int) int { return cmp.Compare(b, a) }}
			c := tr.clone()
			assert.True(t, c.order.less(2, 1), "the clone must have the order of the tree")
		})

		t.Run("leaves the free lists with the tree", func(t *testing.T) {
			t.Parallel()
			tr := build(leavesOf(minItems, minItems))
			tr.releaseLeaf(tr.newLeaf())
			c := tr.clone()
			expect.Length(t, tr.freeLeaves, 1, "the tree must keep its free leaf")
			expect.Empty(t, c.freeLeaves, "the clone must start without free nodes")
		})

		t.Run("makes a write copy each shared node on its path once", func(t *testing.T) {
			t.Parallel()
			tr := build(leavesOf(minItems, minItems))
			root, first, second := tr.root, tr.root.leaves[0], tr.root.leaves[1]
			c := tr.clone()
			tr.set(1, -1)
			expect.NotEqual(t, tr.root, root, "the first write must copy the root", expect.ByIdentity())
			expect.NotEqual(t, tr.root.leaves[0], first, "the first write must copy the leaf", expect.ByIdentity())
			expect.Equal(t, c.root, root, "the clone must keep the old root", expect.ByIdentity())
			expect.Equal(t, root.leaves[0], first, "the old root must keep the old leaf", expect.ByIdentity())
			expect.Equal(t, first.n, minItems, "the old leaf must keep its items")
			copied := tr.root.leaves[0]
			tr.set(3, -3)
			expect.Equal(t, tr.root.leaves[0], copied, "the second write must change the copy in place",
				expect.ByIdentity())
			expect.Equal(t, tr.root.leaves[1], second, "the tree must share a leaf off the path with the clone",
				expect.ByIdentity())
			expect.NoError(t, check(tr), "the tree must keep its invariants")
			expect.NoError(t, check(&c), "the clone must keep its invariants")
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
			reverse := func(a, b int) int { return cmp.Compare(b, a) }
			churn(t, &tree[int, int, custom[int]]{order: reverse}, func(op int) int { return op })
		})

	t.Run("the tree of a Set matches a Go map through a million random operations", func(t *testing.T) {
		t.Parallel()
		churn(t, &tree[int, struct{}, natural[int]]{}, func(int) struct{} { return struct{}{} })
	})

	t.Run("the trees of a Map and a MapFunc keep their invariants under any sequence of writes",
		func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, writeContract, writeSteps)
		})
}

// FuzzTreeModel checks the machine of writeSteps on the sequences of writes
// that a fuzzer finds.
func FuzzTreeModel(f *testing.F) {
	prop.Fuzz(f, writeContract, writeSteps)
}
