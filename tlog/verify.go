// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tlog

import "go.thesmos.sh/core/crypto"

// VerifyInclusion reports whether proof shows that leaf is the leaf at
// index in the tree of size leaves with the given root, following RFC
// 9162, section 2.1.3.2.
//
// Returns [ErrRange] when index is not below size, and [ErrProof] when
// the proof does not recompute root.
//
// # Allocation contract
//
// Zero alloc on the warm path.
func VerifyInclusion(h crypto.Hasher, index, size uint64, leaf, root crypto.Digest, proof []crypto.Digest) error {
	if index >= size {
		return ErrRange
	}

	scratch := scratchPool.Get()
	defer scratchPool.Put(scratch)
	s := h.NewStream()
	defer s.Close()

	fn, sn, r := index, size-1, leaf
	for _, p := range proof {
		if sn == 0 {
			return ErrProof
		}

		if fn&1 == 1 || fn == sn {
			r = nodeHash(s, *scratch, p, r)
			// fn equals sn here and sn is not zero, so the shift ends at
			// a set bit.
			fn, sn = shiftUntil(fn, sn, 1)
		} else {
			r = nodeHash(s, *scratch, r, p)
		}

		fn >>= 1
		sn >>= 1
	}

	if sn != 0 || !r.Equal(root) {
		return ErrProof
	}

	return nil
}

// VerifyConsistency reports whether proof shows that the tree of oldSize
// leaves with oldRoot is a prefix of the tree of newSize leaves with
// newRoot, following RFC 9162, section 2.1.4.2.
//
// Returns [ErrRange] unless 0 < oldSize <= newSize, and [ErrProof] when
// the proof does not recompute both roots. When the sizes are equal,
// the proof must be empty and the roots equal.
//
// # Allocation contract
//
// Zero alloc on the warm path.
func VerifyConsistency(
	h crypto.Hasher, oldSize, newSize uint64, oldRoot, newRoot crypto.Digest, proof []crypto.Digest,
) error {
	if oldSize == 0 || oldSize > newSize {
		return ErrRange
	}

	if oldSize == newSize {
		if len(proof) != 0 || !oldRoot.Equal(newRoot) {
			return ErrProof
		}

		return nil
	}

	if len(proof) == 0 {
		return ErrProof
	}

	// An old tree whose size is a power of two is a subtree of the new
	// one, and the proof leaves out its root, which the verifier has.
	fr, sr, rest := proof[0], proof[0], proof[1:]
	if oldSize&(oldSize-1) == 0 {
		fr, sr, rest = oldRoot, oldRoot, proof
	}

	fn, sn := shiftUntil(oldSize-1, newSize-1, 0)

	scratch := scratchPool.Get()
	defer scratchPool.Put(scratch)
	s := h.NewStream()
	defer s.Close()

	for _, c := range rest {
		if sn == 0 {
			return ErrProof
		}

		if fn&1 == 1 || fn == sn {
			fr = nodeHash(s, *scratch, c, fr)
			sr = nodeHash(s, *scratch, c, sr)
			fn, sn = shiftUntil(fn, sn, 1)
		} else {
			sr = nodeHash(s, *scratch, sr, c)
		}

		fn >>= 1
		sn >>= 1
	}

	if sn != 0 || !fr.Equal(oldRoot) || !sr.Equal(newRoot) {
		return ErrProof
	}

	return nil
}

// shiftUntil shifts fn and sn right together until the low bit of fn is
// bit, as RFC 9162 does between the steps of a verification, and
// returns them. A uint64 has 64 bits, so the loop ends within maxPath
// steps.
func shiftUntil(fn, sn, bit uint64) (shiftedFn, shiftedSn uint64) {
	for range maxPath {
		if fn&1 == bit {
			break
		}

		fn >>= 1
		sn >>= 1
	}

	return fn, sn
}
