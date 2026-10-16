// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package radix

import (
	"cmp"
	"iter"

	"go.thesmos.sh/core/arena"
)

// MaxLen is the largest number of keys that a [Map] stores. The index of a
// record is the high 29 bits of its handle. A map with keys has at most one
// inner node per key, so no kind of record needs more indexes than the
// leaves.
const MaxLen = 1 << 29

// Key constrains the keys of a [Map] to strings and byte slices. A Map
// orders keys by their bytes, as bytes.Compare does.
type Key interface {
	~string | ~[]byte
}

// Config configures a [Map]. The zero Config is the configuration of the
// zero Map.
type Config struct {
	// Limit bounds the number of keys. It also bounds the memory of a map
	// that a caller fills from untrusted input. A Limit of 0 or less is
	// MaxLen, and a Limit above MaxLen is MaxLen too.
	Limit int

	// InlineKeyLen is the length up to which a leaf stores a key in place.
	// New rounds it up to 12, 28 or 60 bytes. Leaves of 16, 32 and 64 bytes
	// store keys of those lengths. A longer key keeps its bytes in a region
	// of the arena.RefSlabs of the map. An InlineKeyLen of 12 or less is 12,
	// and one above 60 is 60.
	InlineKeyLen int
}

// Map is an adaptive radix tree from byte-string keys to values. One
// descent from the root finds the longest stored prefix of a key, or every
// stored prefix of the key.
//
// An inner node has room for 4, 16, 48 or 256 children and grows and
// shrinks with its children. It skips the key bytes that every key below it
// shares. The records of the nodes and the leaves are in the chunks of
// arena.List values. uint32 handles link them, so the records contain no
// pointer. A key longer than the inline length of the map keeps its bytes in
// a region of an arena.RefSlabs. The values are in an arena.List at the
// index of their leaf.
//
// An empty key is a valid key. It is the term of the root and a prefix of
// every key.
//
// The zero Map is empty and ready to use. It has the limit MaxLen, and its
// leaves store keys of up to 12 bytes in place.
//
// # Concurrency
//
// A Map is not safe for concurrent use. Any number of goroutines may read a
// Map that no goroutine writes.
//
// # Allocation contract
//
// [New] allocates the map. [Map.Get], [Map.LongestPrefix], [Map.Len] and a
// range loop over [Map.Prefixes], [Map.Scan] or [Map.All] do not allocate.
// [Map.Set], [Map.Delete] and [Map.Reset] allocate only to grow the lists of
// records, the stacks of free records, and the slabs and free lists for long
// keys. A map whose number of keys does not grow reuses the records and the
// regions that its deletes free.
type Map[K Key, V any] struct {
	// spare is a stack of the indexes of the free records of each kind.
	spare [kinds][]uint32

	// keys contains the regions of the keys longer than the inline length.
	keys arena.RefSlabs

	// n4, n16, n48 and n256 are the records of the inner nodes.
	n4   arena.List[node4]
	n16  arena.List[node16]
	n48  arena.List[node48]
	n256 arena.List[node256]

	// leaves16, leaves32 and leaves64 are the records of the leaves. The map
	// uses the list of its width.
	leaves16 arena.List[leaf16]
	leaves32 arena.List[leaf32]
	leaves64 arena.List[leaf64]

	// values are the values of the keys, at the index of their leaf.
	values arena.List[V]

	// len is the number of keys.
	len int

	// limit is the Limit of the Config, or 0 when the Config sets none. room
	// caps it at MaxLen.
	limit int

	// writes counts the Sets of new keys, the Deletes of keys and the
	// Resets. An iteration ends when the count grows.
	writes uint64

	// root is the handle of the root node. It is 0 before the first Set and
	// after a Reset. The root does not skip bytes.
	root uint32

	// width is the width of the leaves.
	width width
}

// prefixWalk is the state of a descent along the stored prefixes of a key.
type prefixWalk struct {
	// depth is the position in the key of the first skipped byte of h. It
	// is one past the branch byte of the parent of h.
	depth int

	// checked is the length of the last term that matched the key. The
	// bytes of the key before it match the stored keys on the path.
	checked int

	// h is the record that the descent visits next, or 0 at its end.
	h uint32
}

// New returns an empty [Map] over cfg.
//
// # Allocation contract
//
// Allocates the map.
func New[K Key, V any](cfg Config) *Map[K, V] {
	return &Map[K, V]{width: widthOf(cfg.InlineKeyLen), limit: max(cfg.Limit, 0)}
}

