// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package sstable

import (
	"bytes"
	"cmp"
	"context"
)

// Source is a cursor over a run of entries in the order of [Compare]. A move
// positions the cursor on an entry or exhausts it. Each move takes the
// context of the read that it can make, and a read that ends with its
// context exhausts the cursor. An [Iter] of a table, a [Merger] and the
// cursor of a memtable are Sources.
type Source interface {
	// First moves to the first entry and reports whether it exists.
	First(ctx context.Context) bool

	// Last moves to the last entry and reports whether it exists.
	Last(ctx context.Context) bool

	// SeekGE moves to the first entry at or after the entry (key, seq) and
	// reports whether it exists.
	SeekGE(ctx context.Context, key []byte, seq uint64) bool

	// SeekLT moves to the last entry before the entry (key, seq) and reports
	// whether it exists.
	SeekLT(ctx context.Context, key []byte, seq uint64) bool

	// Next moves to the next entry and reports whether it exists. Next of a
	// cursor on no entry reports false.
	Next(ctx context.Context) bool

	// Prev moves to the entry before the current one and reports whether it
	// exists. Prev of a cursor on no entry reports false.
	Prev(ctx context.Context) bool

	// Key returns the key of the current entry. It is valid until the next
	// move of the cursor.
	Key() []byte

	// Seq returns the sequence of the current entry.
	Seq() uint64

	// Value returns the value of the current entry. It is valid until the
	// next move of the cursor.
	Value() []byte

	// Err returns the error that exhausted the cursor, or nil.
	Err() error
}

// Compare returns -1, 0 or +1 for the order of the entry (aKey, aSeq) and
// the entry (bKey, bSeq). It orders by key as bytes.Compare orders keys, then
// by sequence from the highest to the lowest. A nil key equals an empty key.
//
// # Allocation contract
//
// Zero-alloc.
func Compare(aKey []byte, aSeq uint64, bKey []byte, bSeq uint64) int {
	if c := bytes.Compare(aKey, bKey); c != 0 {
		return c
	}

	return cmp.Compare(bSeq, aSeq)
}
