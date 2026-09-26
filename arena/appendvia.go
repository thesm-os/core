// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package arena

// AppendVia calls fn with the arena's backing buffer and adopts the bytes
// fn appends. fn has the signature of the AppendBinary method of
// [encoding.BinaryAppender], so a type that implements the interface
// writes into the arena's spare capacity without a scratch buffer:
//
//	region, err := a.AppendVia(digest.AppendBinary)
//
// On success, fn must write no byte past the end of the slice it returns,
// as an AppendBinary method does. [Arena.Reset] and [Arena.Alloc] rely on
// the spare capacity past that slice being unchanged.
//
// The returned slice covers only the bytes fn wrote, with its capacity
// capped at its length, as for every other arena region.
//
// # On error
//
// The arena returns to its length before the call, so a failed encode
// leaves no partial region for the next append. The bytes fn wrote are
// discarded, and no region is returned.
//
// # Growth
//
// fn may write past the arena's spare capacity. Its [append] then
// reallocates, and the arena uses the new backing array from then on, as
// after an [Arena.Append] that grows. Sub-slices returned earlier keep
// pointing at the old array. See [Arena] for that contract.
//
// # Allocation contract
//
// Zero alloc when the backing buffer has room for what fn writes.
func (a *Arena) AppendVia(fn func(dst []byte) ([]byte, error)) ([]byte, error) {
	start := len(a.buf)

	out, err := fn(a.buf)
	if err != nil {
		// fn wrote past len(a.buf), into the spare capacity or into an
		// array it allocated, so a.buf[:start] is the arena before the
		// call. How far fn wrote into the spare capacity is unknown, so
		// the dirty mark covers the whole spare capacity.
		a.dirty = cap(a.buf)
		a.buf = a.buf[:start]

		return nil, err
	}

	a.buf = out
	end := len(a.buf)

	return a.buf[start:end:end], nil
}

// TruncateTo rewinds the arena to the position m recorded and discards
// every byte appended since.
//
// It returns false and leaves the arena unchanged when m is from an
// earlier lifecycle, before an [Arena.Reset] or [Arena.Shrink], as
// [Arena.SliceSince] does. A stale position refers to the bytes of another
// lifecycle.
//
// TruncateTo does not advance the epoch, because it rewinds within one
// lifecycle. A Marker taken before m remains valid and records the same
// position.
//
// The capacity does not change. Sub-slices over the discarded bytes remain
// readable until the next append overwrites them, and are invalid from
// this call on, as after [Arena.Reset].
//
// # Allocation contract
//
// Zero alloc.
func (a *Arena) TruncateTo(m Marker) bool {
	if m.epoch != a.epoch || m.pos > len(a.buf) {
		return false
	}

	a.dirty = max(a.dirty, len(a.buf))
	a.buf = a.buf[:m.pos]

	return true
}