// Get returns the value of key and reports whether the map contains key.
//
// Get passes over the skipped bytes of each node on its path without
// reading them, and compares key with the stored key of the leaf where it
// ends. A node that skips past the end of key ends the descent at its term.
//
// # Allocation contract
//
// Zero-alloc.
func (m *Map[K, V]) Get(key K) (V, bool) {
	h, depth := m.root, 0
	for h&kindMask > kindLeaf {
		hd := m.header(h)
		depth += int(hd.skip)
		if depth >= len(key) {
			h = hd.term

			break
		}
		h = m.child(h, key[depth])
		depth++
	}
	if h != 0 && string(m.key(h)) == string(key) {
		return m.values.At(int(h >> kindBits)), true
	}

	var zero V

	return zero, false
}

// Set stores value under key. It returns the previous value and true when
// the map contained key. Set copies the bytes of key.
//
// Set compares every skipped byte on its path. It reads a skipped byte
// beyond the first 8 of a node from the key of the smallest leaf below the
// node. A new key that differs from the skipped bytes of a node splits the
// node.
//
// Error modes:
//
//   - A key of 2^32 bytes or more returns [ErrTooLong], and the map is
//     unchanged.
//   - A new key in a map that contains its limit of keys returns [ErrFull],
//     and the map is unchanged.
//
// # Allocation contract
//
// Zero-alloc when the stacks of free records and the regions of long keys
// have room. Otherwise Set allocates when it grows a list of records, a
// stack or a free list. It also allocates a slab for long keys when the
// slabs are full.
func (m *Map[K, V]) Set(key K, value V) (V, bool, error) {
	var zero V
	if uint64(len(key))>>32 != 0 {
		return zero, false, ErrTooLong
	}
	if m.root == 0 {
		m.root = m.alloc(kind4)
	}

	parent, pb, h, depth := uint32(0), byte(0), m.root, 0
	for h&kindMask != kindLeaf {
		hd := m.header(h)
		//dokimi:mutate-skip ror-true: matched returns 0 for a node that skips no bytes, and the check saves the call
		if skip := int(hd.skip); skip != 0 {
			if p := m.matched(h, depth, key); p < skip {
				return zero, false, m.splitSkip(parent, pb, h, depth, p, key, value)
			}
			depth += skip
		}
		if depth == len(key) {
			if hd.term != 0 {
				return m.swap(hd.term, value), true, nil
			}
			if err := m.room(); err != nil {
				return zero, false, err
			}
			hd.term = m.newLeaf(key, value)
			m.len++
			m.writes++

			return zero, false, nil
		}
		b := key[depth]
		c := m.child(h, b)
		if c == 0 {
			if err := m.room(); err != nil {
				return zero, false, err
			}
			m.addChild(parent, pb, h, b, m.newLeaf(key, value))
			m.len++
			m.writes++

			return zero, false, nil
		}
		parent, pb, h = h, b, c
		depth++
	}

	return m.setLeaf(parent, pb, h, depth, key, value)
}

// Delete removes key. It returns the removed value and true when the map
// contained key. Delete zeroes the value in the map, so the map does not
// keep what the value referenced.
//
// Delete passes over the skipped bytes of each node on its path without
// reading them, and compares key with the stored key of the leaf where it
// ends. It replaces a node other than the root that keeps one item with that
// item. It shrinks a node whose children fall to its shrink count to the
// kind before.
//
// # Allocation contract
//
// Zero-alloc when the stacks of free records and the free lists of the
// regions have room, as in a map whose number of keys does not grow.
func (m *Map[K, V]) Delete(key K) (V, bool) {
	var zero V
	parent, pb, h, depth := uint32(0), byte(0), m.root, 0
	for h != 0 {
		hd := m.header(h)
		depth += int(hd.skip)
		if depth >= len(key) {
			t := hd.term
			if t == 0 || string(m.key(t)) != string(key) {
				return zero, false
			}
			hd.term = 0
			v := m.release(t)
			m.collapse(parent, pb, h)

			return v, true
		}
		b := key[depth]
		c := m.child(h, b)
		if c&kindMask == kindLeaf {
			if string(m.key(c)) != string(key) {
				return zero, false
			}
			v := m.release(c)
			m.collapse(parent, pb, m.removeChild(parent, pb, h, b))

			return v, true
		}
		parent, pb, h = h, b, c
		depth++
	}

	return zero, false
}

