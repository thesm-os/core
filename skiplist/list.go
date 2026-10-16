// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package skiplist

import (
	"bytes"
	"cmp"
	"encoding/binary"
	"math"
	"math/bits"
	"sync/atomic"

	"go.thesmos.sh/core/arena"
	"go.thesmos.sh/core/hash/xxh3"
)

// The layout of a node in the node arena: nodeHeader words, then one link
// for each level of the node. A link is the index of the first word of the
// next node at its level, and 0 is no node. The link back is the index of
// the node before the node at level 0, and 0 is the head. The key and the
// value of a node are consecutive bytes of the data arena.
const (
	// wordKeyOff is the word of the offset of the key in the data arena.
	wordKeyOff = 0
	// wordKeyLen is the word of the length of the key.
	wordKeyLen = 1
	// wordValueLen is the word of the length of the value, which follows
	// the key.
	wordValueLen = 2
	// wordSeqHigh is the word of the high 32 bits of the sequence.
	wordSeqHigh = 3
	// wordSeqLow is the word of the low 32 bits of the sequence.
	wordSeqLow = 4
	// wordPrefixHigh is the word of the high 32 bits of the prefix of the
	// key, which keyPrefix returns.
	wordPrefixHigh = 5
	// wordPrefixLow is the word of the low 32 bits of the prefix of the key.
	wordPrefixLow = 6
	// wordBack is the word of the link back. Writers set it with
	// compare-and-swap, and it can be a node before the node before. A
	// cursor that moves back walks forward from it.
	wordBack = 7
	// nodeHeader is the number of words before the links of a node.
	nodeHeader = 8
	// wordBytes is the size of a word in bytes.
	wordBytes = 4
)

// The bounds of a List.
const (
	// maxHeight is the largest number of levels of a node. The head node,
	// at index 0 of the node arena, has links at every level.
	maxHeight = 20
	// headWords is the number of words of the head node.
	headWords = nodeHeader + maxHeight
	// maxValueLen is the largest length of a value that Cost and MaxCost
	// count, and of a key that MaxCost counts. An arena has at most 2^32 - 1
	// bytes, and an int is smaller on a platform whose int has 32 bits.
	maxValueLen = min(math.MaxUint32, math.MaxInt)
)

// The constants of splitmix64, which derives the two secret words of the
// hash of the heights from the seed of a List.
const (
	splitGamma = 0x9e3779b97f4a7c15
	splitMul1  = 0xbf58476d1ce4e5b9
	splitMul2  = 0x94d049bb133111eb
)

// levelBounds[k-1] is the bound of level k, e^-k times 2^64. The hash of an
// entry passes level k when it is below the bound, so a node has level k
// with a probability of e^-k.
var levelBounds = func() (bounds [maxHeight - 1]uint64) {
	for k := range bounds {
		bounds[k] = uint64(math.Ldexp(math.Exp(-float64(k+1)), 64))
	}

	return bounds
}()

// Config configures a [List]. DataBytes and NodeBytes are required. The
// zero Config is invalid, because its arenas have no bytes.
//
// A node takes 4 bytes for each of its 8 words and its links. The nodes take
// 38.3 bytes on average, and the tallest node takes 112 bytes. [List.Cost]
// returns the bytes that the Add of an entry reserves in each arena, and
// [MaxCost] returns them for the tallest node. An engine that budgets its
// entries with Cost or MaxCost against DataBytes and NodeBytes does not
// repeat the layout of a node.
type Config struct {
	// DataBytes is the capacity of the data arena in bytes, from 1 to
	// 2^32 - 1. The data arena contains the keys and the values.
	DataBytes int

	// NodeBytes is the capacity of the node arena in bytes, from 1 to
	// 2^32 - 1. The node arena contains the nodes.
	NodeBytes int

	// Seed is the seed of the hash of the heights of the nodes. A caller
	// that does not know the seed cannot choose entries of one height.
	Seed uint64
}

