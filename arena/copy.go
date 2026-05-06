// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package arena

// CopyOut allocates a single contiguous byte slice containing
// every byte appended since the last [Arena.Reset] and returns
// it. The returned slice is caller-owned — the arena can be
// reset immediately after.
//
// # Allocation contract
//
// Exactly one allocation, sized to [Arena.Len]. Returns nil
// when the arena is empty. Sustained-throughput callers should
// prefer [Arena.CopyOutTo] with a reused destination buffer.
func (a *Arena) CopyOut() []byte {
	if len(a.buf) == 0 {
		return nil
	}
	out := make([]byte, len(a.buf))
	copy(out, a.buf)
	return out
}

// CopyOutTo appends every byte in the arena to dst and
// returns the extended slice. The caller owns the returned
// buffer — the arena can be reset immediately after.
//
// # Allocation contract
//
// Zero-alloc when dst has sufficient capacity. Reallocates
// when dst's capacity is exceeded (standard [append]
// semantics).
func (a *Arena) CopyOutTo(dst []byte) []byte {
	return append(dst, a.buf...)
}

// RebaseSlices rebases the supplied sub-slices in place into
// a single contiguous allocation and returns the backing
// array. Each rebased entry uses a three-index expression
// capped at its length, so downstream [append] cannot extend
// one entry into another's region.
//
// The intended use is consolidating outputs from one or more
// arenas into a single caller-owned allocation at the
// ownership-transfer boundary — typically before resetting
// the source arenas:
//
//	a := arena.New()
//	b := arena.New()
//	entries := [][]byte{
//	    a.Append(payloadA),
//	    b.Append(payloadB),
//	    a.Append(metaA),
//	}
//	owned := arena.RebaseSlices(entries)  // entries now alias `owned`
//	a.Reset()                              // safe: entries no longer alias `a`
//	b.Reset()
//
// The input slices may originate in any [Arena] (or none),
// may overlap in source memory, and may be header-equal
// across indices: RebaseSlices reads each entry's content
// and writes into the fresh allocation in index order, then
// rewrites the slice header at that index. The
// read-then-rewrite-per-index pattern is independent of
// source aliasing.
//
// # Allocation contract
//
// Exactly one allocation sized to the total bytes across
// every entry, when at least one entry is non-empty. Returns
// nil and performs no allocation when slices is empty or
// every entry is empty.
//
// Sustained-throughput callers that already own a destination
// buffer should prefer [RebaseSlicesTo] (zero-alloc), which
// writes into a caller-supplied buffer instead of allocating
// — the same relationship as [Arena.CopyOut] vs
// [Arena.CopyOutTo].
func RebaseSlices(slices [][]byte) []byte {
	if len(slices) == 0 {
		return nil
	}
	totalData := 0
	for i := range slices {
		totalData += len(slices[i])
	}
	if totalData == 0 {
		return nil
	}
	out := make([]byte, 0, totalData)
	for i := range slices {
		start := len(out)
		out = append(out, slices[i]...)
		end := len(out)
		slices[i] = out[start:end:end]
	}
	return out
}

// RebaseSlicesTo rebases the supplied sub-slices in place
// into dst and returns the extended slice. Each rebased
// entry is three-index-capped at its length.
//
// # Allocation contract
//
// Zero-alloc when dst has sufficient capacity to hold every
// entry's bytes. Reallocates per [append]'s growth rule when
// capacity is exceeded.
func RebaseSlicesTo(dst []byte, slices [][]byte) []byte {
	for i := range slices {
		start := len(dst)
		dst = append(dst, slices[i]...)
		end := len(dst)
		slices[i] = dst[start:end:end]
	}
	return dst
}
