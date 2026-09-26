// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package arena provides allocation primitives for hot paths: [Arena], a
// contiguous byte buffer for variable-length binary output, and [List], an
// append-only sequence of typed values in chunks that never move.
//
// # Arena
//
// A request that produces many small variable-length outputs, such as
// audit chain entries, encoded patches or batched telemetry payloads, makes
// one heap allocation per output without an arena. Those allocations
// fragment the heap and lengthen the garbage collector's mark phase. An
// [Arena] replaces them with one contiguous buffer per request. A pooled
// Arena ([go.thesmos.sh/core/pool]) keeps its buffer across requests, so
// steady-state work allocates nothing for it.
//
// Sub-slices returned by [Arena.Append], [Arena.Alloc] and
// [Arena.SliceSince] alias the arena's backing buffer. They are valid until
// [Arena.Reset], after which the next append may overwrite their bytes. A
// caller transfers ownership at the boundary with [Arena.CopyOut],
// [Arena.CopyOutTo], [RebaseSlices] or [RebaseSlicesTo].
//
// # List
//
// Growing a slice with append reallocates its backing array and copies
// every element. A [List] stores its elements in chunks instead. Once its
// first chunk has grown to 4,096 elements, every chunk is allocated whole
// and never moves. From then on, appending copies no element. An address
// from [List.Ptr] remains valid until [List.Truncate] drops its element.
// Truncate keeps the chunks. A List that is emptied and filled again
// reuses them without allocating.
//
// # Pool integration
//
// [Arena.Reset] satisfies [pool.Resettable], so a pool of arenas is one
// line:
//
//	var arenas = pool.NewResetPool(arena.New)
//
// [Arena.Reset] zeroes the bytes written in the finished lifecycle, so a
// pooled arena passes none of one user's bytes to the next.
// [Arena.CapExceeds] reports whether the backing buffer has grown past a
// threshold, so a pool can release an oversized arena with [Arena.Shrink]
// instead of keeping it.
//
// # Allocation contract
//
// [Arena.Append] and [Arena.Alloc] do not allocate when the backing buffer
// has room. The first call after construction or after [Arena.Shrink]
// allocates the backing buffer, and later calls in the same Reset cycle
// reuse it. [Arena.CopyOut] allocates once, the size of the appended
// bytes. [Arena.CopyOutTo] does not allocate when the destination has
// room. [RebaseSlices] and [RebaseSlicesTo] allocate at most once.
//
// [List.Append] allocates only when a List grows past [List.Cap]. No other
// List method allocates.
package arena
