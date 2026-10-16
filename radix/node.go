// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package radix

import (
	"encoding/binary"
	"iter"
	"math/bits"
)

// The kinds of the records of a map. The low kindBits bits of a handle are
// its kind, and the high 29 bits are the index of its record in the list of
// the kind. The handle 0 is no record.
const (
	// kindLeaf is the kind of a leaf.
	kindLeaf = 1
	// kind4, kind16, kind48 and kind256 are the kinds of the inner nodes.
	kind4   = 2
	kind16  = 3
	kind48  = 4
	kind256 = 5
	// kinds is the number of kinds, with the kind 0 of no record.
	kinds = 6
	// kindBits is the number of bits of the kind of a handle.
	kindBits = 3
	// kindMask selects the kind of a handle.
	kindMask = 7
)

// The children of the kinds of inner nodes. A node grows to the next kind
// at the child after its last slot, and shrinks to the kind before when its
// children fall to the shrink count. The margin between the two keeps a key
// that comes and goes at a boundary from resizing the node each time.
const (
	// max4, max16 and max48 are the slots of a node4, a node16 and a node48.
	max4  = 4
	max16 = 16
	max48 = 48
	// shrink16, shrink48 and shrink256 are the counts at which a node16, a
	// node48 and a node256 shrink to the kind before.
	shrink16  = 3
	shrink48  = 12
	shrink256 = 40
)

// prefixLen is the number of skipped bytes that the header of a node keeps.
const prefixLen = 8

// lsb and msb are the lowest and the highest bit of each byte of a word. The
// search of a node16 compares the eight bytes of a word with them at once.
const (
	lsb = 0x0101010101010101
	msb = 0x8080808080808080
)

// header is the part that every inner node has.
type header struct {
	// term is the leaf of the key that ends at the node, or 0.
	term uint32

	// skip is the number of key bytes between the branch byte of the parent
	// and the branch bytes of the node. Every key below the node has the
	// same bytes there. The root does not skip bytes.
	skip uint32

	// prefix contains the first min(skip, 8) skipped bytes.
	prefix [prefixLen]byte

	// count is the number of children.
	count uint16
}

// node4 is an inner node of up to 4 children, with its branch bytes sorted.
type node4 struct {
	header
	keys [max4]byte
	kids [max4]uint32
}

// node16 is an inner node of 4 to 16 children, with its branch bytes sorted.
// An unused slot of keys is 0 and has no child, so a search that matches it
// finds no child.
type node16 struct {
	header
	keys [max16]byte
	kids [max16]uint32
}

// node48 is an inner node of 13 to 48 children. index[b] is the slot of the
// child of the branch byte b in kids plus one, or 0, and present has the bit
// of each branch byte that has a child.
type node48 struct {
	header
	present [4]uint64
	index   [256]uint8
	kids    [max48]uint32
}

// node256 is an inner node of 41 to 256 children. kids[b] is the child of
// the branch byte b, and present has the bit of each branch byte that has a
// child.
type node256 struct {
	header
	present [4]uint64
	kids    [256]uint32
}

// alloc returns the handle of a zero record of kind: a record that a delete
// freed, or a new record at the end of the list of the kind.
func (m *Map[K, V]) alloc(kind uint32) uint32 {
	var i int
	if free := m.spare[kind]; len(free) != 0 {
		i = int(free[len(free)-1])
		m.spare[kind] = free[:len(free)-1]
	} else {
		switch kind {
		case kind4:
			i = m.n4.Len()
			m.n4.Append(node4{})
		case kind16:
			i = m.n16.Len()
			m.n16.Append(node16{})
		case kind48:
			i = m.n48.Len()
			m.n48.Append(node48{})
		default:
			i = m.n256.Len()
			m.n256.Append(node256{})
		}
	}

	//nolint:gosec // G115: a map has fewer inner nodes than keys, and at most MaxLen keys
	return uint32(i)<<kindBits | kind
}

// freeNode zeroes the record of the inner node h and puts its index on the
// stack of free records of its kind.
func (m *Map[K, V]) freeNode(h uint32) {
	i := int(h >> kindBits)
	switch h & kindMask {
	case kind4:
		*m.n4.Ptr(i) = node4{}
	case kind16:
		*m.n16.Ptr(i) = node16{}
	case kind48:
		*m.n48.Ptr(i) = node48{}
	default:
		*m.n256.Ptr(i) = node256{}
	}
	m.spare[h&kindMask] = append(m.spare[h&kindMask], h>>kindBits)
}

// header returns the header of the inner node h.
func (m *Map[K, V]) header(h uint32) *header {
	i := int(h >> kindBits)
	switch h & kindMask {
	case kind4:
		return &m.n4.Ptr(i).header
	case kind16:
		return &m.n16.Ptr(i).header
	case kind48:
		return &m.n48.Ptr(i).header
	default:
		return &m.n256.Ptr(i).header
	}
}