// List is an insert-only ordered map of entries: a skip list whose nodes
// link without locks. It orders the entries by key, as bytes.Compare orders
// keys, and for one key from the highest sequence to the lowest.
//
// A List keeps its node words in an [arena.Bump] of uint32 and its keys and
// values in an arena.Bump of bytes. The arenas never grow or move, and both
// are free of pointers. Every key and value that a List returns is valid for
// the life of the List, or until [List.Reset].
//
// # Concurrency
//
// [List.Add], [List.Get], [List.Len], [List.Size], [List.Cost] and
// [List.NewIter] are safe for concurrent use. Reset is not safe concurrently
// with any other method apart from Cost. An Add that returned is visible to
// every Get and to every move of a cursor that starts after it.
//
// # Allocation contract
//
// [New] allocates the List, its two arenas and their arrays. NewIter
// allocates the cursor. The other methods do not allocate.
type List struct {
	// dataArena reserves the bytes of the keys and the values.
	dataArena *arena.Bump[byte]

	// nodeArena reserves the words of the nodes. Its first headWords words
	// are the head node.
	nodeArena *arena.Bump[uint32]

	// data is the array of dataArena.
	data []byte

	// nodes is the array of nodeArena. A writer writes the words of a node
	// before it links the node, and every link is read and written with
	// sync/atomic, so a goroutine that loads a link sees the words of the
	// node that it links.
	nodes []uint32

	// levels is the height of the tallest node, at least 1. A search starts
	// at its top level.
	levels atomic.Uint32

	// count is the number of entries.
	count atomic.Int64

	// secret0 and secret1 are the secret words of the hash of the heights,
	// one for each factor of its product.
	secret0, secret1 uint64

	// resets counts the calls of Reset. An Inserter and a cursor keep the
	// count of their last call, and use no node of an earlier fill.
	resets uint64
}

// New returns an empty [List] with the arenas of cfg. The heights of its
// nodes derive from the Seed of cfg.
//
// Error modes:
//
//   - A DataBytes or a NodeBytes below 1 or above 2^32 - 1 returns
//     [ErrSize].
//
// # Allocation contract
//
// Allocates the List, its two [arena.Bump] values and their arrays.
func New(cfg Config) (*List, error) {
	// A capacity of 2^32 - 1 bytes or less has no bit set above its 32
	// bits, so every offset and every length fits a word.
	if cfg.DataBytes < 1 || cfg.NodeBytes < 1 || uint64(cfg.DataBytes)>>32 != 0 || uint64(cfg.NodeBytes)>>32 != 0 {
		return nil, ErrSize
	}

	seed := cfg.Seed
	secret0 := splitmix64(&seed)
	secret1 := splitmix64(&seed)
	l := &List{
		dataArena: arena.NewBump[byte](cfg.DataBytes),
		nodeArena: arena.NewBump[uint32](headWords + cfg.NodeBytes/wordBytes),
		secret0:   secret0,
		secret1:   secret1,
	}
	l.data, l.nodes = l.dataArena.Values(), l.nodeArena.Values()
	l.nodeArena.Reserve(headWords)
	l.levels.Store(1)

	return l, nil
}

// Add inserts the entry of key, seq and value. It copies key and value into
// the data arena. An Add that returned is visible to every Get and to every
// move of a cursor that starts after it.
//
// Add first searches the levels for the nodes around the entry. It then
// reserves the words of the node and the bytes of its key and value, and
// writes them. It links the node from level 0 up, each level with a
// compare-and-swap of the link of the node before it. When another writer
// changed the link of level 0 first, Add searches every level again. It
// returns ErrExists when that writer linked the same entry. At a higher
// level, Add searches the level again from the node before the entry.
//
// At level 0 Add also moves the link back of the node after the entry to
// the new node, with a compare-and-swap whose result it does not check. A
// writer that links another node between them at the same moment can leave
// that link on an earlier node, and a cursor that moves back repairs it. An
// [Inserter] adds entries in order with shorter searches.
//
// Error modes:
//
//   - An entry with the key and the sequence of an entry of the List
//     returns [ErrExists], and Add reserves nothing when its search finds
//     the entry.
//   - A node that does not fit the node arena returns [ErrFull], and Add
//     reserves nothing.
//   - Bytes that do not fit the data arena return ErrFull. The words of the
//     node remain reserved until [List.Reset].
//
// A writer that adds the entry at the same moment can make Add return
// ErrExists after it reserved its node and its bytes. They remain reserved
// until Reset too.
//
// # Allocation contract
//
// Zero-alloc.
func (l *List) Add(key []byte, seq uint64, value []byte) error {
	var prev [maxHeight]uint32
	_, _, err := l.insert(key, keyPrefix(key), seq, value, nil, &prev)

	return err
}

