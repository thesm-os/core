// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tlog

import (
	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/pool"
)

// The prefixes RFC 9162 gives a leaf and an interior node. Both fall in
// the unary half of [crypto.Role], so HashTagged accepts both.
const (
	leafRole crypto.Role = 0x00
	nodeRole crypto.Role = 0x01
)

// nodePrefix is the node prefix in static memory, so writing it to a
// Stream does not allocate.
var nodePrefix = [1]byte{byte(nodeRole)}

// scratchPool supplies the buffer that [NodeHash] writes two digests
// into. A digest passed to a Stream directly would escape to the heap.
var scratchPool = pool.NewPool(func() *[2 * crypto.MaxDigestSize]byte {
	return new([2 * crypto.MaxDigestSize]byte)
})

// LeafHash returns the RFC 9162 hash of a leaf: HASH(0x00 || data).
//
// It is h.HashTagged with role 0x00, so it costs what HashTagged costs.
//
// # Allocation contract
//
// Zero alloc on the warm path, as HashTagged is.
func LeafHash(h crypto.Hasher, data []byte) crypto.Digest {
	return h.HashTagged(leafRole, data)
}

// NodeHash returns the RFC 9162 hash of an interior node:
// HASH(0x01 || left || right).
//
// RFC 9162 prefixes a node with 0x01, which falls in the unary half of
// [crypto.Role], so CombineTagged cannot produce it. NodeHash writes left
// and right into pooled scratch and calls h.HashTagged with role 0x01
// over it, which gives the RFC's bytes.
//
// # Allocation contract
//
// Zero alloc on the warm path. The scratch comes from a package pool.
func NodeHash(h crypto.Hasher, left, right crypto.Digest) crypto.Digest {
	scratch := scratchPool.Get()
	n := copy(scratch[:], left.Bytes())
	n += copy(scratch[n:], right.Bytes())
	d := h.HashTagged(nodeRole, scratch[:n])
	scratchPool.Put(scratch)

	return d
}

// nodeHash returns the node hash of left and right through s, which the
// caller borrows once for every node it hashes. scratch has room for the
// two digests. A digest written to s directly would escape to the heap.
func nodeHash(s crypto.Stream, scratch []byte, left, right crypto.Digest) crypto.Digest {
	n := copy(scratch, left.Bytes())
	n += copy(scratch[n:], right.Bytes())

	return pairHash(s, scratch[:n])
}

// pairHash returns HASH(0x01 || pair) through s, for pair the two
// children of a node written side by side.
func pairHash(s crypto.Stream, pair []byte) crypto.Digest {
	s.Reset()
	_, _ = s.Write(nodePrefix[:])
	_, _ = s.Write(pair)

	return s.Sum()
}

// subtreeRoot returns the root of the perfect subtree whose leaves are
// the hashes in data, each size bytes long. The number of hashes is a
// power of two. subtreeRoot folds in scratch, which has room for at
// least len(data) bytes, and hashes through s.
func subtreeRoot(s crypto.Stream, size int, data, scratch []byte) crypto.Digest {
	n := copy(scratch, data)

	// A full tile of 256 hashes halves to one in tileHeight passes.
	for range tileHeight {
		if n <= size {
			break
		}

		n /= 2
		for j := 0; j*size < n; j++ {
			d := pairHash(s, scratch[2*j*size:(2*j+2)*size])
			copy(scratch[j*size:], d.Bytes())
		}
	}

	d, _ := crypto.DigestFromBytes(scratch[:size]) //nolint:errcheck // size is the hasher's own digest size

	return d
}
