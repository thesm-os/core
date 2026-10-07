// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tlog

import (
	"fmt"
	"math/bits"

	"go.thesmos.sh/core/crypto"
)

// TaggedTree is the tagged tree that [TaggedRoot] computes over a list of
// leaves, with every node kept. [TaggedTree.Reset] hashes each interior
// node once, and [TaggedTree.Root] and [TaggedTree.InclusionProof] hash
// nothing. The paths of all n leaves of a tree therefore cost n - 1 node
// hashes, where n calls to [TaggedInclusionProof] cost about n².
//
// The zero TaggedTree has no leaves.
//
// # Concurrency
//
// Not safe for concurrent use. Size, Root and InclusionProof do not
// modify the tree, so goroutines may call them concurrently between calls
// to Reset.
//
// # Allocation contract
//
// Reset allocates only to grow the tree's storage, which is about twice as
// many digests as leaves. A TaggedTree that contained a tree of at least as
// many leaves allocates nothing. InclusionProof allocates only to grow
// dst.
type TaggedTree struct {
	// nodes contains every level of the tree in order: the leaves, each
	// level above them, and the root last. The level above a level of
	// width w has width above(w).
	nodes []crypto.Digest

	// size is the number of leaves, the width of the first level.
	size uint64
}

// Reset makes t the tagged tree over leaves whose interior nodes are
// h.CombineTagged(node, left, right), for node a binary role. t keeps a
// copy of leaves.
//
// Panics when node is a unary role or leaves is empty, and, through
// CombineTagged, when a leaf it combines is not a digest of h. t has no
// leaves after a panic.
//
// # Allocation contract
//
// Allocates only when t has not contained a tree of as many leaves before.
// It hashes each of the len(leaves) - 1 interior nodes once.
func (t *TaggedTree) Reset(h crypto.Hasher, node crypto.Role, leaves []crypto.Digest) {
	t.size = 0

	requireBinary(node)
	requireLeaves(uint64(len(leaves)))

	t.nodes = append(t.nodes[:0], leaves...)

	// start is the index in t.nodes of the first node of the level that
	// the loop pairs, and width is the width of that level. A tree of n
	// leaves has bits.Len64(n - 1) levels above its leaves, the last of
	// width one, the root.
	start, width := uint64(0), uint64(len(leaves))
	//dokimi:mutate-skip aor: a pass past the root appends a copy of the root after it, which Root returns as the root
	for range bits.Len64(width - 1) {
		end := start + width
		for i := start; i+1 < end; i += 2 {
			t.nodes = append(t.nodes, h.CombineTagged(node, t.nodes[i], t.nodes[i+1]))
		}

		if width%2 == 1 {
			t.nodes = append(t.nodes, t.nodes[end-1])
		}

		start, width = end, above(width)
	}

	t.size = uint64(len(leaves))
}

// Size returns the number of leaves in t.
func (t *TaggedTree) Size() uint64 { return t.size }

// Root returns the root of t, the digest that [TaggedRoot] returns for the
// same leaves and node role.
//
// Panics when t has no leaves.
func (t *TaggedTree) Root() crypto.Digest {
	requireLeaves(t.size)

	return t.nodes[len(t.nodes)-1]
}

// InclusionProof appends to dst the audit path of the leaf at index, the
// path that [TaggedInclusionProof] returns for the same leaves and node
// role, and returns the extended slice. It reads the path from the kept
// nodes and hashes nothing.
//
// Returns dst unchanged and [ErrRange] when index is not below
// [TaggedTree.Size].
//
// # Allocation contract
//
// Allocates only to grow dst.
func (t *TaggedTree) InclusionProof(index uint64, dst []crypto.Digest) ([]crypto.Digest, error) {
	if index >= t.size {
		return dst, ErrRange
	}

	level, width := t.nodes, t.size
	for range maxPath {
		if width == 1 {
			break
		}

		if sibling := index ^ 1; sibling < width {
			dst = append(dst, level[sibling])
		}

		level, width = level[width:], above(width)
		index >>= 1
	}

	return dst, nil
}

// TaggedRoot returns the root of the tagged tree over leaves: the tree of
// RFC 9162's shape whose interior nodes are h.CombineTagged(node, left,
// right), for node a binary role. The leaves are digests that the
// protocol has already computed, and TaggedRoot does not hash them again.
// The root of one leaf is that leaf.
//
// A tagged tree over no leaves has no root. Every root that TaggedRoot
// returns is a leaf or a node hashed under node, and a value for an empty
// tree would be neither. A protocol whose tree can be empty chooses that
// value itself.
//
// Panics when node is a unary role or leaves is empty, and, through
// CombineTagged, when a leaf it combines is not a digest of h.
//
// # Allocation contract
//
// Zero alloc. The fold keeps one digest per tree level on the stack, and
// it hashes each of the len(leaves) - 1 interior nodes once.
func TaggedRoot(h crypto.Hasher, node crypto.Role, leaves []crypto.Digest) crypto.Digest {
	requireBinary(node)
	requireLeaves(uint64(len(leaves)))

	return taggedFold(h, node, leaves)
}

