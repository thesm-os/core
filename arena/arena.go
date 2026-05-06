// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package arena

import "go.thesmos.sh/core/epoch"

// Arena is a contiguous byte buffer that supports append-only
// writes. Callers append binary data and receive sub-slices
// pointing into the backing buffer; at the ownership boundary
// they bulk-copy the data to caller-owned memory.
//
// # Concurrency
//
// Arena is NOT safe for concurrent use. Pool one Arena per
// goroutine (e.g. via [go.thesmos.sh/core/pool.NewResetPool])
// for request-scoped use.
//
// # Backing-array lifetime
//
// Sub-slices returned by [Arena.Append], [Arena.Alloc],
// [Arena.SliceSince], and [Arena.Bytes] alias the arena's
// current backing array. When an [Arena.Append] or
// [Arena.Alloc] call exceeds the current capacity, the arena
// reallocates: previously-returned sub-slices reference the
// orphaned backing array (still readable, kept alive by the
// slice header), but they no longer alias subsequent calls'
// returns or [Arena.Bytes].
//
// Concretely:
//
//	a := arena.NewWithCapacity(8)
//	first := a.Append([]byte("a"))     // first → array A, byte 0
//	a.Append(make([]byte, 1024))        // exceeds cap, realloc → array B
//	view := a.Bytes()                   // view → array B (zeroed prefix + tail)
//	// first[0] == 'a' (still alive on array A)
//	// view[0] == 0   (array B was freshly allocated)
//	// first and view DO NOT alias the same memory.
//
// To keep the alias-stable contract, pre-size the arena via
// [NewWithCapacity] above the highest expected total. Once
// capacity is sufficient for the entire Reset cycle, no
// realloc occurs and every returned sub-slice aliases the
// same backing array until [Arena.Reset].
//
// After [Arena.Reset], all previously-returned sub-slices
// must be considered invalid: their bytes may be overwritten
// by subsequent appends.
//
// # Allocation contract
//
// [Arena.Append] and [Arena.Alloc] are zero-allocation when
// the backing buffer has sufficient capacity. Both grow the
// backing buffer when capacity is exceeded — that growth
// reallocates and orphans the old backing array.
type Arena struct {
	buf   []byte
	epoch epoch.Epoch // bumped on every Reset/Shrink to invalidate stale Markers.
}

// New returns an [Arena] with no backing buffer; the first
// [Arena.Append] or [Arena.Alloc] call allocates one.
func New() *Arena {
	return &Arena{}
}

// NewWithCapacity returns an [Arena] with a pre-allocated
// backing buffer of the given capacity. Callers that know an
// upper bound on appended data avoid the first-call allocation
// by sizing up front.
func NewWithCapacity(initialCap int) *Arena {
	return &Arena{buf: make([]byte, 0, initialCap)}
}

// Append copies data into the arena and returns a sub-slice
// pointing into the backing buffer. The returned slice is a
// three-index expression with capacity capped at its length —
// downstream code that calls [append] on it cannot accidentally
// extend into a neighbouring item's region.
//
// The returned slice remains readable until [Arena.Reset] (or
// [Arena.Shrink]) is called. Reading it after Reset returns
// whatever the next [Arena.Append] / [Arena.Alloc] wrote —
// silent corruption. See [Arena] for the backing-array
// lifetime contract.
//
// # Allocation contract
//
// Zero-alloc when the backing buffer has sufficient capacity.
// Reallocates with growth when capacity is exceeded.
func (a *Arena) Append(data []byte) []byte {
	start := len(a.buf)
	a.buf = append(a.buf, data...)
	end := len(a.buf)
	return a.buf[start:end:end]
}

