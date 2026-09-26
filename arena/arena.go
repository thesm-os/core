// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package arena

import "go.thesmos.sh/core/epoch"

// Arena is a contiguous byte buffer for append-only writes. A caller
// appends binary data and receives sub-slices of the backing buffer. At
// the ownership boundary it copies the data out to memory of its own.
//
// # Concurrency
//
// Arena is not safe for concurrent use. Give each goroutine an Arena of
// its own, for example from [go.thesmos.sh/core/pool.NewResetPool].
//
// # Backing-array lifetime
//
// Sub-slices returned by [Arena.Append], [Arena.Alloc], [Arena.SliceSince]
// and [Arena.Bytes] alias the arena's current backing array. When an
// [Arena.Append] or [Arena.Alloc] call needs more than the capacity, the
// arena moves to a new backing array. A sub-slice returned earlier keeps
// the old array alive and remains readable, but it no longer aliases later
// returns or [Arena.Bytes]:
//
//	a := arena.NewWithCapacity(8)
//	first := a.Append([]byte("a"))     // first → array A, byte 0
//	a.Append(make([]byte, 1024))        // exceeds cap, realloc → array B
//	view := a.Bytes()                   // view → array B (copied prefix + tail)
//	// first[0] == 'a' (still alive on array A)
//	// view[0] == 'a'  (a copy on array B)
//	// A write through first does not change view.
//
// An arena created by [NewWithCapacity] with room for the largest total of
// a Reset cycle never reallocates in that cycle, so every sub-slice aliases
// one backing array until [Arena.Reset].
//
// After [Arena.Reset], every sub-slice returned earlier is invalid: the
// next append may overwrite its bytes.
//
// # Allocation contract
//
// [Arena.Append] and [Arena.Alloc] do not allocate when the backing buffer
// has room. When it does not, they allocate a larger backing array and
// leave the old one to the sub-slices that reference it.
type Arena struct {
	buf   []byte
	epoch epoch.Epoch // advanced by every Reset and Shrink to invalidate stale Markers

	// dirty is how far a TruncateTo rewind or a failed AppendVia left
	// written bytes past the length. Every byte of the backing array at or
	// past max(len(buf), dirty) is zero. Reset zeroes buf up to that bound,
	// and Alloc clears only the part of its region below dirty.
	dirty int
}

// New returns an [Arena] with no backing buffer. The first [Arena.Append]
// or [Arena.Alloc] call allocates one.
func New() *Arena {
	return &Arena{}
}

// NewWithCapacity returns an [Arena] whose backing buffer has room for
// initialCap bytes. A caller that knows an upper bound on the data it
// appends sizes the arena up front and avoids the first allocation.
func NewWithCapacity(initialCap int) *Arena {
	return &Arena{buf: make([]byte, 0, initialCap)}
}

// Append copies data into the arena and returns the sub-slice of the
// backing buffer that contains it. The capacity of the returned slice is
// its length, so an [append] to it cannot write into the next item's
// bytes.
//
// The returned slice is valid until [Arena.Reset] or [Arena.Shrink]. After
// either, the next append may overwrite its bytes. See [Arena] for the
// backing-array lifetime.
//
// # Allocation contract
//
// Zero alloc when the backing buffer has room for data. Otherwise Append
// allocates a larger backing array.
func (a *Arena) Append(data []byte) []byte {
	start := len(a.buf)
	a.buf = append(a.buf, data...)
	end := len(a.buf)
	return a.buf[start:end:end]
}

// Alloc reserves n zeroed bytes in the arena and returns them for the
// caller to fill. The capacity of the returned slice is its length, as for
// [Arena.Append].
//
// Use Alloc when the data is produced in place, for example by a decoder
// that writes into the arena. Use [Arena.Append] when the data already
// exists in a buffer of the caller's.
//
// The bytes are zero from the allocation or from [Arena.Reset]. Alloc
// clears only the part of the region that an [Arena.TruncateTo] rewind or
// a failed [Arena.AppendVia] left written. On any other arena it writes
// nothing to the region, so the caller's fill is the first write to it.
//
// # Preconditions
//
// n must not be negative. Alloc does not check it: a negative n panics in
// [make] or in a slice expression.
//
// # Allocation contract
//
// Zero alloc when the backing buffer has room for n bytes. Otherwise Alloc
// allocates a larger backing array.
func (a *Arena) Alloc(n int) []byte {
	start := len(a.buf)
	needed := start + n
	if cap(a.buf) >= needed {
		// Spare capacity is zero at and past the dirty mark, so only
		// the part of the region below it can contain old bytes.
		a.buf = a.buf[:needed]
		clear(a.buf[start:max(start, min(needed, a.dirty))])
	} else {
		// The new backing array comes zeroed from make.
		newCap := growCap(cap(a.buf), needed)
		newBuf := make([]byte, needed, newCap)
		copy(newBuf, a.buf)
		a.buf = newBuf
	}
	return a.buf[start:needed:needed]
}

// growCap returns the capacity of a new backing array: twice have, or want
// when want is larger.
//
// have*2 overflows for an arena above [math.MaxInt]/2 bytes, about 4.6 EB
// on a 64-bit platform, which exceeds addressable memory.
func growCap(have, want int) int {
	return max(have*2, want)
}

// Marker is a position in an arena, returned by [Arena.Mark] for
// [Arena.SliceSince] and [Arena.TruncateTo]. It records the write position
// and the arena's lifecycle [epoch.Epoch]. A Marker from before an
// [Arena.Reset] or [Arena.Shrink] is stale, and both methods refuse it, so
// it cannot slice or rewind the bytes of a later lifecycle.
type Marker struct {
	pos   int
	epoch epoch.Epoch
}

// Mark returns a [Marker] of the current write position and lifecycle
// epoch. With [Arena.SliceSince], it returns a region that two or more
// [Arena.Append] and [Arena.Alloc] calls built as one sub-slice.
//
// A Marker is valid only in the lifecycle that created it. After
// [Arena.Reset] or [Arena.Shrink], [Arena.SliceSince] returns nil for it.
//
// # Allocation contract
//
// Zero alloc.
func (a *Arena) Mark() Marker {
	return Marker{pos: len(a.buf), epoch: a.epoch}
}

// SliceSince returns the sub-slice from m to the current end of the arena,
// with its capacity capped at its length. It returns nil when m is from an
// earlier lifecycle, before an [Arena.Reset] or [Arena.Shrink], and when
// m is at or past the current end.
//
// # Allocation contract
//
// Zero alloc.
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
// Zero alloc.
func (a *Arena) Len() int {
	return len(a.buf)
}

// Cap returns the capacity of the backing buffer.
//
// # Allocation contract
//
// Zero alloc.
func (a *Arena) Cap() int {
	return cap(a.buf)
}

// Bytes returns every byte appended since the last [Arena.Reset], with the
// capacity capped at the length, so an [append] to it cannot write into
// the arena's spare capacity.
//
// Bytes follows the lifetime rules of [Arena.Append]. After a
// reallocation, a slice from an earlier Bytes call no longer aliases the
// arena, and after [Arena.Reset] its bytes may be overwritten. See
// [Arena].
//
// # Allocation contract
//
// Zero alloc.
func (a *Arena) Bytes() []byte {
	end := len(a.buf)
	return a.buf[:end:end]
}
