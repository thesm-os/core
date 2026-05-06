// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package page

import (
	"context"
	"iter"
)

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

// MapCursor wraps an in-memory slice of [Entry] values as a
// [Cursor] over key/value pairs. The canonical concrete
// implementation for tests, fixtures, and adapters that page
// over a key-value store with results already in memory.
//
// # Cancellation
//
// [MapCursor.Seq] honours ctx cancellation: each iteration
// checks [context.Context.Err], yielding (zero, ctx.Err()) and
// stopping when the context is cancelled.
//
// # Pagination
//
// MapCursor returns the configured token from
// [MapCursor.NextPage] unconditionally — pagination is the
// caller's contract. Final pages set the token to ""; non-final
// pages set it to whatever opaque value the next [Page] request
// should carry.
//
// # Allocation contract
//
// Construction allocates the receiver. [MapCursor.Seq] is
// zero-allocation per iteration (range-over-func standard
// overhead). [MapCursor.NextPage] and [MapCursor.Close] are
// zero-alloc.
type MapCursor[K, V any] struct {
	nextToken string
	entries   []Entry[K, V]
}

// NewMapCursor returns a [MapCursor] over entries, returning
// nextToken from [MapCursor.NextPage]. Pass the empty string for
// final pages.
func NewMapCursor[K, V any](entries []Entry[K, V], nextToken string) *MapCursor[K, V] {
	return &MapCursor[K, V]{entries: entries, nextToken: nextToken}
}

// Seq returns an [iter.Seq2] over the wrapped entries, honouring
// ctx cancellation between yields.
func (c *MapCursor[K, V]) Seq(ctx context.Context) iter.Seq2[Entry[K, V], error] {
	return func(yield func(Entry[K, V], error) bool) {
		for _, e := range c.entries {
			if err := ctx.Err(); err != nil {
				var zero Entry[K, V]
				yield(zero, err)
				return
			}
			if !yield(e, nil) {
				return
			}
		}
	}
}

// NextPage returns the opaque token supplied at construction.
func (c *MapCursor[K, V]) NextPage() string {
	return c.nextToken
}

// Close releases cursor resources. MapCursor holds none, so
// Close is a no-op that always returns nil. Idempotent.
func (*MapCursor[K, V]) Close() error {
	return nil
}