// child returns the child of the inner node h at the branch byte b, or 0.
func (m *Map[K, V]) child(h uint32, b byte) uint32 {
	i := int(h >> kindBits)
	switch h & kindMask {
	case kind4:
		n := m.n4.Ptr(i)
		for j := range int(n.count) {
			if n.keys[j] == b {
				return n.kids[j]
			}
		}

		return 0
	case kind16:
		n := m.n16.Ptr(i)
		if s := slot16(&n.keys, b); s >= 0 {
			return n.kids[s]
		}

		return 0
	case kind48:
		n := m.n48.Ptr(i)
		if s := n.index[b]; s != 0 {
			return n.kids[s-1]
		}

		return 0
	default:
		return m.n256.Ptr(i).kids[b]
	}
}

// children returns an iterator over the branch bytes and the children of
// the inner node h, in the order of the bytes. It reads the record of h as
// it yields, so the caller stops it before a write that changes the
// structure of the map.
func (m *Map[K, V]) children(h uint32) iter.Seq2[byte, uint32] {
	return func(yield func(byte, uint32) bool) {
		i := int(h >> kindBits)
		switch h & kindMask {
		case kind4:
			n := m.n4.Ptr(i)
			for j := range int(n.count) {
				if !yield(n.keys[j], n.kids[j]) {
					return
				}
			}
		case kind16:
			n := m.n16.Ptr(i)
			for j := range int(n.count) {
				if !yield(n.keys[j], n.kids[j]) {
					return
				}
			}
		case kind48:
			n := m.n48.Ptr(i)
			for b := range branches(&n.present) {
				if !yield(b, n.kids[n.index[b]-1]) {
					return
				}
			}
		default:
			n := m.n256.Ptr(i)
			for b := range branches(&n.present) {
				if !yield(b, n.kids[b]) {
					return
				}
			}
		}
	}
}

// setChild replaces the child of the inner node h at the branch byte b with
// c. h has a child at b.
func (m *Map[K, V]) setChild(h uint32, b byte, c uint32) {
	i := int(h >> kindBits)
	switch h & kindMask {
	case kind4:
		n := m.n4.Ptr(i)
		for j := range int(n.count) {
			if n.keys[j] == b {
				n.kids[j] = c

				return
			}
		}
	case kind16:
		n := m.n16.Ptr(i)
		//dokimi:mutate-skip ror-true: setChild runs only for a branch byte with a child, so slot16 finds the byte
		if s := slot16(&n.keys, b); s >= 0 {
			n.kids[s] = c
		}
	case kind48:
		n := m.n48.Ptr(i)
		//dokimi:mutate-skip ror-true: setChild runs only for a branch byte with a child, so the index has its slot
		if s := n.index[b]; s != 0 {
			n.kids[s-1] = c
		}
	default:
		m.n256.Ptr(i).kids[b] = c
	}
}

// replace puts h in the slot of a node: the child of parent at the branch
// byte b, or the root when parent is 0.
func (m *Map[K, V]) replace(parent uint32, b byte, h uint32) {
	if parent == 0 {
		m.root = h

		return
	}
	m.setChild(parent, b, h)
}

// addChild adds the child c at the branch byte b to the inner node h, which
// has no child at b and is the child of parent at pb. addChild first grows a
// node without a free slot to the next kind, and puts the new node in the
// slot of h.
func (m *Map[K, V]) addChild(parent uint32, pb byte, h uint32, b byte, c uint32) {
	i := int(h >> kindBits)
	switch h & kindMask {
	case kind4:
		n := m.n4.Ptr(i)
		if n.count == max4 {
			m.addChild(parent, pb, m.grow4(parent, pb, h), b, c)

			return
		}
		insertSorted(n.keys[:], n.kids[:], int(n.count), b, c)
		n.count++
	case kind16:
		n := m.n16.Ptr(i)
		if n.count == max16 {
			m.addChild(parent, pb, m.grow16(parent, pb, h), b, c)

			return
		}
		insertSorted(n.keys[:], n.kids[:], int(n.count), b, c)
		n.count++
	case kind48:
		n := m.n48.Ptr(i)
		if n.count == max48 {
			m.addChild(parent, pb, m.grow48(parent, pb, h), b, c)

			return
		}
		s := 0
		for n.kids[s] != 0 {
			s++
		}
		n.kids[s] = c
		n.index[b] = uint8(s + 1)
		n.present[b>>6] |= 1 << (b & 63)
		n.count++
	default:
		n := m.n256.Ptr(i)
		n.kids[b] = c
		n.present[b>>6] |= 1 << (b & 63)
		n.count++
	}
}

