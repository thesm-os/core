// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package tlog

import (
	"math/bits"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/pool"
)

// The prefixes RFC 9162 gives a leaf and an interior node. Both fall in
// the unary half of [crypto.Role], so HashTagged accepts both.
const (
	leafRole crypto.Role = 0x00
	nodeRole crypto.Role = 0x01
)

// nodeRoom is the room that the input of one node hash takes in scratch:
// the node prefix and two digests of the largest size.
const nodeRoom = 1 + 2*crypto.MaxDigestSize

// scratchPool supplies the buffer that a node hash writes its input into,
// so the hasher hashes the node with one call to Hash. A buffer on the
// stack would escape to the heap through the Hasher interface. The size is
// computed in the function body, where coverage counts it.
var scratchPool = pool.NewPool(func() *[]byte {
	b := make([]byte, nodeRoom)

	return &b
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
// [crypto.Role], so CombineTagged cannot produce it. NodeHash writes the
// prefix, left and right into pooled scratch and hashes them with h.Hash,
// which gives the RFC's bytes in one call.
//
// # Allocation contract
//
// Zero alloc on the warm path. The scratch comes from a package pool.
func NodeHash(h crypto.Hasher, left, right crypto.Digest) crypto.Digest {
	scratch := scratchPool.Get()
	d := nodeHash(h, *scratch, left, right)
	scratchPool.Put(scratch)

	return d
}

// nodeHash writes the node prefix and the digests left and right into
// scratch, and returns their hash from one call to h.Hash. scratch has
// nodeRoom bytes.
func nodeHash(h crypto.Hasher, scratch []byte, left, right crypto.Digest) crypto.Digest {
	scratch[0] = byte(nodeRole)
	n := 1 + copy(scratch[1:], left.Bytes())
	n += copy(scratch[n:], right.Bytes())

	return h.Hash(scratch[:n])
}

// pairHash returns HASH(0x01 || pair), for pair the two children of a node
// written side by side. It writes the node prefix and pair into buf, which
// has room for both, and hashes them with one call to h.Hash.
func pairHash(h crypto.Hasher, buf, pair []byte) crypto.Digest {
	buf[0] = byte(nodeRole)
	n := 1 + copy(buf[1:], pair)

	return h.Hash(buf[:n])
}

// subtreeRoot returns the root of the perfect subtree whose leaves are
// the hashes in data, each size bytes long. The number of hashes is a
// power of two. subtreeRoot folds in scratch, which has room for
// len(data) + nodeRoom bytes: the hashes of the fold, then the input of
// one node hash.
func subtreeRoot(h crypto.Hasher, size int, data, scratch []byte) crypto.Digest {
	copy(scratch, data)
	buf := scratch[len(data):]

	// A power of two of hashes halves to one in as many passes as its
	// trailing zero bits, eight for a full tile. Each pass halves count and
	// hashes each pair of the level into the first half of scratch.
	count := len(data) / size
	for range bits.TrailingZeros(uint(count)) {
		count /= 2
		for j := range count {
			d := pairHash(h, buf, scratch[2*j*size:(2*j+2)*size])
			copy(scratch[j*size:], d.Bytes())
		}
	}

	d, _ := crypto.DigestFromBytes(scratch[:size])

	return d
}
