// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package page

// Entry is one key/value pair carried through a [Cursor] over a
// key-value store. Used as the element type of [MapCursor].
//
// # Allocation contract
//
// Generic value type; pass by value. The underlying K and V
// follow the consumer's allocation behaviour.
type Entry[K, V any] struct {
	Key   K
	Value V
}

// MapCursor is a [SliceCursor] over [Entry] values: the [Cursor] for
// key/value pairs that are already in memory. It is an alias, so its
// methods and their cancellation, pagination and allocation contracts
// are those of [SliceCursor].
type MapCursor[K, V any] = SliceCursor[Entry[K, V]]

// NewMapCursor returns a [MapCursor] over entries, returning
// nextToken from [Cursor.NextPage]. Pass the empty string for final
// pages.
func NewMapCursor[K, V any](entries []Entry[K, V], nextToken string) *MapCursor[K, V] {
	return NewSliceCursor(entries, nextToken)
}