// Get returns the value and the sequence of the first entry at or after
// the entry (key, seq) when that entry has key. For a snapshot seq, it is
// the newest version of key at or below seq. It reports false when the
// first entry at or after (key, seq) has another key, or when no entry
// follows. The value is valid until [List.Reset], and its capacity is its
// length.
//
// # Allocation contract
//
// Zero-alloc.
func (l *List) Get(key []byte, seq uint64) ([]byte, uint64, bool) {
	_, nd := l.search(key, keyPrefix(key), seq)
	if nd == 0 || !bytes.Equal(l.key(nd), key) {
		return nil, 0, false
	}

	return l.value(nd), uint64(l.nodes[nd+wordSeqHigh])<<32 | uint64(l.nodes[nd+wordSeqLow]), true
}

// Len returns the number of entries.
//
// # Allocation contract
//
// Zero-alloc.
func (l *List) Len() int {
	return int(l.count.Load())
}

// Size returns the bytes of both arenas in use: the bytes of the keys and
// the values, and 4 bytes for each word of a node. It counts the words and
// the bytes that an Add reserved before it returned an error.
//
// # Allocation contract
//
// Zero-alloc.
func (l *List) Size() int {
	return l.dataArena.Len() + wordBytes*(l.nodeArena.Len()-headWords)
}

// Cost returns the bytes that Add of the entry (key, seq), with a value of
// valueLen bytes, reserves in the data arena and in the node arena of l. The
// results have the order of the fields of [Config]. The height of the node
// derives from the entry and the seed of l, so Cost is exact: an Add that
// succeeds grows [List.Size] by dataBytes + nodeBytes. An Add that finds the
// entry before it reserves takes nothing, and one that returns ErrFull for
// the data arena keeps the words of the node.
//
// dataBytes is the length of key plus valueLen. nodeBytes is 4 bytes for
// each of the 8 words of the header of the node and each of its links. A
// valueLen below 0 counts as 0, and one above 2^32 - 1, the largest capacity
// of an arena, counts as 2^32 - 1.
//
// # Concurrency
//
// Safe for concurrent use with every method of l, Reset included. Cost reads
// only the seed of l.
//
// # Allocation contract
//
// Zero-alloc.
func (l *List) Cost(key []byte, seq uint64, valueLen int) (dataBytes, nodeBytes int) {
	return len(key) + min(max(valueLen, 0), maxValueLen), wordBytes * (nodeHeader + l.height(key, seq))
}

// Reset removes every entry and keeps the arenas. It zeroes the bytes and
// the words in use, so it takes time in proportion to [List.Size]. Every key
// and value that the List returned before is invalid after Reset. A cursor
// of the List is on no entry until a First, a Last, a SeekGE or a SeekLT
// moves it, and an [Inserter] searches its next Add from the head.
//
// # Allocation contract
//
// Zero-alloc.
func (l *List) Reset() {
	l.dataArena.Reset()
	l.nodeArena.Reset()
	l.nodeArena.Reserve(headWords)
	l.levels.Store(1)
	l.count.Store(0)
	//dokimi:mutate-skip uoi-incdec: a cursor and an Inserter compare the count for equality alone, and a decrement changes it as an increment does
	l.resets++
}

