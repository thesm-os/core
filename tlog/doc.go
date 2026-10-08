// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package tlog implements the Merkle tree of RFC 9162 over any
// [crypto.Hasher], stores it as the tiles of C2SP tlog-tiles, and builds
// trees of the same shape whose interior nodes are hashed under a role
// that a protocol assigns.
//
// With SHA-256 the bytes match RFC 6962, so the independent witnesses
// of C2SP tlog-witness verify a tree this package builds. A tree over
// any other hasher is RFC 9162 with a different hash, which C2SP
// witnesses do not verify.
//
// # Trees in memory
//
// [LeafHash] and [NodeHash] produce the RFC 9162 leaf and node hashes.
// [Root], [InclusionProof] and [ConsistencyProof] work on a tree whose
// leaf hashes are in memory, such as the entries of one batch.
// [VerifyInclusion] and [VerifyConsistency] check a proof against a
// root.
//
// # Tagged trees
//
// A protocol that builds more than one tree over one hasher keeps the
// trees apart by hashing the interior nodes of each tree with
// [crypto.Hasher.CombineTagged] under a binary role of its own. No
// interior node of such a tree has the bytes of an interior node under
// another role, or of an RFC 9162 tree. [TaggedRoot],
// [TaggedInclusionProof] and [VerifyTaggedInclusion] give these trees RFC
// 9162's shape and proofs. A [TaggedTree] keeps every node of one tree, so
// the paths of all its leaves cost one hash per interior node. A
// [TaggedFold] computes the root from leaves that arrive one at a time, such
// as the digests of the chunks of a stream, in a state of 64 digests.
//
// [TaggedRangeProof] and [TaggedTree.RangeProof] prove a range of
// consecutive leaves with at most two hashes per tree level, and the proof
// of one leaf is its path. [TaggedRangeRoot] recomputes the root from the
// leaves of a range and its proof, for a verifier that compares the root,
// or a value that it derives from the root, with a value that it trusts.
//
// A tagged tree over no leaves has no root, and [TaggedRoot] panics
// instead of returning one. A protocol whose tree can be empty chooses the
// value it commits to for that case.
//
// # Trees in tiles
//
// A tile is a subtree of height 8. A [Tile] at level L stores up to 256
// consecutive hashes of tree level 8L: level 0 stores leaf hashes, and
// each higher level stores the roots of full tiles below it. A full tile
// never changes. [Tile.Path] and [ParseTilePath] convert between a tile
// and its C2SP path, and [Tiles] lists the tiles that growing a tree
// adds.
//
// A writer integrates leaves with a [Builder], which keeps only the
// partial tile at each level. [Builder.Integrate] computes the tiles a
// batch adds into an [Update], and the writer stores them concurrently
// and calls [Builder.Commit].
//
// A reader builds a proof from stored tiles through a [TileReader], and
// [BlobTiles] reads tiles from a [go.thesmos.sh/core/blob.Store].
// [TreeRoot], [ProveInclusion] and [ProveConsistency] read every tile
// they need in one batch, at most two per tile level.
//
// [AppendBundleEntry], [BundleEntries] and [BundlePath] implement the
// C2SP entry bundles that store the entries themselves.
//
// # Write order
//
// A writer keeps three steps in order, and a crash between any two of
// them leaves a consistent log:
//
//  1. Write the entry bundles of the batch.
//  2. Write every tile of the [Update], then call [Builder.Commit].
//  3. Publish the new size and root in a signed checkpoint.
//
// A reader starts from a checkpoint and reads only tiles at or below its
// size, so tiles written before a crash and not yet covered by a
// checkpoint are invisible. Tile bytes are a function of the leaf
// hashes, so a writer that resumes with [NewBuilder] at the last
// published size and integrates the same leaves again writes the same
// bytes to the same paths.
//
// # Trust
//
// The prove functions read tiles and trust them. A proof built from a
// corrupted tile fails [VerifyInclusion] or [VerifyConsistency] at the
// verifier. A caller that caches tiles from an untrusted server
// verifies a proof against a signed root before it keeps them.
//
// # Failure semantics
//
// Every error classifies under [go.thesmos.sh/core/errs.Classify]. A
// proof that does not verify is [ErrProof], an index or size outside
// the tree is [ErrRange], and tile data of the wrong length is
// [ErrTileSize]. Errors from a [TileReader] keep their class.
//
// [VerifyInclusion] and [VerifyTaggedInclusion] return [ErrProof] for a
// proof hash whose size differs from the leaf's. The content of a proof
// from an untrusted source therefore never makes a verifier panic. The
// tagged functions panic on a unary node role and on a tree without
// leaves. Both are programmer errors, as the preconditions of
// [crypto.Hasher.CombineTagged] are.
//
// # Allocation contract
//
// Hashing, the in-memory roots and the verifiers allocate nothing on
// the warm path. [Builder.Integrate] into a reused [Update] allocates
// nothing once the Update has grown, and [TaggedTree.Reset] into a
// reused TaggedTree allocates nothing once the tree has grown. The
// prove functions borrow their tile buffers from a pool.
package tlog
