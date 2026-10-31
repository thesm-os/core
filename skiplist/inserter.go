// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package skiplist

// Inserter adds entries to a [List], and keeps the node before its last entry
// at each level. An Add of an entry after the last entry searches each level
// from that node instead of the head, so an Inserter that adds entries in
// order, or close to the last one, searches a few nodes at each level. An Add
// of an earlier entry searches from the head, as [List.Add] does.
//
// The zero Inserter is ready for use with any List. An Inserter used with
// another List, or with a List that [List.Reset] emptied, searches from the
// head once, and keeps the nodes of that List from then on.
//
// # Concurrency
//
// An Inserter is for one goroutine. Other goroutines can add to the List at
// the same time, through List.Add or Inserters of their own.
//
// # Allocation contract
//
// Zero-alloc.
type Inserter struct {
	// list is the List of the last Add that succeeded, or nil.
	list *List

	// resets is the count of the resets of list at that Add.
	resets uint64

	// prev is a node before every entry after the entry of that Add, for
	// each level: the node of the entry at its levels, the node before it
	// at the levels above, and 0, the head, above the height of list.
	prev [maxHeight]uint32
}

// Add inserts the entry of key, seq and value into l, with the contract and
// the errors of [List.Add]. When the last Add of ins succeeded on l since its
// last Reset, and the entry is after the entry of that Add, the search of
// each level starts at the node that ins keeps for the level. The nodes of a
// List never move before Reset, so each of them is before the entry.
//
// An Add that succeeds keeps the new node at each of its levels and the node
// before it at each higher level. An Add that fails keeps the nodes of the
// Add before it.
//
// Error modes:
//
//   - An entry with the key and the sequence of an entry of the List
//     returns [ErrExists].
//   - An entry that does not fit an arena returns [ErrFull].
//
// # Allocation contract
//
// Zero-alloc.
func (ins *Inserter) Add(l *List, key []byte, seq uint64, value []byte) error {
	prefix := l.prefixOf(key)
	var from *[maxHeight]uint32
	if ins.list == l && ins.resets == l.resets && l.compare(key, prefix, seq, ins.prev[0]) > 0 {
		from = &ins.prev
	}
	var prev [maxHeight]uint32
	at, h, err := l.insert(key, prefix, seq, value, from, &prev)
	if err != nil {
		return err
	}

	for level := range h {
		prev[level] = at
	}
	ins.list, ins.resets, ins.prev = l, l.resets, prev

	return nil
}
