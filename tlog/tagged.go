// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package tlog

import (
	"fmt"
	"math/bits"
	"slices"

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

// RangeProof appends to dst the range proof of the leaves [begin, end) of
// t, the proof that [TaggedRangeProof] returns for the same leaves and node
// role, and returns the extended slice. It reads the proof from the kept
// nodes and hashes nothing.
//
// Returns dst unchanged and [ErrRange] when begin is not below end, and
// when end is above [TaggedTree.Size].
//
// # Allocation contract
//
// Allocates only to grow dst.
func (t *TaggedTree) RangeProof(begin, end uint64, dst []crypto.Digest) ([]crypto.Digest, error) {
	if begin >= end || end > t.size {
		return dst, ErrRange
	}

	var (
		w     rangeWalk
		spans [2 * maxPath]span
	)

	w.walk(t.size, begin, end)
	for _, s := range w.spans(spans[:0]) {
		// The node over the leaves of s is on the level of the smallest
		// perfect tree that holds them, and the index of s.lo there is its
		// index on that level.
		level := bits.Len64(s.hi - s.lo - 1)

		nodes, width := t.nodes, t.size
		for range level {
			nodes, width = nodes[width:], above(width)
		}

		dst = append(dst, nodes[s.lo>>level])
	}

	return dst, nil
}

// TaggedFold computes the root of a tagged tree from its leaves, which it
// receives one at a time and in order: the root that [TaggedRoot] returns
// for the same leaves. It keeps the roots of the perfect subtrees of the
// leaves so far, one for each set bit of the number of leaves, so its state
// is 64 digests whatever the number of leaves.
//
// [TaggedFold.Reset] starts a fold. The zero TaggedFold has no leaves and
// no hasher: its Size is 0, its Root panics as for a fold of no leaves, and
// its Add panics. A fold has at most 2^64 - 1 leaves.
//
// # Concurrency
//
// Size and Root do not modify the fold, so goroutines may call them
// concurrently between calls to Reset and Add. Reset and Add are not safe
// for concurrent use.
//
// # Allocation contract
//
// Reset, Add, Size and Root allocate nothing. Over n leaves, Add hashes
// n - popcount(n) interior nodes in all, and each Root hashes
// popcount(n) - 1.
type TaggedFold struct {
	// h hashes the interior nodes under node.
	h crypto.Hasher

	// stack contains the roots of the perfect subtrees of the leaves so
	// far, largest first: one for each set bit of size.
	stack [64]crypto.Digest

	// size is the number of leaves that Add received since the last Reset.
	size uint64

	node crypto.Role
}

// Reset starts the fold of the tagged tree whose interior nodes are
// h.CombineTagged(node, left, right), for node a binary role. It drops the
// leaves of the last fold.
//
// Panics when node is a unary role, and leaves f unchanged.
func (f *TaggedFold) Reset(h crypto.Hasher, node crypto.Role) {
	requireBinary(node)

	f.h, f.node, f.size = h, node, 0
}

// Add folds leaf into f as the next leaf of the tree. It stacks the leaf as
// a subtree of one leaf, and combines the two smallest stacked roots once
// for each subtree that the leaf completes.
//
// Panics when Reset did not start f, and, through CombineTagged, when leaf
// is not a digest of the hasher of f.
func (f *TaggedFold) Add(leaf crypto.Digest) {
	if f.h == nil {
		//nolint:forbidigo // a fold without a hasher is a programmer error, as TaggedRoot over no leaves is
		panic("tlog: Add on a TaggedFold that Reset did not start")
	}

	// A fold of up to 2^64 - 1 leaves stacks at most 63 roots before the
	// leaf.
	n := bits.OnesCount64(f.size)
	f.stack[n] = leaf
	n++

	for j := f.size; j&1 == 1; j >>= 1 {
		n--
		f.stack[n-1] = f.h.CombineTagged(f.node, f.stack[n-1], f.stack[n])
	}

	f.size++
}

// Size returns the number of leaves that Add received since the last
// Reset.
func (f *TaggedFold) Size() uint64 { return f.size }

// Root returns the root of the tagged tree over the leaves that Add
// received, the root that [TaggedRoot] returns for the same leaves. It
// combines the stacked roots from the smallest up, and leaves f unchanged,
// so Add can continue after it.
//
// Panics when f has no leaves, as TaggedRoot does for an empty list.
func (f *TaggedFold) Root() crypto.Digest {
	requireLeaves(f.size)

	n := bits.OnesCount64(f.size)
	root := f.stack[n-1]
	for _, d := range slices.Backward(f.stack[:n-1]) {
		root = f.h.CombineTagged(f.node, d, root)
	}

	return root
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

// TaggedRangeProof appends to dst the range proof of the leaves [begin,
// end) in the tagged tree that [TaggedRoot] computes over leaves, and
// returns the extended slice. The proof contains the roots of the largest
// subtrees of RFC 9162's split that lie outside the range, each after the
// proof of the subtree beside it. Each of these groups lists its roots
// from the bottom up, and the groups follow one another in this order:
//
//   - the subtrees beside the edge from the smallest subtree that contains
//     the range down to begin;
//   - the subtrees beside the edge from that subtree down to the last leaf
//     of the range;
//   - the siblings on the path from the root down to that subtree.
//
// The proof of one leaf is the path that [TaggedInclusionProof] returns,
// and a range in a tree of n leaves has a proof of at most 2⌈log2 n⌉
// hashes. [TaggedRangeRoot] recomputes the root from the leaves of the
// range and the proof.
//
// Returns dst unchanged and [ErrRange] when begin is not below end, and
// when end is above len(leaves). Panics when node is a unary role, and,
// through CombineTagged, when a leaf it combines is not a digest of h.
//
// # Allocation contract
//
// Allocates only to grow dst. One call hashes about len(leaves) nodes. A
// [TaggedTree] returns the proof of every range after hashing each node
// once.
func TaggedRangeProof(
	h crypto.Hasher, node crypto.Role, leaves []crypto.Digest, begin, end uint64, dst []crypto.Digest,
) ([]crypto.Digest, error) {
	requireBinary(node)

	if begin >= end || end > uint64(len(leaves)) {
		return dst, ErrRange
	}

	var (
		w     rangeWalk
		spans [2 * maxPath]span
	)

	w.walk(uint64(len(leaves)), begin, end)
	for _, s := range w.spans(spans[:0]) {
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

// TaggedRangeRoot returns the root of the tagged tree of size leaves whose
// leaves from begin on are rng, as the range proof in proof proves them.
// The proof is one that [TaggedRangeProof] or [TaggedTree.RangeProof]
// returns. A verifier compares the root, or a value that it derives from
// the root, with a value that it trusts. For a range of one leaf,
// TaggedRangeRoot returns the root that [VerifyTaggedInclusion] compares.
//
// Returns [ErrRange] for an empty rng and for a range past size, and
// [ErrProof] for a proof whose length is not the length of the proof of the
// range, and for a proof that contains a hash whose size is not the size of
// the leaves. It checks both before it hashes, so a proof from an untrusted
// source never makes CombineTagged panic, and a proof of another length
// costs no hash. Panics when node is a unary role, and, through
// CombineTagged, when a leaf of rng is not a digest of h.
//
// # Allocation contract
//
// Zero alloc. It hashes len(rng) + len(proof) - 1 nodes.
func TaggedRangeRoot(
	h crypto.Hasher, node crypto.Role, begin, size uint64, rng, proof []crypto.Digest,
) (crypto.Digest, error) {
	requireBinary(node)

	// An end at or below begin is an empty rng, or a range whose end
	// overflows.
	end := begin + uint64(len(rng))
	if end <= begin || end > size {
		return crypto.Digest{}, ErrRange
	}

	var (
		w     rangeWalk
		spans [2 * maxPath]span
	)

	w.walk(size, begin, end)
	if len(proof) != len(w.spans(spans[:0])) {
		return crypto.Digest{}, ErrProof
	}

	for _, p := range proof {
		if p.Size() != rng[0].Size() {
			return crypto.Digest{}, ErrProof
		}
	}

	// next is the position in proof of the next hash. The folds below take
	// the hashes in the order of the proof.
	next := 0

	var r crypto.Digest
	if w.nl == 0 {
		r = taggedFold(h, node, rng)
	} else {
		// The left edge ends at the node of the range that starts at begin,
		// and the right edge at the node that ends at end.
		left := taggedFold(h, node, rng[:w.left[w.nl-1].hi-begin])
		for i := w.nl - 2; i >= 0; i-- {
			if s := w.left[i]; s.hi <= begin {
				left = h.CombineTagged(node, proof[next], left)
				next++
			} else {
				left = h.CombineTagged(node, left, taggedFold(h, node, rng[s.lo-begin:s.hi-begin]))
			}
		}

		right := taggedFold(h, node, rng[w.right[w.nr-1].lo-begin:])
		for i := w.nr - 2; i >= 0; i-- {
			if s := w.right[i]; s.lo >= end {
				right = h.CombineTagged(node, right, proof[next])
				next++
			} else {
				right = h.CombineTagged(node, taggedFold(h, node, rng[s.lo-begin:s.hi-begin]), right)
			}
		}

		r = h.CombineTagged(node, left, right)
	}

	for i := w.np - 1; i >= 0; i-- {
		if w.path[i].lo >= end {
			r = h.CombineTagged(node, r, proof[next])
		} else {
			r = h.CombineTagged(node, proof[next], r)
		}

		next++
	}

	return r, nil
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
		stack[n] = leaf
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