// removeChild removes the child of the inner node h at the branch byte b.
// h is the child of parent at pb. removeChild shrinks a node whose children
// fall to its shrink count to the kind before, and puts the smaller node in
// the slot of h. It returns the handle of the node.
func (m *Map[K, V]) removeChild(parent uint32, pb byte, h uint32, b byte) uint32 {
	i := int(h >> kindBits)
	switch h & kindMask {
	case kind4:
		n := m.n4.Ptr(i)
		for j := range int(n.count) {
			if n.keys[j] == b {
				removeSorted(n.keys[:], n.kids[:], int(n.count), j)
				n.count--

				break
			}
		}
	case kind16:
		n := m.n16.Ptr(i)
		//dokimi:mutate-skip ror-true: removeChild runs only for a branch byte with a child, so slot16 finds the byte
		if j := slot16(&n.keys, b); j >= 0 {
			removeSorted(n.keys[:], n.kids[:], int(n.count), j)
			n.count--
		}
		if n.count == shrink16 {
			return m.shrink16(parent, pb, h)
		}
	case kind48:
		n := m.n48.Ptr(i)
		//dokimi:mutate-skip ror-true: removeChild runs only for a branch byte with a child, so the index has its slot
		if s := n.index[b]; s != 0 {
			n.kids[s-1] = 0
			n.index[b] = 0
			n.present[b>>6] &^= 1 << (b & 63)
			n.count--
		}
		if n.count == shrink48 {
			return m.shrink48(parent, pb, h)
		}
	default:
		n := m.n256.Ptr(i)
		n.kids[b] = 0
		n.present[b>>6] &^= 1 << (b & 63)
		n.count--
		if n.count == shrink256 {
			return m.shrink256(parent, pb, h)
		}
	}

	return h
}

// grow4 replaces the full node4 h with a node16 of the same header and
// children, and returns the handle of the node16. h is the child of parent
// at pb.
func (m *Map[K, V]) grow4(parent uint32, pb byte, h uint32) uint32 {
	nh := m.alloc(kind16)
	old, n := m.n4.Ptr(int(h>>kindBits)), m.n16.Ptr(int(nh>>kindBits))
	n.header = old.header
	copy(n.keys[:], old.keys[:])
	copy(n.kids[:], old.kids[:])
	m.freeNode(h)
	m.replace(parent, pb, nh)

	return nh
}

// grow16 replaces the full node16 h with a node48 of the same header and
// children, and returns its handle. h is the child of parent at pb.
func (m *Map[K, V]) grow16(parent uint32, pb byte, h uint32) uint32 {
	nh := m.alloc(kind48)
	old, n := m.n16.Ptr(int(h>>kindBits)), m.n48.Ptr(int(nh>>kindBits))
	n.header = old.header
	for j, b := range old.keys {
		n.index[b] = uint8(j + 1)
		n.kids[j] = old.kids[j]
		n.present[b>>6] |= 1 << (b & 63)
	}
	m.freeNode(h)
	m.replace(parent, pb, nh)

	return nh
}

// grow48 replaces the full node48 h with a node256 of the same header and
// children, and returns its handle. h is the child of parent at pb.
func (m *Map[K, V]) grow48(parent uint32, pb byte, h uint32) uint32 {
	nh := m.alloc(kind256)
	old, n := m.n48.Ptr(int(h>>kindBits)), m.n256.Ptr(int(nh>>kindBits))
	n.header, n.present = old.header, old.present
	for b := range branches(&old.present) {
		n.kids[b] = old.kids[old.index[b]-1]
	}
	m.freeNode(h)
	m.replace(parent, pb, nh)

	return nh
}

// shrink16 replaces the node16 h of shrink16 children with a node4 of the
// same header and children, and returns its handle. h is the child of
// parent at pb.
func (m *Map[K, V]) shrink16(parent uint32, pb byte, h uint32) uint32 {
	nh := m.alloc(kind4)
	old, n := m.n16.Ptr(int(h>>kindBits)), m.n4.Ptr(int(nh>>kindBits))
	n.header = old.header
	copy(n.keys[:], old.keys[:shrink16])
	copy(n.kids[:], old.kids[:shrink16])
	m.freeNode(h)
	m.replace(parent, pb, nh)

	return nh
}

// shrink48 replaces the node48 h of shrink48 children with a node16 of the
// same header and children, and returns its handle. h is the child of
// parent at pb.
func (m *Map[K, V]) shrink48(parent uint32, pb byte, h uint32) uint32 {
	nh := m.alloc(kind16)
	old, n := m.n48.Ptr(int(h>>kindBits)), m.n16.Ptr(int(nh>>kindBits))
	n.header = old.header
	j := 0
	for b := range branches(&old.present) {
		n.keys[j], n.kids[j] = b, old.kids[old.index[b]-1]
		j++
	}
	m.freeNode(h)
	m.replace(parent, pb, nh)

	return nh
}

