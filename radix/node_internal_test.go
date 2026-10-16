// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package radix

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"
	"slices"
	"strconv"
	"strings"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"
)

// The machine of the structure of a map.
const (
	// structureContract is the contract of structureSteps, which TestNode
	// and FuzzNode check.
	structureContract = "a map keeps the invariants of its tree and the keys of its model under any sequence of writes"

	// widest is the most keys that a fill sets, more than the 48 children of
	// a node48.
	widest = 80

	// share is the least share of the cases of the machine that count each
	// label of its coverage.
	share = 0.05
)

// The labels of the coverage of the machine.
const (
	labelNode16  = "a node16"
	labelNode48  = "a node48"
	labelNode256 = "a node256"
	labelDeep    = "a skip of more than 8 bytes"
	labelRegion  = "a key in a region"
)

// errInvariant is the error that check returns for a broken invariant.
var errInvariant = errors.New("radix: invariant")

// treeStems are the keys that the steps of the machine extend. Their lengths
// are on both sides of every inline length. Many share a long run of one
// byte, so a node skips more than 8 bytes, and a key splits a skip past its
// first 8 bytes.
var treeStems = func() []string {
	ss := []string{""}
	for _, n := range []int{1, 2, 8, 9, 11, 12, 13, 27, 28, 29, 59, 60, 61} {
		ss = append(ss, strings.Repeat("k", n))
	}

	return append(ss, strings.Repeat("k", 20)+"x", strings.Repeat("k", 9)+"y"+strings.Repeat("k", 10))
}()

// treeTails are the endings that a step appends to a stem.
var treeTails = []string{"", "a", "b", "c", "ab", "ba", "abc"}

// treeFill is the input of the steps that set and delete the keys of a stem
// followed by each of n bytes from lo on.
type treeFill struct {
	stem  string
	lo, n int
}

// checker walks a map for check. It records the records of the tree and the
// longest skip of an inner node.
type checker[K Key, V comparable] struct {
	m       *Map[K, V]
	seen    [kinds]map[uint32]bool
	leaves  int
	maxSkip int
}

// check returns an error when the map of c breaks an invariant of its tree:
//
//   - The root is an inner node without skipped bytes, and the map without a
//     root has no key.
//   - Every inner node other than the root has at least two items among its
//     children and its term, and the count of its kind.
//   - The slots of each node match its count: sorted branch bytes in a node4
//     and a node16, 0 and no child in their unused slots, and present bits
//     that match the slots of a node48 and a node256.
//   - The skipped bytes of a node are the bytes that every key below it has
//     there, its prefix contains the first 8 of them, and its term ends at
//     the node.
//   - A leaf stores a key of up to the inline length in place, and a longer
//     key in a region of the slabs.
//   - Every record is in the tree or on the stack of its kind, not both and
//     at most once, and a free record and its value are zero.
func (c *checker[K, V]) check(m *Map[K, V]) error {
	c.m = m
	for k := range c.seen {
		c.seen[k] = map[uint32]bool{}
	}
	if m.root != 0 {
		if m.root&kindMask == kindLeaf {
			return fmt.Errorf("%w: a leaf at the root", errInvariant)
		}
		if err := c.node(m.root, nil, true); err != nil {
			return err
		}
	}
	if c.leaves != m.len {
		return fmt.Errorf("%w: a map of %d keys with %d leaves", errInvariant, m.len, c.leaves)
	}

	lens := [kinds]int{
		kind4: m.n4.Len(), kind16: m.n16.Len(), kind48: m.n48.Len(), kind256: m.n256.Len(),
		kindLeaf: [...]int{width16: m.leaves16.Len(), width32: m.leaves32.Len(), width64: m.leaves64.Len()}[m.width],
	}
	if m.values.Len() != lens[kindLeaf] {
		return fmt.Errorf("%w: %d values for %d leaves", errInvariant, m.values.Len(), lens[kindLeaf])
	}
	for kind := uint32(kindLeaf); kind < kinds; kind++ {
		free := map[uint32]bool{}
		for _, i := range m.spare[kind] {
			if int(i) >= lens[kind] || free[i] || c.seen[kind][i] {
				return fmt.Errorf("%w: a free record of the kind %d that is not free", errInvariant, kind)
			}
			free[i] = true
			if !c.zero(kind, i) {
				return fmt.Errorf("%w: a free record of the kind %d that is not zero", errInvariant, kind)
			}
		}
		if len(c.seen[kind])+len(free) != lens[kind] {
			return fmt.Errorf("%w: a record of the kind %d outside the tree and the stack", errInvariant, kind)
		}
	}

	return nil
}