// TaggedInclusionProof appends to dst the audit path of the leaf at index
// in the tagged tree that [TaggedRoot] computes over leaves, and returns
// the extended slice. The path is PATH of RFC 9162, section 2.1.3.1, over
// the tagged tree's nodes.
//
// Returns dst unchanged and [ErrRange] when index is not below
// len(leaves). Panics when node is a unary role, and, through
// CombineTagged, when a leaf it combines is not a digest of h.
//
// # Allocation contract
//
// Allocates only to grow dst. One call hashes about len(leaves) nodes, so
// the paths of every leaf of one tree cost about len(leaves)² node hashes
// through this function. A [TaggedTree] returns every path after hashing
// each node once.
func TaggedInclusionProof(
	h crypto.Hasher, node crypto.Role, leaves []crypto.Digest, index uint64, dst []crypto.Digest,
) ([]crypto.Digest, error) {
	requireBinary(node)

	if index >= uint64(len(leaves)) {
		return dst, ErrRange
	}

	var spans [maxPath]span
	for _, s := range inclusionSpans(0, uint64(len(leaves)), index, spans[:0]) {
		dst = append(dst, taggedFold(h, node, leaves[s.lo:s.hi]))
	}

	return dst, nil
}

// VerifyTaggedInclusion reports whether proof shows that leaf is the leaf
// at index in the tagged tree of size leaves with the given root, whose
// interior nodes are h.CombineTagged(node, left, right). It follows RFC
// 9162, section 2.1.3.2, as [VerifyInclusion] does.
//
// Returns [ErrRange] when index is not below size, and [ErrProof] when
// the proof does not recompute root or contains a hash whose size is not
// the size of leaf. A proof from an untrusted source therefore never makes
// CombineTagged panic. It returns ErrProof at the first hash of a proof
// longer than the path, without hashing it. Panics when node is a unary
// role, and, through CombineTagged, when leaf is not a digest of h.
//
// # Allocation contract
//
// Zero alloc.
func VerifyTaggedInclusion(
	h crypto.Hasher, node crypto.Role, index, size uint64, leaf, root crypto.Digest, proof []crypto.Digest,
) error {
	requireBinary(node)

	if index >= size {
		return ErrRange
	}

	fn, sn, r := index, size-1, leaf
	for _, p := range proof {
		if sn == 0 || p.Size() != leaf.Size() {
			return ErrProof
		}

		if fn&1 == 1 || fn == sn {
			r = h.CombineTagged(node, p, r)
			// fn equals sn here and sn is not zero, so the shift ends at
			// a set bit.
			fn, sn = shiftUntil(fn, sn, 1)
		} else {
			r = h.CombineTagged(node, r, p)
		}

		fn >>= 1
		sn >>= 1
	}

	if sn != 0 || !r.Equal(root) {
		return ErrProof
	}

	return nil
}

// taggedFold returns the root of the tagged tree over leaves, which must
// not be empty. It is the fold of [Root] with CombineTagged as the node
// hash: it stacks the roots of the perfect subtrees of the leaves read so
// far, then combines the stacked roots from the right. Both folds call
// their node hash directly, with no function value between the fold and
// the hash.
func taggedFold(h crypto.Hasher, node crypto.Role, leaves []crypto.Digest) crypto.Digest {
	// stack lists the roots of the perfect subtrees of the leaves seen so
	// far, largest first. A tree of up to 2^64 leaves has at most 64.
	var stack [64]crypto.Digest

	n := 0
	for i, leaf := range leaves {
		stack[n] = leaf //nolint:gosec // G602: n is the number of set bits of i+1, at most 64
		n++
		for j := uint64(i); j&1 == 1; j >>= 1 {
			n--
			stack[n-1] = h.CombineTagged(node, stack[n-1], stack[n])
		}
	}

	//nolint:gosec // G602: leaves is not empty, so n is at least one
	root := stack[n-1]
	for k := n - 2; k >= 0; k-- {
		root = h.CombineTagged(node, stack[k], root)
	}

	return root
}

// above returns the width of the level above a level of width nodes. Each
// pair of nodes, counted from the left, has one parent, and the last node
// of a level of odd width is also a node of the level above. Pairing from
// the left gives the tree RFC 9162's split of n leaves into the largest
// power of two below n and the rest.
func above(width uint64) uint64 {
	return (width + 1) / 2
}

// requireBinary panics unless node is a binary role. Every tagged function
// checks the role before it hashes, so a unary role fails for a tree of any
// size, and not only once two nodes are combined.
func requireBinary(node crypto.Role) {
	if !node.IsBinary() {
		panic(fmt.Sprintf( //nolint:forbidigo // a unary node role is a programmer error, as CombineTagged treats it
			"tlog: a tagged tree requires a binary node role (high bit set), got %#02x", byte(node),
		))
	}
}

// requireLeaves panics when n, a number of leaves, is zero, because a
// tagged tree over no leaves has no root.
func requireLeaves(n uint64) {
	if n == 0 {
		//nolint:forbidigo // an empty tree's value is the caller's choice, so asking for it is a programmer error
		panic("tlog: a tagged tree over no leaves has no root")
	}
}