// shrink256 replaces the node256 h of shrink256 children with a node48 of
// the same header and children, and returns its handle. h is the child of
// parent at pb.
func (m *Map[K, V]) shrink256(parent uint32, pb byte, h uint32) uint32 {
	nh := m.alloc(kind48)
	old, n := m.n256.Ptr(int(h>>kindBits)), m.n48.Ptr(int(nh>>kindBits))
	n.header, n.present = old.header, old.present
	j := 0
	for b := range branches(&old.present) {
		n.kids[j] = old.kids[b]
		//nolint:gosec // G115: a node256 shrinks at shrink256 children, which a node48 has room for
		n.index[b] = uint8(j + 1)
		j++
	}
	m.freeNode(h)
	m.replace(parent, pb, nh)

	return nh
}

// leaves returns an iterator over the leaves below the record h, in the
// order of their keys. A leaf yields itself. The iterator reads the records
// as it yields, so the caller stops it before a write that changes the
// structure of the map.
func (m *Map[K, V]) leaves(h uint32) iter.Seq[uint32] {
	return func(yield func(uint32) bool) {
		m.eachLeaf(h, yield)
	}
}

// eachLeaf calls yield for each leaf below the record h in the order of
// their keys: the term of a node before its children, and the children in
// the order of their branch bytes. It reports false once yield returned
// false.
func (m *Map[K, V]) eachLeaf(h uint32, yield func(uint32) bool) bool {
	if h&kindMask == kindLeaf {
		return yield(h)
	}
	if t := m.header(h).term; t != 0 && !yield(t) {
		return false
	}
	for _, c := range m.children(h) {
		if !m.eachLeaf(c, yield) {
			return false
		}
	}

	return true
}

// minLeaf returns the leaf of the smallest key below the record h, or 0
// when h has no key. It descends to the term of each node, or to the child
// of its smallest branch byte.
func (m *Map[K, V]) minLeaf(h uint32) uint32 {
	for h != 0 && h&kindMask != kindLeaf {
		if t := m.header(h).term; t != 0 {
			return t
		}
		h = m.firstChild(h)
	}

	return h
}

// firstChild returns the child of the smallest branch byte of the inner
// node h, or 0 for a node without children.
func (m *Map[K, V]) firstChild(h uint32) uint32 {
	for _, c := range m.children(h) {
		return c
	}

	return 0
}

// slot16 returns the lowest slot of keys whose byte is b, or -1. keys are
// the branch bytes of a node16. slot16 compares b with the eight bytes of a
// word at once. For a branch byte of the node, the lowest match is its slot.
// For another byte, a match is an unused slot. Such a slot is 0 and has no
// child.
func slot16(keys *[max16]byte, b byte) int {
	x := lsb * uint64(b)
	lo := binary.LittleEndian.Uint64(keys[:8]) ^ x
	if z := (lo - lsb) &^ lo & msb; z != 0 {
		return bits.TrailingZeros64(z) >> 3
	}
	hi := binary.LittleEndian.Uint64(keys[8:]) ^ x
	if z := (hi - lsb) &^ hi & msb; z != 0 {
		return 8 + bits.TrailingZeros64(z)>>3
	}

	return -1
}

// insertSorted inserts the branch byte b with the child c into the first n
// slots of keys and kids, which are sorted by byte and have room.
func insertSorted(keys []byte, kids []uint32, n int, b byte, c uint32) {
	j := n
	//dokimi:mutate-skip ror-boundary: insertSorted runs only for a branch byte without a child, so no key equals b
	for j > 0 && keys[j-1] > b {
		keys[j], kids[j] = keys[j-1], kids[j-1]
		j--
	}
	keys[j], kids[j] = b, c
}

// removeSorted removes the slot j from the first n slots of keys and kids,
// and zeroes the slot that the removal frees.
func removeSorted(keys []byte, kids []uint32, n, j int) {
	copy(keys[j:n-1], keys[j+1:n])
	copy(kids[j:n-1], kids[j+1:n])
	keys[n-1], kids[n-1] = 0, 0
}

// branches returns an iterator over the branch bytes whose bits present
// sets, in increasing order.
func branches(present *[4]uint64) iter.Seq[byte] {
	return func(yield func(byte) bool) {
		for w, word := range present {
			for ; word != 0; word &= word - 1 {
				//nolint:gosec // G115: w is below 4 and a bit of a word below 64, so the byte is below 256
				if !yield(byte(w<<6 | bits.TrailingZeros64(word))) {
					return
				}
			}
		}
	}
}

// prefixOf returns the first prefixLen bytes of s, with zeros after a
// shorter s.
func prefixOf[S Key](s S) [prefixLen]byte {
	var p [prefixLen]byte
	copy(p[:], s)

	return p
}