// NewIter returns a cursor over the entries of the List. The cursor is on
// no entry until [Iter.First], [Iter.Last], [Iter.SeekGE] or [Iter.SeekLT]
// moves it.
//
// # Allocation contract
//
// Allocates the cursor.
func (l *List) NewIter() *Iter {
	return &Iter{list: l}
}

// insert inserts the entry of key, seq and value, with the contract and the
// errors of Add, and returns its node and the height of its node. prefix is
// the prefix of key. from is nil or the nodes of the hint of an Inserter,
// as findSplice takes them. prev contains 0, the head, at each level on the
// call. insert leaves in it the last node before the entry at each level of
// its search.
func (l *List) insert(
	key []byte,
	prefix, seq uint64,
	value []byte,
	from, prev *[maxHeight]uint32,
) (uint32, int, error) {
	var (
		next [maxHeight]uint32
		node []uint32
		at   uint32
		h    int
	)

	// Level 0 decides whether the entry joins the List. A failed
	// compare-and-swap there searches every level again, so a writer that
	// linked the same entry first makes the search find it.
	for {
		if l.findSplice(key, prefix, seq, from, prev, &next) {
			return 0, 0, ErrExists
		}
		if node == nil {
			h = l.height(key, seq)
			nd, ok := l.nodeArena.Reserve(nodeHeader + h)
			if !ok {
				return 0, 0, ErrFull
			}
			off, ok := l.dataArena.Reserve(len(key) + len(value))
			if !ok {
				return 0, 0, ErrFull
			}

			copy(l.data[off:], key)
			copy(l.data[off+len(key):], value)
			node = l.nodes[nd : nd+nodeHeader+h]
			//nolint:gosec // G115: the data arena has at most 2^32 - 1 bytes
			node[wordKeyOff], node[wordKeyLen], node[wordValueLen] = uint32(off), uint32(len(key)), uint32(len(value))
			//nolint:gosec // G115: the conversions take the two halves of the sequence
			node[wordSeqHigh], node[wordSeqLow] = uint32(seq>>32), uint32(seq)
			//nolint:gosec // G115: the conversions take the two halves of the prefix
			node[wordPrefixHigh], node[wordPrefixLow] = uint32(prefix>>32), uint32(prefix)
			//nolint:gosec // G115: the node arena has at most 2^30 + headWords words
			at = uint32(nd)
			l.raise(h)
		}
		node[nodeHeader], node[wordBack] = next[0], prev[0]
		after := next[0]
		if atomic.CompareAndSwapUint32(&l.nodes[prev[0]+nodeHeader], after, at) {
			// The link back of the node after moves from the node before to
			// the new node. A writer that moved it first leaves it on another
			// node before the node after, which a cursor that moves back
			// walks forward from.
			//dokimi:mutate-skip ror-true: no move reads the link back of the head, node 0, so a compare-and-swap of it changes no result
			if after != 0 {
				atomic.CompareAndSwapUint32(&l.nodes[after+wordBack], prev[0], at)
			}

			break
		}
	}

	// A higher level searches its splice again from the node before the
	// entry before each compare-and-swap. The List has no other node with
	// key and seq, because level 0 links the entry.
	for level := 1; level < h; level++ {
		for {
			prev[level], next[level], _ = l.splice(key, prefix, seq, prev[level], 0, level)
			node[nodeHeader+level] = next[level]
			if atomic.CompareAndSwapUint32(&l.nodes[prev[level]+nodeHeader+uint32(level)], next[level], at) {
				break
			}
		}
	}
	l.count.Add(1)

	return at, h, nil
}

// raise raises the height of the List to h when h is above it. When another
// writer raised it first, raise compares h with the height that the other
// writer set.
func (l *List) raise(h int) {
	for {
		top := l.levels.Load()
		//nolint:gosec // G115: a height is at most maxHeight
		//dokimi:mutate-skip ror-boundary: a compare-and-swap of the height with itself changes nothing
		if uint32(h) <= top || l.levels.CompareAndSwap(top, uint32(h)) {
			return
		}
	}
}

