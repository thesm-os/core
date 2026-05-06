// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package page

import (
	"context"
	"iter"
)

// Cursor iterates over a paginated result stream.
//
// Cursor lives in this package (rather than in any specific data
// package) because every list-returning interface across the
// thesmos ecosystem returns one. Promoting it here means every
// consumer shares the same iteration contract.
//
// # Iteration
//
// Cursor has exactly one consumption shape — the [iter.Seq2]
// returned by [Cursor.Seq]. Callers use the standard Go 1.23+
// for-range form:
//
//	for item, err := range cursor.Seq(ctx) {
//	    if err != nil { return err }
//	    process(item)
//	}
//	token := cursor.NextPage()
//
// The iterator yields (item, nil) for successful items and
// (zero, err) for mid-stream failures; callers MUST check err in
// every iteration (range-over-func makes this syntactically
// unavoidable — there is no "forgot to call Err()" trap).
//
// Callers that need manual pull control use [iter.Pull2] from the
// stdlib.
//
// # Pagination boundary
//
// [Cursor.NextPage] stays on the [Cursor] (not on the iterator)
// because the continuation token is per-traversal metadata that
// the iterator itself does not carry. Typical usage: range over
// [Cursor.Seq] to consume one page, then call [Cursor.NextPage]
// to get the token for the next request.
//
// # Allocation contract
//
// Per-iteration overhead is the standard range-over-func cost:
// one stack-allocated yield closure under Go's escape analysis.
// Cursors that fetch on demand (one network round-trip per page
// batch) allocate at fetch time only.
type Cursor[T any] interface {
	// Seq returns a range-over-func iterator over the cursor's
	// remaining items.
	Seq(ctx context.Context) iter.Seq2[T, error]

	// NextPage returns the continuation token suitable for the
	// next [Page] request, or the empty string if there are no
	// more pages. Callers invoke [Cursor.NextPage] AFTER draining
	// [Cursor.Seq] for the current page.
	NextPage() string

	// Close releases cursor resources. Idempotent.
	Close() error
}
