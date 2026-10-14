// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package arena

// Reset clears the arena for reuse. The length drops to zero and the
// capacity remains, so the next [Arena.Append] or [Arena.Alloc] writes from
// offset 0 without a new backing array.
//
// Reset zeroes every byte written since the previous Reset, including the
// bytes that an [Arena.TruncateTo] rewind or a failed [Arena.AppendVia]
// left past the length. The next user of a pooled arena therefore finds
// only zeros in its spare capacity, which [Arena.AppendVia] hands to its
// appender. A caller that took the capacity with one [Arena.Alloc] and
// wrote a prefix of it calls [Arena.ResetWritten] to zero only the prefix.
//
// Reset advances the lifecycle epoch of the arena. [Arena.SliceSince]
// returns nil for a [Marker] from before the Reset, and [Arena.TruncateTo]
// refuses it, so neither slices nor rewinds the bytes of the next
// lifecycle.
//
// A sub-slice that [Arena.Append], [Arena.Alloc], [Arena.SliceSince] or
// [Arena.Bytes] returned before the Reset remains a valid Go slice, but the
// next append may overwrite its bytes. A read of such a slice after the
// Reset returns zeros or the data of the next lifecycle. Every borrowed
// sub-slice is invalid from the Reset on.
//
// Reset satisfies the [go.thesmos.sh/core/pool.Resettable] constraint, so
// [go.thesmos.sh/core/pool.NewResetPool] builds a pool of arenas.
//
// # Allocation contract
//
// Zero alloc. Runs in time proportional to the bytes written since the
// previous Reset, or to the capacity after a failed [Arena.AppendVia].
func (a *Arena) Reset() {
	a.ResetWritten(len(a.buf))
}

// ResetWritten ends the lifecycle of the arena as [Arena.Reset] does, for a
// caller that wrote only the first n bytes since the previous Reset. It
// zeroes those bytes and the bytes that an [Arena.TruncateTo] rewind or a
// failed [Arena.AppendVia] left written. It does not zero the rest of the
// length. Like Reset, it sets the length to zero, keeps the capacity and
// advances the lifecycle epoch.
//
// A caller that takes the capacity with one [Arena.Alloc] and fills a
// prefix passes the end of the prefix, so ResetWritten clears only the
// bytes that the lifecycle used. The first ResetWritten of a new arena
// touches only the pages of those bytes.
//
// An n above the length counts as the length, because every region of
// Append and Alloc ends at or before it. A negative n counts as the length
// too, so ResetWritten then zeroes what Reset zeroes.
//
// # Preconditions
//
// Every byte of the length at or past n must be zero. ResetWritten does not
// check it, because the check would read the bytes that it skips. A byte
// that the caller wrote past n remains in the spare capacity, and a later
// Alloc returns it in place of a zero.
//
// # Allocation contract
//
// Zero alloc. Runs in time proportional to the larger of n and the bytes
// that a TruncateTo rewind or a failed AppendVia left past the length.
func (a *Arena) ResetWritten(n int) {
	if n < 0 {
		n = len(a.buf)
	}

	// The capacity bounds the clear, because an appender may hand AppendVia
	// a smaller buffer than the one that set the dirty mark.
	clear(a.buf[:min(max(a.dirty, min(n, len(a.buf))), cap(a.buf))])
	a.buf = a.buf[:0]
	//dokimi:mutate-skip sbr-delete: a stale dirty mark only widens what the next Reset and Alloc clear
	a.dirty = 0
	a.epoch = a.epoch.Successor()
}

// CapExceeds reports whether the capacity of the backing buffer exceeds
// maxCap. A pool calls it between Reset and Put, and releases an arena that
// grew past the threshold under an unusual load. Without the check, the
// pool keeps the buffer of one large request.
//
// CapExceeds only reports the capacity. The caller decides whether to
// shrink the arena, to log the size or to keep the arena.
//
// # Allocation contract
//
// Zero alloc.
func (a *Arena) CapExceeds(maxCap int) bool {
	return cap(a.buf) > maxCap
}

// Shrink releases the backing buffer. The arena remains usable, and the
// next [Arena.Append] or [Arena.Alloc] allocates a new buffer.
//
// Like [Arena.Reset], Shrink advances the lifecycle epoch, so every
// [Marker] of the arena becomes stale. The collector frees the old buffer
// once the caller drops the last sub-slice of it.
//
// # Allocation contract
//
// Zero alloc. Shrink only drops the reference to the buffer.
func (a *Arena) Shrink() {
	a.buf = nil
	//dokimi:mutate-skip sbr-delete: a stale dirty mark only widens what the next Reset and Alloc clear
	a.dirty = 0
	a.epoch = a.epoch.Successor()
}
