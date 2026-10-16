// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package radix

import (
	"encoding/binary"
	"slices"

	"go.thesmos.sh/core/arena"
)

// lenBytes is the size of the length of the key at the end of a leaf record.
const lenBytes = 4

// The inline lengths of the three widths of a leaf: the longest key that a
// record of 16, 32 or 64 bytes stores in place.
const (
	inline16 = 12
	inline32 = 28
	inline64 = 60
)

// width selects the records of the leaves of a map. A map keeps its leaves
// in the list of its width, and every leaf of the map has that width.
type width uint8

const (
	// width16 is the width of records of 16 bytes, and of the zero Map.
	width16 width = 0
	// width32 is the width of records of 32 bytes.
	width32 width = 1
	// width64 is the width of records of 64 bytes.
	width64 width = 2
)

// leaf16, leaf32 and leaf64 are the records of the leaves of the three
// widths. A record stores a key of up to its size minus 4 bytes in place.
// For a longer key, its first 8 bytes are the arena.Ref of the region of
// the key, little-endian. Its last 4 bytes are the length of the key,
// little-endian.
type (
	leaf16 [16]byte
	leaf32 [32]byte
	leaf64 [64]byte
)

// record returns the bytes of the record of the leaf h.
func (m *Map[K, V]) record(h uint32) []byte {
	i := int(h >> kindBits)
	switch m.width {
	case width16:
		return m.leaves16.Ptr(i)[:]
	case width32:
		return m.leaves32.Ptr(i)[:]
	default:
		return m.leaves64.Ptr(i)[:]
	}
}

// key returns the key of the leaf h, with a capacity of its length. The key
// is a part of the record of the leaf or of the region of the key.
func (m *Map[K, V]) key(h uint32) []byte {
	rec := m.record(h)
	inline := rec[:len(rec)-lenBytes]
	n := int(binary.LittleEndian.Uint32(rec[len(rec)-lenBytes:]))
	if n <= len(inline) {
		return inline[:n:n]
	}
	return slices.Clip(m.keys.Bytes(arena.Ref(binary.LittleEndian.Uint64(inline)), n))
}

// newLeaf returns the handle of a new leaf of key with value. It reuses a
// record that a delete freed, or appends a record to the list of the width
// of the map. It copies the bytes of key into the record, or into a region
// of the slabs of the map for a key longer than the inline length.
func (m *Map[K, V]) newLeaf(key K, value V) uint32 {
	var i int
	if free := m.spare[kindLeaf]; len(free) != 0 {
		i = int(free[len(free)-1])
		m.spare[kindLeaf] = free[:len(free)-1]
		*m.values.Ptr(i) = value
	} else {
		i = m.values.Len()
		m.values.Append(value)
		switch m.width {
		case width16:
			m.leaves16.Append(leaf16{})
		case width32:
			m.leaves32.Append(leaf32{})
		default:
			m.leaves64.Append(leaf64{})
		}
	}

	//nolint:gosec // G115: a map has at most MaxLen keys
	h := uint32(i)<<kindBits | kindLeaf
	rec := m.record(h)
	inline := rec[:len(rec)-lenBytes]
	//nolint:gosec // G115: Set refuses a key of 2^32 bytes or more
	binary.LittleEndian.PutUint32(rec[len(rec)-lenBytes:], uint32(len(key)))
	if len(key) <= len(inline) {
		copy(inline, key)

		return h
	}
	r := m.keys.Alloc(len(key))
	copy(m.keys.Bytes(r, len(key)), key)
	binary.LittleEndian.PutUint64(inline, uint64(r))

	return h
}

// freeLeaf takes the region of a long key of the leaf h back into the slabs,
// zeroes its record and puts its index on the stack of free leaves. When the
// slabs refuse the region, freeLeaf leaves the record as it is and does not
// put its index on the stack, so no later Set hands out the region again.
func (m *Map[K, V]) freeLeaf(h uint32) {
	rec := m.record(h)
	if !m.freeRegion(rec) {
		return
	}
	clear(rec)
	m.spare[kindLeaf] = append(m.spare[kindLeaf], h>>kindBits)
}

// freeRegion takes the region of the key of the leaf record rec back into
// the slabs when the key is longer than the inline length. It reports false
// when the slabs refuse the region, which only a record that the map did not
// write can cause.
func (m *Map[K, V]) freeRegion(rec []byte) bool {
	inline := rec[:len(rec)-lenBytes]
	n := int(binary.LittleEndian.Uint32(rec[len(rec)-lenBytes:]))
	if n <= len(inline) {
		return true
	}

	return m.keys.Free(arena.Ref(binary.LittleEndian.Uint64(inline)), n) == nil
}

// freeRegions takes the region of every long key of the map back into the
// slabs. Slabs that refuse a region no longer match the leaves, so
// freeRegions then replaces them with empty slabs.
func (m *Map[K, V]) freeRegions() {
	if m.root == 0 {
		return
	}

	refused := false
	for l := range m.leaves(m.root) {
		refused = !m.freeRegion(m.record(l)) || refused
	}
	if refused {
		m.keys = arena.RefSlabs{}
	}
}

// widthOf returns the width of the leaves that store keys of up to
// inlineKeyLen bytes in place. It rounds inlineKeyLen up to 12, 28 or 60
// bytes. A length of 12 or less selects 12, and one above 60 selects 60.
func widthOf(inlineKeyLen int) width {
	if inlineKeyLen <= inline16 {
		return width16
	} else if inlineKeyLen <= inline32 {
		return width32
	}

	return width64
}