// Len returns the number of keys in the map.
//
// # Allocation contract
//
// Zero-alloc.
func (m *Map[K, V]) Len() int {
	return m.len
}

// Reset removes every key and ends every iteration in progress. It keeps the
// chunks of the records and the slabs of the long keys for later keys. It
// takes the region of each long key back into the slabs and zeroes every
// record and every value, so it runs in O(n).
//
// # Allocation contract
//
// Zero-alloc when the free lists of the regions have room for the regions
// of the long keys of the map.
func (m *Map[K, V]) Reset() {
	m.freeRegions()
	m.n4.Truncate(0)
	m.n16.Truncate(0)
	m.n48.Truncate(0)
	m.n256.Truncate(0)
	m.leaves16.Truncate(0)
	m.leaves32.Truncate(0)
	m.leaves64.Truncate(0)
	m.values.Truncate(0)
	for k := range m.spare {
		m.spare[k] = m.spare[k][:0]
	}
	m.root, m.len = 0, 0
	m.writes++
}

// LongestPrefix returns the length n and the value of the longest stored key
// that is a prefix of key. key[:n] is that stored key, and n is the last
// length that [Map.Prefixes] yields. ok is false when no stored key is a
// prefix of key.
//
// # Allocation contract
//
// Zero-alloc.
func (m *Map[K, V]) LongestPrefix(key K) (n int, value V, ok bool) {
	w := prefixWalk{h: m.root}
	last := uint32(0)
	for t := m.nextPrefix(key, &w); t != 0; t = m.nextPrefix(key, &w) {
		last = t
	}
	if last == 0 {
		return
	}

	return len(m.key(last)), m.values.At(int(last >> kindBits)), true
}

// Prefixes returns an iterator over every stored key that is a prefix of
// key. It yields the length n and the value of each from the shortest to the
// longest, and key[:n] is the stored key.
//
// The iterator descends once. It compares each term on the path with key
// from the end of the last term that matched, so it compares each byte of
// key with a stored key at most once. A term that differs from key ends the
// descent, because every term below it differs too. A Set of a new key, a
// Delete or a Reset in the loop body ends the iteration after the body
// returns.
//
// # Allocation contract
//
// Zero-alloc for a range loop that the compiler inlines the iterator into.
func (m *Map[K, V]) Prefixes(key K) iter.Seq2[int, V] {
	return func(yield func(int, V) bool) {
		writes := m.writes
		w := prefixWalk{h: m.root}
		for t := m.nextPrefix(key, &w); t != 0; t = m.nextPrefix(key, &w) {
			if !yield(len(m.key(t)), m.values.At(int(t>>kindBits))) || m.writes > writes {
				return
			}
		}
	}
}

// Scan returns an iterator over every stored key that starts with prefix.
// It yields the keys in the order of their bytes. Each yielded key is valid
// until the yield returns or the loop body writes to the map.
//
// The iterator descends to the node or the leaf whose subtree contains the
// keys with the prefix. It compares prefix with the key of the smallest
// leaf below it and then walks the subtree. The walk yields the term of a
// node before its children, and the children in the order of their branch
// bytes. It recurses once per level, so its depth is the height of the
// tree. A Set of a new key, a Delete or a Reset in the loop body ends the
// iteration after the body returns. A Set that replaces a value does not
// end it.
//
// # Allocation contract
//
// Zero-alloc for a range loop that the compiler inlines the iterator into.
func (m *Map[K, V]) Scan(prefix K) iter.Seq2[[]byte, V] {
	return func(yield func([]byte, V) bool) {
		h := m.cover(prefix)
		l := m.minLeaf(h)
		if l == 0 {
			return
		}
		if lk := m.key(l); len(lk) < len(prefix) || string(lk[:len(prefix)]) != string(prefix) {
			return
		}

		writes := m.writes
		for l := range m.leaves(h) {
			if !yield(m.key(l), m.values.At(int(l>>kindBits))) || m.writes > writes {
				return
			}
		}
	}
}

// All returns an iterator over every key in the order of their bytes. It is
// [Map.Scan] with an empty prefix.
//
// # Allocation contract
//
// As Scan.
func (m *Map[K, V]) All() iter.Seq2[[]byte, V] {
	var empty K

	return m.Scan(empty)
}

