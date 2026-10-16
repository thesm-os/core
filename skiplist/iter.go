// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package skiplist

import "sync/atomic"

// Iter is a cursor over the entries of a [List], in the order of the List.
// A move positions the cursor on an entry or exhausts it. A cursor yields
// the entries that a move finds linked: every entry whose Add returned before
// the move, and the entries that writers link while it moves.
//
// The key and the value of the current entry are valid until [List.Reset],
// also after the next move.
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

	// node is the node of the current entry, or 0 when the cursor is on no
	// entry.
	node uint32
}

// First moves to the first entry and reports whether it exists.
//
// # Allocation contract
//
// Zero-alloc.
func (it *Iter) First() bool {
	it.node = atomic.LoadUint32(&it.list.nodes[nodeHeader])

	return it.node != 0
}

// SeekGE moves to the first entry at or after the entry (key, seq) and
// reports whether it exists. For a snapshot seq, an entry with key is the
// newest version of key at or below seq.
//
// # Allocation contract
//
// Zero-alloc.
func (it *Iter) SeekGE(key []byte, seq uint64) bool {
	it.node = it.list.seekGE(key, seq)

	return it.node != 0
}

// Next moves to the next entry and reports whether it exists. Next of a
// cursor on no entry reports false.
//
// # Allocation contract
//
// Zero-alloc.
func (it *Iter) Next() bool {
	if it.node == 0 {
		return false
	}
	it.node = atomic.LoadUint32(&it.list.nodes[it.node+nodeHeader])

	return it.node != 0
}

// Key returns the key of the current entry, with a capacity of its length,
// or nil for a cursor on no entry.
//
// # Allocation contract
//
// Zero-alloc.
func (it *Iter) Key() []byte {
	if it.node == 0 {
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
	return uint64(it.list.nodes[it.node+wordSeqHigh])<<32 | uint64(it.list.nodes[it.node+wordSeqLow])
}

// Value returns the value of the current entry, with a capacity of its
// length, or nil for a cursor on no entry.
//
// # Allocation contract
//
// Zero-alloc.
func (it *Iter) Value() []byte {
	if it.node == 0 {
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