// Alloc reserves n zeroed bytes in the arena and returns a
// sub-slice for the caller to fill in. The returned slice is
// three-index-capped — see [Arena.Append] for the rationale.
//
// Alloc is the right choice when the data is produced
// in-place (decoder writes directly into the arena's buffer).
// [Arena.Append] is the right choice when the data already
// exists in a caller-owned buffer.
//
// # Preconditions
//
// n must be non-negative. Alloc does not validate this;
// negative n triggers a stdlib panic (from [make] or slice
// bounds) — caller's responsibility to enforce the
// precondition.
//
// # Allocation contract
//
// Zero-alloc when the backing buffer has sufficient capacity.
// Reallocates with growth when capacity is exceeded.
func (a *Arena) Alloc(n int) []byte {
	start := len(a.buf)
	needed := start + n
	if cap(a.buf) >= needed {
		// Capacity already covers the request; just expand the
		// length and zero the new region (a prior Reset may have
		// left old bytes behind).
		a.buf = a.buf[:needed]
		clear(a.buf[start:needed])
	} else {
		// Grow with doubling. The new region is implicitly
		// zeroed by [make].
		newCap := growCap(cap(a.buf), needed)
		newBuf := make([]byte, needed, newCap)
		copy(newBuf, a.buf)
		a.buf = newBuf
	}
	return a.buf[start:needed:needed]
}

// growCap returns a target capacity that satisfies want and
// grows the previous capacity by doubling — the same growth
// rate Go's [append] uses for slices.
//
// have*2 overflows for arenas approaching [math.MaxInt]/2 on
// 64-bit platforms (~4.6 EB). Such an arena would already
// exceed addressable memory; callers that approach this scale
// must implement their own bounded allocator.
func growCap(have, want int) int {
	return max(have*2, want)
}

// Marker is an opaque token returned by [Arena.Mark] for use
// with [Arena.SliceSince]. A Marker captures the arena's
// write position AND its lifecycle [epoch.Epoch] — Markers
// from a previous lifecycle (before [Arena.Reset] or
// [Arena.Shrink]) are detected and rejected by
// [Arena.SliceSince], preventing silent corruption when a
// stale Marker survives across a Reset.
type Marker struct {
	pos   int
	epoch epoch.Epoch
}

// Mark returns a [Marker] capturing the current write
// position and lifecycle epoch. Combined with
// [Arena.SliceSince], it captures a region built across
// multiple [Arena.Append] / [Arena.Alloc] calls as a single
// sub-slice.
//
// A Marker is valid only within the lifecycle in which it
// was created. After [Arena.Reset] or [Arena.Shrink],
// previously-returned Markers become stale and
// [Arena.SliceSince] returns nil for them.
//
// # Allocation contract
//
// Zero-alloc.
func (a *Arena) Mark() Marker {
	return Marker{pos: len(a.buf), epoch: a.epoch}
}

// SliceSince returns a three-index sub-slice from m to the
// current end of the arena. Returns nil when:
//
//   - m's epoch does not match the arena's current epoch
//     (m predates a [Arena.Reset] or [Arena.Shrink]); or
//   - m's position is at or past the current end.
//
// # Allocation contract
//
// Zero-alloc.
func (a *Arena) SliceSince(m Marker) []byte {
	if m.epoch != a.epoch {
		return nil
	}
	end := len(a.buf)
	if m.pos >= end {
		return nil
	}
	return a.buf[m.pos:end:end]
}

// Len returns the number of bytes currently appended.
//
// # Allocation contract
//
// Zero-alloc.
func (a *Arena) Len() int {
	return len(a.buf)
}

// Cap returns the backing buffer capacity.
//
// # Allocation contract
//
// Zero-alloc.
func (a *Arena) Cap() int {
	return cap(a.buf)
}

// Bytes returns a read-only view over every byte appended
// since the last [Arena.Reset]. The returned slice is a
// three-index expression capped at the current length so
// downstream [append] cannot extend it into the arena's
// remaining capacity.
//
// Bytes follows the same backing-array lifetime contract as
// [Arena.Append]'s return: a buffer-grow event between Bytes
// and a subsequent Bytes call invalidates the alias between
// them. After [Arena.Reset] the returned slice's bytes may be
// overwritten. See [Arena] for the full lifetime model.
//
// # Allocation contract
//
// Zero-alloc.
func (a *Arena) Bytes() []byte {
	end := len(a.buf)
	return a.buf[:end:end]
}