// nextPrefix moves w along the descent of key to the next stored key that is
// a prefix of key. It returns the leaf of that key, or 0 once no prefix
// follows, and a caller stops the walk at 0. nextPrefix passes over the
// skipped bytes of each node. It compares each term with key from the end of
// the last term that matched, so it compares each byte of key with a stored
// key at most once. A term that differs from key ends the walk, because
// every term below it differs too. nextPrefix finds the record after a term
// before it returns the term. A caller must stop the walk after a write that
// changes the structure of the map.
func (m *Map[K, V]) nextPrefix(key K, w *prefixWalk) uint32 {
	for w.h != 0 {
		h := w.h
		if h&kindMask == kindLeaf {
			w.h = 0
			if lk := m.key(h); len(lk) <= len(key) && string(lk[w.checked:]) == string(key[w.checked:len(lk)]) {
				return h
			}

			return 0
		}

		hd := m.header(h)
		w.depth += int(hd.skip)
		depth := w.depth
		if depth > len(key) {
			return 0
		}
		t := hd.term
		if depth == len(key) {
			w.h = 0
		} else {
			w.h = m.child(h, key[depth])
			w.depth++
		}
		if t != 0 {
			if string(m.key(t)[w.checked:]) != string(key[w.checked:depth]) {
				return 0
			}
			//dokimi:mutate-skip sbr-delete: a later term compares the bytes before depth again, and they match
			w.checked = depth

			return t
		}
	}

	return 0
}

// room returns ErrFull when the map contains its limit of keys. A limit of 0
// or above MaxLen counts as MaxLen.
func (m *Map[K, V]) room() error {
	if m.len >= min(cmp.Or(m.limit, MaxLen), MaxLen) {
		return ErrFull
	}

	return nil
}

// swap stores value in the leaf t and returns the value that it replaces.
func (m *Map[K, V]) swap(t uint32, value V) V {
	p := m.values.Ptr(int(t >> kindBits))
	old := *p
	*p = value

	return old
}

// release removes the leaf t from the map. It returns the value of t,
// zeroes the value in the map and frees the leaf.
func (m *Map[K, V]) release(t uint32) V {
	p := m.values.Ptr(int(t >> kindBits))
	v := *p
	var zero V
	*p = zero
	m.freeLeaf(t)
	m.len--
	m.writes++

	return v
}

// matched returns the number of skipped bytes of the inner node h that match
// key from depth on. It compares the bytes that the header keeps and reads
// the rest from the key of the smallest leaf below h.
func (m *Map[K, V]) matched(h uint32, depth int, key K) int {
	hd := m.header(h)
	n := min(int(hd.skip), len(key)-depth)
	i := 0
	for i < min(n, prefixLen) && hd.prefix[i] == key[depth+i] {
		i++
	}
	//dokimi:mutate-skip sbr-delete,lcr-left,lcr-right,ror-false: the loop below returns the same count, after a search of the smallest leaf
	if i < prefixLen ||
		//dokimi:mutate-skip ror-boundary,ror-false: the loop below returns the same count
		n <= prefixLen {

		return i
	}
	lk := m.key(m.minLeaf(h))
	for i < n && lk[depth+i] == key[depth+i] {
		i++
	}

	return i
}

// splitSkip stores a new key that matches the first p skipped bytes of the
// inner node h from depth on and then ends or differs. h is the child of
// parent at pb. splitSkip puts a new node4 in the slot of h. The new node
// skips those p bytes, and h keeps the skipped bytes after its branch byte
// in the new node.
func (m *Map[K, V]) splitSkip(parent uint32, pb byte, h uint32, depth, p int, key K, value V) error {
	if err := m.room(); err != nil {
		return err
	}

	// The skipped bytes of h are in its header, or in every key below h.
	hd := m.header(h)
	skip := int(hd.skip)
	var sk []byte
	//dokimi:mutate-skip ror-boundary: the key of the smallest leaf has the 8 bytes of the prefix there
	if skip <= prefixLen {
		sk = hd.prefix[:skip]
	} else {
		sk = m.key(m.minLeaf(h))[depth : depth+skip]
	}
	branch, rest := sk[p], prefixOf(sk[p+1:])

	x := m.alloc(kind4)
	leaf := m.newLeaf(key, value)
	hd = m.header(h)
	//nolint:gosec // G115: a skip is shorter than a key, which has fewer than 2^32 bytes
	hd.skip, hd.prefix = uint32(skip-p-1), rest
	n := m.n4.Ptr(int(x >> kindBits))
	//nolint:gosec // G115: p is below the skip of h
	n.skip, n.prefix = uint32(p), prefixOf(key[depth:depth+p])
	insertSorted(n.keys[:], n.kids[:], 0, branch, h)
	n.count = 1
	if depth+p == len(key) {
		n.term = leaf
	} else {
		insertSorted(n.keys[:], n.kids[:], 1, key[depth+p], leaf)
		n.count = 2
	}
	m.replace(parent, pb, x)
	m.len++
	m.writes++

	return nil
}

