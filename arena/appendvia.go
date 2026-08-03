// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package arena

// AppendVia lets an appender write directly into the arena's buffer
// and adopts the result.
//
// fn receives the arena's backing slice and returns it extended,
// which is exactly [encoding.BinaryAppender]'s shape — so a type
// implementing that interface writes into arena capacity with no
// adapter:
//
//	region, err := a.AppendVia(digest.AppendBinary)
//
// That is the gap this closes. [Arena.Append] needs bytes that
// already exist and [Arena.Alloc] needs a length known in advance, so
// before this an appender had to encode into a scratch buffer and
// copy — one allocation and one copy per item, which is what the
// arena exists to avoid.
//
// The returned slice covers only the bytes fn wrote, three-index
// capped like every other arena region so a downstream [append]
// cannot reach into a neighbour.
//
// # On error
//
// The arena is truncated to its pre-call extent, so a failed encode
// leaves no partial region for the next append to run into. Whatever
// fn managed to write before failing is discarded, and no region is
// returned.
//
// # Growth
//
// fn may write past the arena's spare capacity. Its [append] then
// reallocates and the arena adopts the new backing array, exactly as
// [Arena.Append] does when it grows — with the same consequence for
// previously-returned sub-slices, which are left pointing at the
// orphaned array. See [Arena] for that contract.
//
// # Allocation contract
//
// Zero-alloc when the backing buffer has capacity for what fn writes.
func (a *Arena) AppendVia(fn func(dst []byte) ([]byte, error)) ([]byte, error) {
	start := len(a.buf)

	out, err := fn(a.buf)
	if err != nil {
		// fn's writes went past len(a.buf) — into spare capacity or
		// onto an array it reallocated — so the arena's own length
		// still describes the pre-call extent. Re-slicing makes that
		// explicit rather than relying on it.
		a.buf = a.buf[:start]

		return nil, err
	}

	a.buf = out
	end := len(a.buf)

	return a.buf[start:end:end], nil
}

// TruncateTo rewinds the arena to the extent captured by m,
// discarding everything appended since.
//
// Returns false without modifying the arena when m is stale — from
// before an [Arena.Reset] or [Arena.Shrink] — mirroring the epoch
// check in [Arena.SliceSince]. Rewinding on a stale marker would cut
// into the current lifecycle's bytes at a position that meant
// something else.
//
// Unlike Reset, this does NOT advance the epoch: it rewinds within
// one lifecycle, so markers taken before m remain valid and still
// describe the positions they always did.
//
// Capacity is preserved; only the length moves. Sub-slices covering
// discarded bytes remain readable until the next append overwrites
// them, and must be treated as invalid from here — the same rule
// Reset carries.
//
// # Allocation contract
//
// Zero-alloc.
func (a *Arena) TruncateTo(m Marker) bool {
	if m.epoch != a.epoch || m.pos > len(a.buf) {
		return false
	}

	a.buf = a.buf[:m.pos]

	return true
}
