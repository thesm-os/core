// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package arena provides a contiguous byte buffer for batching
// variable-length binary output on hot paths. Instead of N
// independent heap allocations (one per output item), callers
// append into a single arena and bulk-copy the result at the
// ownership-transfer boundary.
//
// # Why this matters
//
// On a request that produces many small variable-length
// outputs (audit chain entries, encoded patches, batched
// telemetry payloads), N independent allocations cause heap
// fragmentation and GC mark-phase latency. An arena replaces
// those with one contiguous allocation per request. With pool
// reuse ([go.thesmos.sh/core/pool]) the arena's backing buffer
// is preserved across requests — steady-state work allocates
// nothing for the arena itself.
//
// # Ownership and lifetime
//
// Sub-slices returned by [Arena.Append], [Arena.Alloc], and
// [Arena.SliceSince] alias the arena's backing buffer. They
// remain readable until [Arena.Reset] is called, after which
// the bytes they point to may be overwritten by subsequent
// appends — silent corruption if a stale slice is read.
// Treat returned sub-slices as borrowed; transfer ownership
// at the boundary via [Arena.CopyOut] / [Arena.CopyOutTo] /
// [RebaseSlices] / [RebaseSlicesTo].
//
// # Pool integration
//
// [Arena.Reset] satisfies [pool.Resettable], so an
// arena-of-arenas pool is one line:
//
//	var arenas = pool.NewResetPool(arena.New)
//
// [Arena.ShouldShrink] reports whether the backing buffer has
// grown past a threshold; the pool wrapper can decide to
// release oversized arenas (via [Arena.Shrink]) rather than
// retain them indefinitely.
//
// # Allocation contract
//
// [Arena.Append] and [Arena.Alloc] are zero-allocation when
// the backing buffer has sufficient capacity. The first call
// after construction (or after [Arena.Shrink]) allocates the
// backing buffer; subsequent calls within the same Reset
// cycle reuse it.
//
// [Arena.CopyOut] performs exactly one allocation sized to
// the appended bytes. [Arena.CopyOutTo] is zero-allocation
// when the destination buffer has sufficient capacity.
// [RebaseSlices] and [RebaseSlicesTo] allocate at most once.
package arena