// setLeaf stores value under key after the descent of key ended at the leaf
// h at depth. h is the child of parent at pb. setLeaf replaces the value of h
// when its key is key. Otherwise it puts a new node4 in the slot of h. The
// new node skips the bytes that both keys share from depth on, and h and the
// leaf of key are its children or its term.
func (m *Map[K, V]) setLeaf(parent uint32, pb byte, h uint32, depth int, key K, value V) (V, bool, error) {
	var zero V
	lk := m.key(h)
	if string(lk) == string(key) {
		return m.swap(h, value), true, nil
	}
	if err := m.room(); err != nil {
		return zero, false, err
	}

	p := depth
	for p < len(lk) && p < len(key) && lk[p] == key[p] {
		p++
	}
	// The branch byte of h comes from its key before newLeaf can move its
	// record.
	var lb byte
	if p < len(lk) {
		lb = lk[p]
	}

	x := m.alloc(kind4)
	leaf := m.newLeaf(key, value)
	n := m.n4.Ptr(int(x >> kindBits))
	//nolint:gosec // G115: the shared bytes are fewer than the bytes of a key
	n.skip, n.prefix = uint32(p-depth), prefixOf(key[depth:p])
	n.count = 1
	if p == len(lk) {
		n.term = h
		insertSorted(n.keys[:], n.kids[:], 0, key[p], leaf)
	} else if p == len(key) {
		n.term = leaf
		insertSorted(n.keys[:], n.kids[:], 0, lb, h)
	} else {
		insertSorted(n.keys[:], n.kids[:], 0, lb, h)
		insertSorted(n.keys[:], n.kids[:], 1, key[p], leaf)
		n.count = 2
	}
	m.replace(parent, pb, x)
	m.len++
	m.writes++

	return zero, false, nil
}

// collapse runs after the inner node h lost an item. It removes h when h is
// not the root and keeps one item. h is the child of parent at pb, and
// parent 0 means that h is the root. collapse puts the term of a node without
// children in the slot of the node. It merges a node of one child and no
// term into the child. The child then skips the skipped bytes of h, the
// branch byte and its own skipped bytes.
func (m *Map[K, V]) collapse(parent uint32, pb byte, h uint32) {
	if parent == 0 {
		return
	}

	hd := m.header(h)
	if hd.count == 0 {
		m.replace(parent, pb, hd.term)
		m.freeNode(h)
	} else if hd.count == 1 && hd.term == 0 {
		// A node of one child is a node4.
		n := m.n4.Ptr(int(h >> kindBits))
		c := n.kids[0]
		if c&kindMask != kindLeaf {
			ch := m.header(c)
			var p [prefixLen]byte
			k := copy(p[:], n.prefix[:min(int(n.skip), prefixLen)])
			if k < prefixLen {
				p[k] = n.keys[0]
				copy(p[k+1:], ch.prefix[:min(int(ch.skip), prefixLen)])
			}
			ch.skip, ch.prefix = n.skip+1+ch.skip, p
		}
		m.replace(parent, pb, c)
		m.freeNode(h)
	}
}

// cover returns the node or the leaf whose subtree contains every stored
// key that starts with prefix, or 0 when no stored key can. It compares only
// the branch bytes on its path, so the caller compares prefix with a key
// below the result.
func (m *Map[K, V]) cover(prefix K) uint32 {
	h, depth := m.root, 0
	for h != 0 && h&kindMask != kindLeaf {
		depth += int(m.header(h).skip)
		if depth >= len(prefix) {
			return h
		}
		h = m.child(h, prefix[depth])
		depth++
	}

	return h
}