// node checks the subtree of the record h, whose keys start with path. The
// first skipped byte of an inner node h is at len(path).
func (c *checker[K, V]) node(h uint32, path []byte, root bool) error {
	kind, i := h&kindMask, h>>kindBits
	if kind == 0 || kind >= kinds {
		return fmt.Errorf("%w: a handle of the kind %d", errInvariant, kind)
	}
	if c.seen[kind][i] {
		return fmt.Errorf("%w: a record of the kind %d at two places", errInvariant, kind)
	}
	c.seen[kind][i] = true
	if kind == kindLeaf {
		return c.leaf(h, path)
	}

	hd := c.m.header(h)
	skip := int(hd.skip)
	c.maxSkip = max(c.maxSkip, skip)
	if root && skip != 0 {
		return fmt.Errorf("%w: a root that skips %d bytes", errInvariant, skip)
	}
	l := c.m.minLeaf(h)
	if l == 0 {
		if !root {
			return fmt.Errorf("%w: an inner node without keys", errInvariant)
		}

		return nil
	}
	lk := c.m.key(l)
	if len(lk) < len(path)+skip {
		return fmt.Errorf("%w: a key shorter than the skipped bytes above it", errInvariant)
	}
	full := append(slices.Clone(path), lk[len(path):len(path)+skip]...)
	if hd.prefix != prefixOf(full[len(path):]) {
		return fmt.Errorf("%w: a prefix that differs from the skipped bytes", errInvariant)
	}
	items := int(hd.count)
	if t := hd.term; t != 0 {
		if t&kindMask != kindLeaf || !bytes.Equal(c.m.key(t), full) {
			return fmt.Errorf("%w: a term whose key does not end at its node", errInvariant)
		}
		if err := c.node(t, full, false); err != nil {
			return err
		}
		items++
	}
	if !root && items < 2 {
		return fmt.Errorf("%w: an inner node of %d items below the root", errInvariant, items)
	}
	if err := c.slots(h); err != nil {
		return err
	}
	for b, ch := range c.m.children(h) {
		if err := c.node(ch, append(slices.Clone(full), b), false); err != nil {
			return err
		}
	}

	return nil
}

// leaf checks the leaf h, whose key starts with path, and the storage of its
// key.
func (c *checker[K, V]) leaf(h uint32, path []byte) error {
	k := c.m.key(h)
	if !bytes.HasPrefix(k, path) {
		return fmt.Errorf("%w: a leaf of %q below the path %q", errInvariant, k, path)
	}
	rec := c.m.record(h)
	inline := rec[:len(rec)-lenBytes]
	n := int(binary.LittleEndian.Uint32(rec[len(rec)-lenBytes:]))
	if n != len(k) {
		return fmt.Errorf("%w: a leaf of %d bytes whose key has %d", errInvariant, n, len(k))
	}
	rest := inline[min(n, 8):]
	if n <= len(inline) {
		rest = inline[n:]
	}
	if bytes.Count(rest, []byte{0}) != len(rest) {
		return fmt.Errorf("%w: a record with bytes past its key", errInvariant)
	}
	c.leaves++

	return nil
}

// slots checks the slots of the inner node h against its count and its
// kind.
func (c *checker[K, V]) slots(h uint32) error {
	i := int(h >> kindBits)
	switch h & kindMask {
	case kind4:
		n := c.m.n4.Ptr(i)
		if n.count > max4 {
			return fmt.Errorf("%w: a node4 of %d children", errInvariant, n.count)
		}

		return listed(n.keys[:], n.kids[:], int(n.count))
	case kind16:
		n := c.m.n16.Ptr(i)
		if n.count <= shrink16 || n.count > max16 {
			return fmt.Errorf("%w: a node16 of %d children", errInvariant, n.count)
		}

		return listed(n.keys[:], n.kids[:], int(n.count))
	case kind48:
		n := c.m.n48.Ptr(i)
		if n.count <= shrink48 || n.count > max48 {
			return fmt.Errorf("%w: a node48 of %d children", errInvariant, n.count)
		}
		used := map[uint8]bool{}
		for b := range 256 {
			s := n.index[b]
			if (s != 0) != (n.present[b>>6]&(1<<(b&63)) != 0) || s > max48 ||
				(s != 0 && (used[s] || n.kids[s-1] == 0)) {

				return fmt.Errorf("%w: a node48 whose slot of the byte %d does not match", errInvariant, b)
			}
			used[s] = s != 0
		}
		kids := 0
		for _, k := range n.kids {
			if k != 0 {
				kids++
			}
		}

		return counted(n.present, int(n.count), kids)
	default:
		n := c.m.n256.Ptr(i)
		if n.count <= shrink256 {
			return fmt.Errorf("%w: a node256 of %d children", errInvariant, n.count)
		}
		kids := 0
		for b, k := range &n.kids {
			if (k != 0) != (n.present[b>>6]&(1<<(b&63)) != 0) {
				return fmt.Errorf("%w: a node256 whose slot of the byte %d does not match", errInvariant, b)
			}
			if k != 0 {
				kids++
			}
		}

		return counted(n.present, int(n.count), kids)
	}
}