// findSplice searches every level from the top for the nodes around the
// entry (key, seq): prev[level] is the last node before it at the level,
// and next[level] the first node after it, or 0. prefix is the prefix of
// key. The levels above the top of the search keep the head node and no
// node. findSplice reports true when the List contains an entry with key and
// seq.
//
// from is nil or the nodes of the hint of an Inserter. Each of them is a node
// before the entry, and the node of a level is at or after the nodes of the
// levels above it. The head is therefore the node of from at the levels above
// the last one that has another node. While the search of every level above
// ends where it started, findSplice searches each level from the node of from,
// which is at or after that end. A search that moves past its start can end
// past the nodes of from below it, so findSplice searches the levels below
// from the nodes that it finds, as a search from the head does.
func (l *List) findSplice(key []byte, prefix, seq uint64, from, prev, next *[maxHeight]uint32) bool {
	var before, stop uint32
	for level := int(l.levels.Load()) - 1; level >= 0; level-- {
		if from != nil {
			before = from[level]
		}
		var exists bool
		prev[level], next[level], exists = l.splice(key, prefix, seq, before, stop, level)
		if exists {
			return true
		}
		if prev[level] != before {
			from = nil
		}
		//dokimi:mutate-skip sbr-delete: a search of each level from the head finds the same splice, so only the time of a search changes
		before, stop = prev[level], next[level]
	}

	return false
}

// splice moves along the level from the node before, and returns the last
// node before the entry (key, seq) and the node after it, or 0. prefix is the
// prefix of key. It reports true, with the node of the entry as the node
// after it, when it finds an entry with key and seq.
//
// stop is a node after the entry, which the search of the level above
// compared, or 0, the end of the level. splice stops at stop without a
// comparison. Every node of the level above is a node of the level too, and
// stop is after before, so the walk from before ends at stop at the latest.
func (l *List) splice(key []byte, prefix, seq uint64, before, stop uint32, level int) (prev, next uint32, exists bool) {
	for {
		//nolint:gosec // G115: a level is below maxHeight
		next = atomic.LoadUint32(&l.nodes[before+nodeHeader+uint32(level)])
		if next == stop {
			return before, next, false
		}
		c := l.compare(key, prefix, seq, next)
		if c <= 0 {
			return before, next, c == 0
		}
		before = next
	}
}

// search searches every level from the top for the entry (key, seq), and
// returns the last node before it and the first node at or after it. before
// is 0, the head node, when no node precedes the entry, and next is 0 when
// no node follows it. prefix is the prefix of key. A level stops without a
// comparison at the node at which the level above stopped, or at 0, the end
// of the top level. The walk of a level ends there at the latest, as the
// walk of splice does.
func (l *List) search(key []byte, prefix, seq uint64) (before, next uint32) {
	var stop uint32
	for level := l.levels.Load(); level > 0; level-- {
		for {
			next = atomic.LoadUint32(&l.nodes[before+nodeHeader+level-1])
			if next == stop || l.compare(key, prefix, seq, next) <= 0 {
				break
			}
			before = next
		}
		//dokimi:mutate-skip sbr-delete: a level that compares the node at which the level above stopped finds the same node
		stop = next
	}

	return before, next
}

// last returns the last node of the List, or 0 for a List without entries.
// It follows the links of each level from the top until the next node is 0.
func (l *List) last() uint32 {
	var nd uint32
	for level := l.levels.Load(); level > 0; level-- {
		for {
			next := atomic.LoadUint32(&l.nodes[nd+nodeHeader+level-1])
			if next == 0 {
				break
			}
			nd = next
		}
	}

	return nd
}

