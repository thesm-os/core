// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

// Package arena provides allocation primitives for hot paths. [Arena] is a
// contiguous byte buffer for variable-length binary output. [List] is an
// append-only sequence of typed values in chunks that never move. [Slabs]
// allocates byte slices from slabs and takes single slices back for reuse.
// [RefSlabs] allocates as Slabs does and returns positions instead of
// slices. [Bump] is a fixed array that goroutines reserve in ranges without
// locks.
//
// # Arena
//
// Without an arena, a request that produces many small variable-length
// outputs makes one heap allocation per output. Audit chain entries,
// encoded patches and batched telemetry payloads are such outputs. Those
// allocations fragment the heap and lengthen the mark phase of the garbage
// collector. An [Arena] replaces them with one contiguous buffer per
// request. A pooled Arena ([go.thesmos.sh/core/pool]) keeps its buffer
// across requests, so steady-state work does not allocate for it.
//
// [Arena.Append], [Arena.Alloc] and [Arena.SliceSince] return sub-slices
// that alias the backing buffer of the arena. They are valid until
// [Arena.Reset]. After the Reset, the next append may overwrite their
// bytes. A caller transfers ownership at the boundary with
// [Arena.CopyOut], [Arena.CopyOutTo], [RebaseSlices] or [RebaseSlicesTo].
//
// [NewWithBuffer] builds an Arena over zeroed memory of the caller. A caller
// that moves the memory of a memtable between an Arena and a
// [go.thesmos.sh/core/skiplist.List] keeps the memory, and builds each Arena
// over it without an allocation of the memory.
//
// # List
//
// Growing a slice with append reallocates its backing array and copies
// every element. A [List] stores its elements in chunks instead. Once its
// first chunk has grown to 4,096 elements, every chunk is allocated whole
// and never moves. From then on, an append does not copy an element. An
// address from [List.Ptr] remains valid until [List.Truncate] drops its
// element. Truncate keeps the chunks. A List that is emptied and filled
// again reuses them without allocating.
//
// # Slabs
//
// An Arena frees all its bytes at once, and a List drops only a suffix.
// Neither lets a store that removes its values one at a time in any order
// reuse the space of a removed value. [Slabs.Alloc] returns a slice whose
// capacity is a size class. The smallest class is [MinClass] bytes, and
// eight classes divide each interval between two powers of two above it.
// A slice above MinClass takes less than 1.125 times its length.
// Alloc carves the slice from slabs of a size that the caller chooses.
// [Slabs.Free] zeroes the class of a slice and keeps it for the next Alloc
// of the class. A store whose values come and go then reuses the space of
// a removed value without an allocation. [Slabs.All] yields every slab
// with its free space. A test searches all the memory of the Slabs for a
// removed value with it.
//
// # RefSlabs
//
// A record that keeps a slice from Slabs contains a pointer, so the garbage
// collector scans every chunk of such records. [RefSlabs.Alloc] returns a
// [Ref] instead: the number of a slab and an offset in it, in one uint64.
// [RefSlabs.Bytes] returns the bytes of a region, and [RefSlabs.Free] takes
// a region back. A RefSlabs has the size classes of Slabs, so a region above
// MinClass takes less than 1.125 times its length.
//
// # Bump
//
// Arena, List, Slabs and RefSlabs are for one goroutine. A [Bump] is a fixed
// array that goroutines reserve in ranges without locks. [Bump.Reserve]
// moves the end of the reserved values with a compare-and-swap, and reports
// false when fewer values remain than it needs. The array never grows or
// moves, so a reserved range keeps its position until [Bump.Reset]. The
// memtable of [go.thesmos.sh/core/skiplist] keeps its nodes and its bytes in
// two Bumps. [NewBumpWithArray] builds a Bump over an array of the caller.
//
// # Pool integration
//
// [Arena.Reset] satisfies [pool.Resettable], so a pool of arenas is one
// line:
//
//	var arenas = pool.NewResetPool(arena.New)
//
// [Arena.Reset] zeroes the bytes written in the finished lifecycle, so a
// pooled arena does not pass the bytes of one user to the next.
// [Arena.ResetWritten] ends a lifecycle in which the caller wrote only a
// prefix of the length, and zeroes only that prefix.
// [Arena.CapExceeds] reports whether the backing buffer has grown past a
// threshold, so a pool can release an oversized arena with [Arena.Shrink]
// instead of keeping it.
//
// # Allocation contract
//
// [Arena.Append] and [Arena.Alloc] do not allocate when the backing buffer
// has room. The first call after [New] or after [Arena.Shrink] allocates the
// backing buffer, and later calls in the same Reset cycle reuse it.
// [NewWithCapacity] allocates the Arena and its backing buffer, and
// [NewWithBuffer] allocates the Arena alone. [Arena.CopyOut] makes one
// allocation of the size of the appended bytes. [Arena.CopyOutTo] does not
// allocate when the destination has room. [RebaseSlices] and
// [RebaseSlicesTo] allocate at most once.
//
// [List.Append] allocates only when a List grows past [List.Cap]. No other
// List method allocates.
//
// [Slabs.Alloc] allocates a new slab only when the free list of its class
// is empty and the current slab has no room for the class. [Slabs.Free]
// allocates only when the free list of a class grows past its capacity.
// Once the free lists have grown to the working set, an Alloc followed by
// a Free does not allocate. [RefSlabs.Alloc] and [RefSlabs.Free] allocate
// as their Slabs counterparts do. [RefSlabs.Bytes] does not allocate.
//
// [NewBump] allocates the Bump and its array, and [NewBumpWithArray]
// allocates the Bump alone. No method of a Bump allocates.
package arena