// zero reports whether the record i of kind is zero, with its value for a
// leaf.
func (c *checker[K, V]) zero(kind, i uint32) bool {
	var zv V
	switch kind {
	case kindLeaf:
		rec := c.m.record(i<<kindBits | kindLeaf)

		return bytes.Count(rec, []byte{0}) == len(rec) && c.m.values.At(int(i)) == zv
	case kind4:
		return *c.m.n4.Ptr(int(i)) == node4{}
	case kind16:
		return *c.m.n16.Ptr(int(i)) == node16{}
	case kind48:
		return *c.m.n48.Ptr(int(i)) == node48{}
	default:
		return *c.m.n256.Ptr(int(i)) == node256{}
	}
}

func TestNode(t *testing.T) {
	t.Parallel()

	t.Run("alloc", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the record that freeNode freed", func(t *testing.T) {
			t.Parallel()
			var m Map[string, int]
			h := m.alloc(kind16)
			i := h >> kindBits
			m.n16.Ptr(int(i)).count = 5
			m.freeNode(h)
			assert.Equal(t, m.spare[kind16], []uint32{i}, "freeNode puts the index of the record on the stack")
			reused := m.alloc(kind16)
			expect.Empty(t, m.spare[kind16], "alloc takes the index from the stack")
			expect.Equal(t, reused>>kindBits, i, "alloc returns the freed record")
			expect.Equal(t, *m.n16.Ptr(int(i)), node16{}, "the freed record is zero")
			expect.Equal(t, m.n16.Len(), 1, "alloc appends no record")
		})
	})

	t.Run("slot16", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the lowest slot of every byte", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "slot16 returns the lowest slot of each byte", func(c *prop.Case) {
				set := c.Draw(prop.List(prop.Integer[byte](0, 255), prop.MinSize(max4), prop.MaxSize(max16)), "bytes")
				slices.Sort(set)
				set = slices.Compact(set)
				var keys [max16]byte
				copy(keys[:], set)
				for b := range 256 {
					// The unused slots are 0, so slot16 finds the byte 0 of a node
					// without it in the first unused slot.
					want := slices.Index(set, byte(b))
					if want < 0 && b == 0 && len(set) < max16 {
						want = len(set)
					}
					msg := "slot16 returns the lowest slot of the byte " + strconv.Itoa(b)
					assert.Equal(c, slot16(&keys, byte(b)), want, msg)
				}
			})
		})
	})

	t.Run("addChild", func(t *testing.T) {
		t.Parallel()

		t.Run("grows a node to the next kind after its last slot", func(t *testing.T) {
			t.Parallel()
			var m Map[string, int]
			for j := range 256 {
				_, _, err := m.Set("x"+string([]byte{byte(j)}), j)
				assert.NoError(t, err, "Set stores the key")
				want := uint32(kindLeaf)
				if j >= max48 {
					want = kind256
				} else if j >= max16 {
					want = kind48
				} else if j >= max4 {
					want = kind16
				} else if j >= 1 {
					want = kind4
				}
				msg := "the record has the kind of " + strconv.Itoa(j+1) + " keys"
				assert.Equal(t, m.child(m.root, 'x')&kindMask, want, msg)
			}
			var ck checker[string, int]
			assert.NoError(t, ck.check(&m), "the map keeps the invariants of its tree")
		})
	})

	t.Run("removeChild", func(t *testing.T) {
		t.Parallel()

		t.Run("shrinks a node to the kind before at its shrink count", func(t *testing.T) {
			t.Parallel()
			var m Map[string, int]
			for j := range 256 {
				_, _, err := m.Set("x"+string([]byte{byte(j)}), j)
				assert.NoError(t, err, "Set stores the key")
			}
			for j := range 254 {
				_, ok := m.Delete("x" + string([]byte{byte(j)}))
				assert.True(t, ok, "Delete removes the key")
				left := 255 - j
				want := uint32(kind256)
				if left <= shrink16 {
					want = kind4
				} else if left <= shrink48 {
					want = kind16
				} else if left <= shrink256 {
					want = kind48
				}
				msg := "the node has the kind of " + strconv.Itoa(left) + " children"
				assert.Equal(t, m.child(m.root, 'x')&kindMask, want, msg)
			}
			var ck checker[string, int]
			assert.NoError(t, ck.check(&m), "the map keeps the invariants of its tree")
		})
	})

	t.Run("collapse", func(t *testing.T) {
		t.Parallel()

		t.Run("merges a node of one child into the child", func(t *testing.T) {
			t.Parallel()
			var m Map[string, int]
			for _, k := range []string{"abcdefghijklm1", "abcdefghijklm2", "abcdef"} {
				_, _, err := m.Set(k, 1)
				assert.NoError(t, err, "Set stores the key")
			}
			_, ok := m.Delete("abcdef")
			assert.True(t, ok, "Delete removes the term")
			hd := m.header(m.child(m.root, 'a'))
			expect.Equal(t, hd.skip, uint32(12), "the child skips the bytes of both nodes and the branch byte")
			expect.Equal(t, hd.prefix, [prefixLen]byte([]byte("bcdefghi")), "the child keeps the first 8 skipped bytes")
			var ck checker[string, int]
			expect.NoError(t, ck.check(&m), "the map keeps the invariants of its tree")
		})

		t.Run("gives the slot of a node without children to its term", func(t *testing.T) {
			t.Parallel()
			var m Map[string, int]
			for _, k := range []string{"ab", "abc"} {
				_, _, err := m.Set(k, 1)
				assert.NoError(t, err, "Set stores the key")
			}
			_, ok := m.Delete("abc")
			assert.True(t, ok, "Delete removes the child")
			l := m.child(m.root, 'a')
			assert.Equal(t, l&kindMask, uint32(kindLeaf), "the term takes the slot of the node")
			expect.Equal(t, m.key(l), []byte("ab"), "the slot contains the leaf of the term")
		})
	})

	t.Run("splitSkip", func(t *testing.T) {
		t.Parallel()

		t.Run("splits a skip past its first 8 bytes", func(t *testing.T) {
			t.Parallel()
			var m Map[string, int]
			long := strings.Repeat("k", 20)
			for i, k := range []string{long + "a", long + "b", strings.Repeat("k", 12) + "x"} {
				_, _, err := m.Set(k, i)
				assert.NoError(t, err, "Set stores the key")
			}
			hd := m.header(m.child(m.root, 'k'))
			expect.Equal(t, hd.skip, uint32(11), "the new node skips the bytes that the three keys share")
			var ck checker[string, int]
			expect.NoError(t, ck.check(&m), "the map keeps the invariants of its tree")
		})
	})

	t.Run("leaves", func(t *testing.T) {
		t.Parallel()

		t.Run("stops when the yield returns false", func(t *testing.T) {
			t.Parallel()
			var m Map[string, int]
			for _, k := range []string{"", "a", "b"} {
				_, _, err := m.Set(k, 1)
				assert.NoError(t, err, "Set stores the key")
			}
			n := 0
			for range m.leaves(m.root) {
				n++

				break
			}
			assert.Equal(t, n, 1, "leaves yields one leaf before the break")
		})
	})

	for _, inline := range []int{inline16, inline32, inline64} {
		t.Run("keeps the invariants under any sequence of writes with "+strconv.Itoa(inline)+" bytes in place",
			func(t *testing.T) {
				t.Parallel()
				prop.ForAll(t, structureContract, func(c *prop.Case) { structureSteps(c, inline) },
					prop.Require(labelNode16, share), prop.Require(labelNode48, share),
					prop.Require(labelNode256, share), prop.Require(labelDeep, share),
					prop.Require(labelRegion, share))
			})
	}
}