// compare returns -1, 0 or +1 for the order of the entry (key, seq) and the
// entry of the node nd: by key, then by sequence from the highest to the
// lowest. prefix is the prefix of key. Keys whose prefixes differ order as
// their prefixes order, so compare reads the key of nd only when the two
// prefixes are equal.
func (l *List) compare(key []byte, prefix, seq uint64, nd uint32) int {
	np := uint64(l.nodes[nd+wordPrefixHigh])<<32 | uint64(l.nodes[nd+wordPrefixLow])
	//dokimi:mutate-skip ror-false,sbr-delete: bytes.Compare of the whole keys returns the order of the prefixes too
	if prefix != np {
		return cmp.Compare(prefix, np)
	}
	if c := bytes.Compare(key, l.key(nd)); c != 0 {
		return c
	}

	return cmp.Compare(uint64(l.nodes[nd+wordSeqHigh])<<32|uint64(l.nodes[nd+wordSeqLow]), seq)
}

// key returns the key of the node nd, with a capacity of its length.
func (l *List) key(nd uint32) []byte {
	//dokimi:mutate-skip aor: wordKeyOff is 0, so nd-wordKeyOff equals nd+wordKeyOff
	off := l.nodes[nd+wordKeyOff]
	end := off + l.nodes[nd+wordKeyLen]

	return l.data[off:end:end]
}

// value returns the value of the node nd, with a capacity of its length.
func (l *List) value(nd uint32) []byte {
	//dokimi:mutate-skip aor: wordKeyOff is 0, so nd-wordKeyOff equals nd+wordKeyOff
	keyOff := l.nodes[nd+wordKeyOff]
	off := keyOff + l.nodes[nd+wordKeyLen]
	end := off + l.nodes[nd+wordValueLen]

	return l.data[off:end:end]
}

// height returns the number of levels of the node of the entry (key, seq):
// 1, plus 1 for each level k from 1 to maxHeight-1 whose bound the hash of
// the entry is below. The hash multiplies two factors into 128 bits and
// folds the halves with an exclusive or. Each factor combines a part of the
// entry with a secret word of its own, so a caller that does not know the
// seed cannot make a factor 0 or choose entries of one height.
func (l *List) height(key []byte, seq uint64) int {
	hi, lo := bits.Mul64(xxh3.Hash(key)^l.secret0, seq^l.secret1)
	x := hi ^ lo
	h := 1
	for h < maxHeight && x < levelBounds[h-1] {
		h++
	}

	return h
}

// MaxCost returns the most bytes that Add of an entry with a key of keyLen
// bytes and a value of valueLen bytes reserves in the data arena and in the
// node arena of a [List] of any seed. It is the cost of the entry in a node
// of the largest height. [List.Cost] of such an entry returns the same
// dataBytes and at most nodeBytes. The results have the order of the fields
// of [Config].
//
// dataBytes is keyLen plus valueLen. nodeBytes is 112, because a node of the
// largest height has 8 words of header and 20 links of 4 bytes each. A
// keyLen or a valueLen below 0 counts as 0. A keyLen or a valueLen above
// 2^32 - 1 counts as 2^32 - 1, because an arena has at most 2^32 - 1 bytes.
//
// # Concurrency
//
// Safe for concurrent use. MaxCost does not read a List.
//
// # Allocation contract
//
// Zero-alloc.
func MaxCost(keyLen, valueLen int) (dataBytes, nodeBytes int) {
	return min(max(keyLen, 0), maxValueLen) + min(max(valueLen, 0), maxValueLen), wordBytes * (nodeHeader + maxHeight)
}

// splitmix64 advances the state of a splitmix64 generator and returns its
// next output. New takes the secret words of the hash of the heights from
// the first two outputs for the seed.
func splitmix64(state *uint64) uint64 {
	*state += splitGamma
	z := *state
	z = (z ^ z>>30) * splitMul1
	z = (z ^ z>>27) * splitMul2

	return z ^ z>>31
}

// keyPrefix returns the prefix of key: its first 8 bytes as a big-endian
// integer, padded with zero bytes for a key of fewer than 8 bytes. Two keys
// whose prefixes differ order as their prefixes order. Two keys with equal
// prefixes can differ in a later byte or in their length. It does not
// allocate.
func keyPrefix(key []byte) uint64 {
	var b [8]byte
	copy(b[:], key)

	return binary.BigEndian.Uint64(b[:])
}
