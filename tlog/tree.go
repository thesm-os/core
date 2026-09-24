// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tlog

import (
	"math/bits"
	"slices"

	"go.thesmos.sh/core/crypto"
)

// maxPath bounds the number of hashes in a proof over a tree of up to
// 2^64 leaves. It also bounds every loop that walks the levels of a
// tree or the bits of a size, so no fault in such a loop can make it
// run forever or grow memory without end.
const maxPath = 66

// span is the range of leaves [lo, hi) under one node of a proof.
type span struct {
	lo, hi uint64
}

// split returns k, the largest power of two below n, for n of at least
// two. RFC 9162 splits a tree of n leaves into its first k leaves and
// the rest.
func split(n uint64) uint64 {
	return 1 << (bits.Len64(n-1) - 1)
}

// inclusionSpans appends to dst the leaf ranges whose hashes form the
// audit path of the leaf at index within [lo, hi), in proof order. It
// follows PATH of RFC 9162, section 2.1.3.1, from the root down, and
// then reverses the path so the deepest sibling comes first.
func inclusionSpans(lo, hi, index uint64, dst []span) []span {
	start := len(dst)

	for range maxPath {
		if hi-lo <= 1 {
			break
		}

		k := split(hi - lo)
		if index < lo+k {
			dst = append(dst, span{lo + k, hi})
			hi = lo + k
		} else {
			dst = append(dst, span{lo, lo + k})
			lo += k
		}
	}

	slices.Reverse(dst[start:])

	return dst
}

// consistencySpans appends to dst the leaf ranges whose hashes form the
// consistency proof between the tree of the first old leaves and the
// tree [lo, hi), in proof order. It follows SUBPROOF of RFC 9162,
// section 2.1.4.1, from the root down, and then reverses the proof so
// the deepest subtree comes first. The proof leaves out the old tree's
// root when the old tree is a subtree of the new one, because the
// verifier has it.
func consistencySpans(lo, hi, old uint64, dst []span) []span {
	start := len(dst)
	whole := true

	for range maxPath {
		if old == hi {
			if !whole {
				dst = append(dst, span{lo, hi})
			}

			break
		}

		k := split(hi - lo)
		if old <= lo+k {
			dst = append(dst, span{lo + k, hi})
			hi = lo + k
		} else {
			dst = append(dst, span{lo, lo + k})
			lo += k
			whole = false
		}
	}

	slices.Reverse(dst[start:])

	return dst
}

// Root returns the Merkle Tree Hash of the tree whose leaf hashes are
// leaves. The root of an empty tree is HASH() over no input, as RFC 9162
// defines it.
//
// # Allocation contract
//
// Zero alloc on the warm path. The fold keeps one digest per tree level
// on the stack.
func Root(h crypto.Hasher, leaves []crypto.Digest) crypto.Digest {
	if len(leaves) == 0 {
		return h.Hash(nil)
	}

	scratch := scratchPool.Get()
	defer scratchPool.Put(scratch)
	s := h.NewStream()
	defer s.Close()

	// stack lists the roots of the perfect subtrees of the leaves seen so
	// far, largest first. A tree of up to 2^64 leaves has at most 64.
	var stack [64]crypto.Digest

	n := 0
	for i, leaf := range leaves {
		stack[n] = leaf //nolint:gosec // G602: n is the number of set bits of i+1, at most 64
		n++
		for j := uint64(i); j&1 == 1; j >>= 1 {
			n--
			stack[n-1] = nodeHash(s, scratch[:], stack[n-1], stack[n])
		}
	}

	//nolint:gosec // G602: leaves is not empty, so n is at least one
	root := stack[n-1]
	for k := n - 2; k >= 0; k-- {
		root = nodeHash(s, scratch[:], stack[k], root)
	}

	return root
}

// InclusionProof appends to dst the audit path for the leaf at index in
// the tree whose leaf hashes are leaves, and returns the extended slice.
//
// Returns dst unchanged and [ErrRange] when index is not below
// len(leaves).
//
// # Allocation contract
//
// Allocates only to grow dst. It hashes every leaf once per level of
// the path.
func InclusionProof(
	h crypto.Hasher, leaves []crypto.Digest, index uint64, dst []crypto.Digest,
) ([]crypto.Digest, error) {
	if index >= uint64(len(leaves)) {
		return dst, ErrRange
	}

	var spans [maxPath]span
	for _, s := range inclusionSpans(0, uint64(len(leaves)), index, spans[:0]) {
		dst = append(dst, Root(h, leaves[s.lo:s.hi]))
	}

	return dst, nil
}

// ConsistencyProof appends to dst the proof that the tree of the first
// oldSize leaves is a prefix of the tree of all of leaves, and returns
// the extended slice.
//
// Returns dst unchanged and [ErrRange] unless 0 < oldSize <=
// len(leaves). When oldSize equals len(leaves), the proof is empty.
//
// # Allocation contract
//
// Allocates only to grow dst.
func ConsistencyProof(
	h crypto.Hasher, leaves []crypto.Digest, oldSize uint64, dst []crypto.Digest,
) ([]crypto.Digest, error) {
	if oldSize == 0 || oldSize > uint64(len(leaves)) {
		return dst, ErrRange
	}

	var spans [maxPath]span
	for _, s := range consistencySpans(0, uint64(len(leaves)), oldSize, spans[:0]) {
		dst = append(dst, Root(h, leaves[s.lo:s.hi]))
	}

	return dst, nil
}
