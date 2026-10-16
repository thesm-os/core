// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package skiplist

import (
	"context"
	"sync/atomic"
)

// Iter is a cursor over the entries of a [List], in the order of the List.
// A move positions the cursor on an entry or exhausts it. A cursor yields
// the entries that a move finds linked: every entry whose Add returned before
// the move, and the entries that writers link while it moves.
//
// A cursor moves forward along the links of level 0, and [Iter.Prev] moves
// back along the link back of the current node. A link back can lag behind
// a writer that links a node at the same moment, so Prev walks forward from
// it to the node before the current one. [Iter.SeekGE] of a cursor on an
// entry before its target first compares the target with the next entry.
// [Iter.Last], [Iter.SeekLT] and the other seeks search from the head in
// O(log n) steps for n entries.
//
// Each move takes a context and ignores it, because a memtable does not
// wait. The moves then match those of the cursor of a table.
//
// The key and the value of the current entry are valid until [List.Reset],
// also after the next move. After Reset the cursor is on no entry until a
// First, a Last, a SeekGE or a SeekLT moves it.
//
// # Concurrency
//
// A cursor is for one goroutine. Its moves are safe while other goroutines
// add to the List.
//
// # Allocation contract
//
// The methods of a cursor do not allocate.
type Iter struct {
	// list is the List of the cursor.
	list *List

	// resets is the count of the resets of list at the last First, Last,
	// SeekGE or SeekLT. A cursor of an earlier fill is on no entry.
	resets uint64

	// node is the node of the current entry, or 0 when the cursor is on no
	// entry.
	node uint32
}

// First moves to the first entry and reports whether it exists.
//
// # Allocation contract
//
// Zero-alloc.
func (it *Iter) First(context.Context) bool {
	it.node, it.resets = atomic.LoadUint32(&it.list.nodes[nodeHeader]), it.list.resets

	return it.node != 0
}

// Last moves to the last entry and reports whether it exists. It follows
// the links of each level from the top of the List.
//
// # Allocation contract
//
// Zero-alloc.
func (it *Iter) Last(context.Context) bool {
	it.node, it.resets = it.list.last(), it.list.resets

	return it.node != 0
}

// SeekGE moves to the first entry at or after the entry (key, seq) and
// reports whether it exists. For a snapshot seq, an entry with key is the
// newest version of key at or below seq. A cursor on an entry before (key,
// seq) moves to the next entry without a search when that entry is at or
// after (key, seq). A cursor on no entry moves to the first entry the same
// way. Every other SeekGE searches from the head.
//
// # Allocation contract
//
// Zero-alloc.
func (it *Iter) SeekGE(_ context.Context, key []byte, seq uint64) bool {
	l := it.list
	prefix := keyPrefix(key)
	// A cursor on no entry is on node 0, the head. Its words are those of
	// the empty key at the sequence 0, and it links to the first entry.
	if it.resets == l.resets && l.compare(key, prefix, seq, it.node) > 0 {
		next := atomic.LoadUint32(&l.nodes[it.node+nodeHeader])
		if next != 0 && l.compare(key, prefix, seq, next) <= 0 {
			it.node = next

			return true
		}
	}
	_, it.node = l.search(key, prefix, seq)
	it.resets = l.resets

	return it.node != 0
}

// SeekLT moves to the last entry before the entry (key, seq) and reports
// whether it exists.
//
// # Allocation contract
//
// Zero-alloc.
func (it *Iter) SeekLT(_ context.Context, key []byte, seq uint64) bool {
	it.node, _ = it.list.search(key, keyPrefix(key), seq)
	it.resets = it.list.resets

	return it.node != 0
}

// Next moves to the next entry and reports whether it exists. Next of a
// cursor on no entry reports false.
//
// # Allocation contract
//
// Zero-alloc.
func (it *Iter) Next(context.Context) bool {
	if it.node == 0 || it.resets != it.list.resets {
		return false
	}
	it.node = atomic.LoadUint32(&it.list.nodes[it.node+nodeHeader])

	return it.node != 0
}

// Prev moves to the entry before the current one and reports whether it
// exists. It reads the link back b of the current node. While the link of b
// at level 0 is not the current node, b moves to the node after it. A link
// back is always a node before its node, and no node leaves a List before
// Reset, so the walk ends at the node before the current one. Each step of
// the walk moves the link back of the current node to the next b. Prev of a
// cursor on no entry reports false.
//
// # Allocation contract
//
// Zero-alloc.
func (it *Iter) Prev(context.Context) bool {
	if it.node == 0 || it.resets != it.list.resets {
		return false
	}
	l := it.list
	b := atomic.LoadUint32(&l.nodes[it.node+wordBack])
	for {
		next := atomic.LoadUint32(&l.nodes[b+nodeHeader])
		if next == it.node {
			break
		}
		// A writer linked next after b and left the link back on b. next is
		// before the current node too.
		atomic.CompareAndSwapUint32(&l.nodes[it.node+wordBack], b, next)
		b = next
	}
	it.node = b

	return b != 0
}

// Key returns the key of the current entry, with a capacity of its length,
// or nil for a cursor on no entry.
//
// # Allocation contract
//
// Zero-alloc.
func (it *Iter) Key() []byte {
	if it.node == 0 || it.resets != it.list.resets {
		return nil
	}

	return it.list.key(it.node)
}

// Seq returns the sequence of the current entry, or 0 for a cursor on no
// entry. The words of the head node, at index 0, are 0.
//
// # Allocation contract
//
// Zero-alloc.
func (it *Iter) Seq() uint64 {
	if it.resets != it.list.resets {
		return 0
	}

	return uint64(it.list.nodes[it.node+wordSeqHigh])<<32 | uint64(it.list.nodes[it.node+wordSeqLow])
}

// Value returns the value of the current entry, with a capacity of its
// length, or nil for a cursor on no entry.
//
// # Allocation contract
//
// Zero-alloc.
func (it *Iter) Value() []byte {
	if it.node == 0 || it.resets != it.list.resets {
		return nil
	}

	return it.list.value(it.node)
}

// Err returns nil. A cursor over a List has no error.
//
// # Allocation contract
//
// Zero-alloc.
func (*Iter) Err() error {
	return nil
}
