// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package page

import (
	"context"
	"iter"
)

// SliceCursor wraps an in-memory slice as a [Cursor]. The
// canonical concrete [Cursor] implementation for tests, fixtures,
// and adapters that already have all results in memory.
//
// # Cancellation
//
// [Cursor.Seq] honours ctx cancellation: each iteration checks
// [context.Context.Err], yielding (zero, ctx.Err()) and stopping
// when the context is cancelled.
//
// # Pagination
//
// SliceCursor returns NextToken from [Cursor.NextPage]
// unconditionally — pagination is the caller's contract. Callers
// returning a final page set NextToken to "" so consumers stop;
// callers returning a non-final page set NextToken to whatever
// opaque value the next [Page] request should carry.
//
// # Allocation contract
//
// Construction allocates the receiver. [Cursor.Seq] is
// zero-allocation per iteration (range-over-func standard
// overhead). [Cursor.NextPage] and [Cursor.Close] are zero-alloc.
type SliceCursor[T any] struct {
	nextToken string
	items     []T
}

// NewSliceCursor returns a [SliceCursor] over items, returning
// nextToken from [Cursor.NextPage]. Pass the empty string for
// final pages.
func NewSliceCursor[T any](items []T, nextToken string) *SliceCursor[T] {
	return &SliceCursor[T]{items: items, nextToken: nextToken}
}

// Seq returns a [iter.Seq2] over the wrapped slice, honouring
// ctx cancellation between items.
func (c *SliceCursor[T]) Seq(ctx context.Context) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		for _, item := range c.items {
			if err := ctx.Err(); err != nil {
				var zero T
				yield(zero, err)
				return
			}
			if !yield(item, nil) {
				return
			}
		}
	}
}

// NextPage returns the opaque token supplied at construction.
func (c *SliceCursor[T]) NextPage() string {
	return c.nextToken
}

// Close releases cursor resources. SliceCursor holds none, so
// Close is a no-op that always returns nil. Idempotent.
func (*SliceCursor[T]) Close() error {
	return nil
}