// FuzzNode checks the machine of structureSteps with leaves of 16 bytes on
// the sequences of writes that a fuzzer finds.
func FuzzNode(f *testing.F) {
	prop.Fuzz(f, structureContract, func(c *prop.Case) { structureSteps(c, inline16) })
}

// structureSteps runs the steps of a machine of writes over a map whose
// leaves store keys of up to inline bytes in place, with a Go map as its
// model. After each step the map must keep the invariants of its tree, have
// the length of the model and contain each key of the model. The case
// counts the kinds of nodes, the skips of more than 8 bytes and the keys in
// regions that the map had.
func structureSteps(c *prop.Case, inline int) {
	m := New[string, int](Config{InlineKeyLen: inline})
	model := map[string]int{}
	value := 0
	labels := map[string]bool{}

	key := func(c *prop.Case, _ struct{}) any {
		return c.Draw(prop.SampledFrom(treeStems...), "stem") + c.Draw(prop.SampledFrom(treeTails...), "tail")
	}
	span := func(c *prop.Case, _ struct{}) any {
		return treeFill{
			stem: c.Draw(prop.SampledFrom(treeStems...), "stem"),
			lo:   c.Draw(prop.Integer(0, 255), "lo"),
			n:    c.Draw(prop.Integer(1, widest), "n"),
		}
	}

	stateful.Steps(c, stateful.Machine[struct{}]{
		Actions: []stateful.Action[struct{}]{
			{
				Name:   "set",
				Weight: 3,
				Input:  key,
				Run: func(c *prop.Case, _ int, in any) {
					value++
					_, _, err := m.Set(in.(string), value)
					assert.NoError(c, err, "Set stores the key")
					model[in.(string)] = value
				},
			},
			{
				Name:   "delete",
				Weight: 2,
				Input:  key,
				Run: func(c *prop.Case, _ int, in any) {
					_, ok := m.Delete(in.(string))
					_, present := model[in.(string)]
					assert.Equal(c, ok, present, "Delete reports whether the model contains the key")
					delete(model, in.(string))
				},
			},
			{
				Name:  "fill",
				Input: span,
				Run: func(c *prop.Case, _ int, in any) {
					f := in.(treeFill)
					value++
					for j := range f.n {
						k := f.stem + string([]byte{byte(f.lo + j)})
						_, _, err := m.Set(k, value)
						assert.NoError(c, err, "Set stores the key of the fill")
						model[k] = value
					}
				},
			},
			{
				Name:  "erase",
				Input: span,
				Run: func(c *prop.Case, _ int, in any) {
					f := in.(treeFill)
					for j := range f.n {
						k := f.stem + string([]byte{byte(f.lo + j)})
						_, ok := m.Delete(k)
						_, present := model[k]
						assert.Equal(c, ok, present, "Delete reports whether the model contains the key of the erase")
						delete(model, k)
					}
				},
			},
			{
				Name: "reset",
				Run: func(*prop.Case, int, any) {
					m.Reset()
					clear(model)
				},
			},
		},
		Invariant: func(c *prop.Case, _ struct{}) {
			var ck checker[string, int]
			assert.NoError(c, ck.check(m), "the map keeps the invariants of its tree")
			assert.Equal(c, m.Len(), len(model), "the map has the length of the model")
			for k, v := range model {
				got, ok := m.Get(k)
				assert.True(c, ok, "the map contains the key of the model")
				assert.Equal(c, got, v, "the map has the value of the model")
			}
			labels[labelNode16] = labels[labelNode16] || len(ck.seen[kind16]) != 0
			labels[labelNode48] = labels[labelNode48] || len(ck.seen[kind48]) != 0
			labels[labelNode256] = labels[labelNode256] || len(ck.seen[kind256]) != 0
			labels[labelDeep] = labels[labelDeep] || ck.maxSkip > prefixLen
			labels[labelRegion] = labels[labelRegion] || m.keys.InUse() != 0
		},
	})

	for label, ok := range labels {
		if ok {
			c.Classify(label)
		}
	}
}

// listed returns an error unless the first count slots of keys are sorted
// and distinct with a child each, and every other slot is 0 and has no child.
func listed(keys []byte, kids []uint32, count int) error {
	for j := range keys {
		if j < count {
			if kids[j] == 0 || (j > 0 && keys[j-1] >= keys[j]) {
				return fmt.Errorf("%w: a slot %d out of order or without a child", errInvariant, j)
			}
		} else if kids[j] != 0 || keys[j] != 0 {
			return fmt.Errorf("%w: an unused slot %d that is not empty", errInvariant, j)
		}
	}

	return nil
}

// counted returns an error unless the present bits, the count and the
// children of a node48 or a node256 agree.
func counted(present [4]uint64, count, kids int) error {
	ones := 0
	for _, w := range present {
		ones += bits.OnesCount64(w)
	}
	if ones != count || kids != count {
		return fmt.Errorf("%w: %d present bits and %d children for a count of %d", errInvariant, ones, kids, count)
	}

	return nil
}
