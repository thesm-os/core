// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package page defines [Page] and [Cursor] — the pagination
// request and response shape used by every list-returning
// interface so unbounded scans are impossible to write
// accidentally.
//
// # Shape
//
//   - [Page] carries the request (Limit + opaque continuation
//     Token).
//   - [Cursor] is the response interface; its [Cursor.Seq] returns
//     a Go 1.23+ range-over-func iterator that yields (item, err)
//     pairs and a [Cursor.NextPage] token for the next request.
//
// # Provided implementations
//
//   - [SliceCursor] wraps an in-memory slice as a [Cursor]. The
//     canonical concrete impl for tests, fixtures, and adapters
//     that already have all results in memory.
//
// External implementations live in consumer modules — every
// storage adapter that streams results from a remote backend
// implements [Cursor] over its own paging primitive.
//
// # Allocation contract
//
// [Page] is a value type; pass by value. Per-iteration cost from
// [Cursor.Seq] is the standard range-over-func overhead (one
// stack-allocated yield closure under Go's escape analysis).
// Cursors that fetch on demand allocate at fetch time only.
package page
