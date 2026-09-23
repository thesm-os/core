// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package arena

// Reset clears the arena for reuse. The backing buffer's
// length drops to zero but its capacity is preserved — the
// next [Arena.Append] / [Arena.Alloc] writes from offset 0
// without reallocating.
//
// Reset zeroes every byte written since the previous Reset,
// including bytes an [Arena.TruncateTo] rewind or a failed
// [Arena.AppendVia] left past the length. The next user of a
// pooled arena therefore finds only zeros in its spare
// capacity, which [Arena.AppendVia] hands to its appender.
//
// Reset advances the arena's lifecycle epoch, invalidating
// every previously-returned [Marker]: subsequent
// [Arena.SliceSince] calls with a stale Marker return nil
// rather than silently slicing into the new lifecycle's
// bytes.
//
// Sub-slices previously returned by [Arena.Append],
// [Arena.Alloc], [Arena.SliceSince], or [Arena.Bytes] remain
// valid Go slices but their backing bytes may be overwritten
// by the next append — silent corruption if a stale slice is
// read after Reset. Treat Reset as the boundary at which all
// borrowed sub-slices become invalid.
//
// Reset satisfies the [go.thesmos.sh/core/pool.Resettable]
// constraint, so [*Arena] can be pooled via
// [go.thesmos.sh/core/pool.NewResetPool].
//
// # Allocation contract
//
// Zero-alloc. Runs in time proportional to the bytes written since
// the previous Reset, or to the capacity after a failed
// [Arena.AppendVia].
func (a *Arena) Reset() {
	// Bounded by the capacity: an appender may hand AppendVia back a
	// smaller buffer than the one the dirty mark was taken on.
	clear(a.buf[:min(max(a.dirty, len(a.buf)), cap(a.buf))])
	a.buf = a.buf[:0]
	a.dirty = 0
	a.epoch = a.epoch.Successor()
}

// CapExceeds reports whether the backing buffer's capacity
// exceeds maxCap. A pool wrapper consults this between Reset
// and Put to discard arenas that grew beyond a threshold
// during anomalous load — without it, one outlier request
// permanently bloats the pool.
//
// The name reports the observation ("capacity exceeds the
// threshold"); the consumer decides whether to shrink, log,
// or escalate.
//
// # Allocation contract
//
// Zero-alloc.
func (a *Arena) CapExceeds(maxCap int) bool {
	return cap(a.buf) > maxCap
}

// Shrink releases the backing buffer. The arena remains
// usable — the next [Arena.Append] / [Arena.Alloc] allocates
// a fresh buffer.
//
// Like [Arena.Reset], Shrink advances the lifecycle epoch:
// previously-returned [Marker] values are invalidated, and
// previously-returned sub-slices may be garbage-collected
// once the caller drops their last reference.
//
// # Allocation contract
//
// Zero-alloc (drops the slice header reference).
func (a *Arena) Shrink() {
	a.buf = nil
	a.dirty = 0
	a.epoch = a.epoch.Successor()
}
