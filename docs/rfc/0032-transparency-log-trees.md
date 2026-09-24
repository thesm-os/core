---
rfc: 0032
title: Transparency Log Trees
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Draft
created: 2026-09-24
updated: 2026-09-24
discussion: none
supersedes: none
superseded-by: none
produces-adr: tbd
---

# RFC-0032: Transparency Log Trees

## Summary

We propose a package `tlog` that implements the Merkle tree of RFC 9162
over any `crypto.Hasher`:

- `LeafHash` and `NodeHash` produce the RFC 9162 leaf and node hashes.
- `Root`, `InclusionProof` and `ConsistencyProof` work on a tree in
  memory, such as the entries of one batch.
- `VerifyInclusion` and `VerifyConsistency` check a proof against a
  root.
- `Tile`, `Builder` and the `TileReader` seam store a tree as the
  immutable 256-hash tiles of C2SP tlog-tiles. A writer integrates a
  batch of leaves with at most 96 KiB of state and writes the batch's
  tiles concurrently. A reader builds a proof from one batched read of
  at most two tiles per tile level.

With SHA-256 the bytes match RFC 6962, so a tree built by this package
can be verified by the independent witnesses that C2SP tlog-witness
defines.

## Motivation

### Core has tree hashing and no tree

A caller that commits records to a verifiable log writes the tree
itself: the root over a list, the inclusion and consistency proofs,
the verifier and the layout for storing the tree. Core supplies only
the parts below them, domain-separated tree hashing (`HashTagged`,
`CombineTagged`) and content-addressed storage (`cas`). Each part a
caller writes is a place where two implementations can disagree about
a byte.

### The standards fix one shape

- RFC 9162 defines the tree, the inclusion proof and the consistency
  proof, and their verification algorithms.
- C2SP tlog-tiles stores a tree as tiles of 256 hashes, each immutable
  once full, with a fixed path scheme. A proof at any tree size reads
  a small, bounded number of tiles.
- C2SP tlog-checkpoint defines the root a log signs as "the root of the
  RFC 6962 Merkle hash tree", and tlog-witness verifies consistency
  proofs "according to RFC 6962, Section 2.1.2". RFC 6962 fixes
  SHA-256 and the prefixes `0x00` for a leaf and `0x01` for a node.

These standards leave the package one correct shape. It needs only the
standard library, and the standards supply the vectors a conformance
suite needs. A log that produces exactly these bytes can be verified
by independent witnesses.

### The existing Go package does not fit core

`golang.org/x/mod/sumdb/tlog` implements RFC 6962 trees and tiles for
the Go checksum database. Core cannot import it:

- Its module requires `golang.org/x/tools`. ADR-0015 admits only
  golang.org/x modules without requirements.
- Its `Hash` is `[32]byte`, so it cannot represent a SHA-384 tree.
- Its tile paths include the tile height (`tile/H/L/N`), and C2SP
  tlog-tiles fixes the height at 8 and omits it (`tile/L/N`).

It remains the reference this package is tested against.

## Detailed design

### Hashing

```go
// LeafHash returns the RFC 9162 hash of a leaf: HASH(0x00 || data).
//
// It is h.HashTagged with role 0x00, so it costs what HashTagged
// costs.
//
// # Allocation contract
//
// Zero alloc on the warm path, as HashTagged is.
func LeafHash(h crypto.Hasher, data []byte) crypto.Digest

// NodeHash returns the RFC 9162 hash of an interior node:
// HASH(0x01 || left || right).
//
// RFC 9162 prefixes a node with 0x01, which falls in the unary half of
// crypto.Role, so CombineTagged cannot produce it. NodeHash writes
// left and right into pooled scratch and calls h.HashTagged with role
// 0x01 over it, which gives the RFC's bytes.
//
// # Allocation contract
//
// Zero alloc on the warm path. The scratch comes from a package pool.
func NodeHash(h crypto.Hasher, left, right crypto.Digest) crypto.Digest
```

A node hash through `crypto.Hasher` with pooled scratch costs 113 to
119 ns, against 79 ns for `crypto/sha256.Sum256` over the same 65
bytes, and neither allocates. The difference is the interface call,
the pool and the copies of 65-byte `Digest` values. `Root`, the
verifiers, `Builder` and the prove functions borrow one
`crypto.Stream` per call and hash every node through it, so a node
skips the pool and the per-node `HashTagged` call. Measured with Go
1.27.1 on an AMD Ryzen 9 9950X3D, three runs of one second each.

RFC 9162's prefixes are the standard's own domain separation, and
`tlog` uses them as the standard defines them. ADR-0013 describes
core's arity bit as having RFC 6962's structure: one prefix for a leaf
and another for a node. The bytes differ, because RFC 6962's node
prefix `0x01` is in core's unary half. Within a `tlog` tree, roles
`0x00` and `0x01` belong to the standard. A caller that uses the same
hasher for its own roles keeps its trees apart from `tlog` trees, as it
keeps any two protocols apart.

### Trees in memory

```go
// Root returns the Merkle Tree Hash of the tree whose leaf hashes are
// leaves. The root of an empty tree is HASH() over no input, as RFC
// 9162 defines it.
//
// # Allocation contract
//
// Zero alloc on the warm path. The fold keeps one digest per tree
// level on the stack.
func Root(h crypto.Hasher, leaves []crypto.Digest) crypto.Digest

// InclusionProof appends to dst the audit path for the leaf at index
// in the tree whose leaf hashes are leaves, and returns the extended
// slice.
//
// Returns ErrRange when index is not below len(leaves).
func InclusionProof(h crypto.Hasher, leaves []crypto.Digest, index uint64, dst []crypto.Digest) ([]crypto.Digest, error)

// ConsistencyProof appends to dst the proof that the tree of the
// first oldSize leaves is a prefix of the tree of all of leaves.
//
// Returns ErrRange unless 0 < oldSize <= len(leaves). When oldSize
// equals len(leaves), the proof is empty.
func ConsistencyProof(h crypto.Hasher, leaves []crypto.Digest, oldSize uint64, dst []crypto.Digest) ([]crypto.Digest, error)
```

These functions serve a tree that fits in memory, such as the entries
of one write. The inclusion path of an entry within its batch is then
a standard RFC 9162 proof, verified by the same function as a proof
against a whole log.

### Verification

```go
// VerifyInclusion reports whether proof shows that leaf is the leaf
// at index in the tree of size leaves with the given root, following
// RFC 9162, section 2.1.3.2.
//
// Returns ErrRange when index is not below size, and ErrProof when
// the proof does not recompute root.
//
// # Allocation contract
//
// Zero alloc on the warm path.
func VerifyInclusion(h crypto.Hasher, index, size uint64, leaf, root crypto.Digest, proof []crypto.Digest) error

// VerifyConsistency reports whether proof shows that the tree of
// oldSize leaves with oldRoot is a prefix of the tree of newSize
// leaves with newRoot, following RFC 9162, section 2.1.4.2.
//
// Returns ErrRange unless 0 < oldSize <= newSize. When the sizes are
// equal, the proof must be empty and the roots equal.
//
// # Allocation contract
//
// Zero alloc on the warm path.
func VerifyConsistency(h crypto.Hasher, oldSize, newSize uint64, oldRoot, newRoot crypto.Digest, proof []crypto.Digest) error
```

RFC 9162 defines a consistency proof for `0 < m < n`. The package adds
the two boundary cases that `x/mod/sumdb/tlog` also handles: equal
sizes need an empty proof and equal roots, and an old size of zero is
out of range, because an empty tree has no root to be consistent with.

### Tiles

A tile is a subtree of height 8. A tile at level `L` stores up to 256
consecutive hashes of tree level `8L`: level 0 stores leaf hashes, and
each higher level stores the roots of full tiles below it.

```go
// Tile names one tile of a tree stored as C2SP tlog-tiles.
type Tile struct {
    // Index is the tile's position within its level.
    Index uint64

    // Width is the number of hashes, 1 to 256. A tile of width 256 is
    // full and immutable.
    Width uint16

    // Level is the tile level. Level L stores hashes of tree level
    // 8L. Levels 0 to 7 cover every tree of up to 2^64 leaves, and
    // ParseTilePath refuses a higher level.
    Level uint8
}

// Path returns the tile's path relative to the log's prefix, in the
// form tile/<L>/<N>[.p/<W>], where N is written in groups of three
// digits, each group but the last prefixed with x.
func (t Tile) Path() string

// ParseTilePath returns the Tile a path names, or ErrTilePath.
func ParseTilePath(path string) (Tile, error)

// Tiles returns the tiles a tree of size newSize has that a tree of
// size oldSize does not have in the same form: the full tiles
// completed between the two sizes, and the partial tiles at the right
// edge of the tree of size newSize.
func Tiles(oldSize, newSize uint64) iter.Seq[Tile]
```

A full tile is 256 times the digest size: 8,192 bytes with SHA-256, as
C2SP requires, and 12,288 bytes with SHA-384. A tree over any hasher
other than SHA-256 is RFC 9162 with a different hash. C2SP witnesses
do not verify it.

### Entry bundles

```go
// AppendBundleEntry appends entry to a C2SP entry bundle: a big-endian
// uint16 length, then the entry. Returns ErrEntrySize for an entry
// longer than 65,535 bytes.
func AppendBundleEntry(dst, entry []byte) ([]byte, error)

// BundleEntries iterates over the entries of a bundle. It yields
// ErrBundle and stops at a length that runs past the end of data.
func BundleEntries(data []byte) iter.Seq2[[]byte, error]

// BundlePath returns the path of the entry bundle at index with the
// given width: tile/entries/<N>[.p/<W>].
func BundlePath(index uint64, width uint16) string
```

### Integrating batches into a stored tree

A writer integrates leaves in batches. The tiles a batch completes or
changes are independent objects, so the writer stores them
concurrently and then publishes the new size and root. Trillian
Tessera, the tile-based log implementation of the transparency-dev
project, has the same shape: it integrates sequenced entries in
batches, writes the resulting tiles to GCS and S3 in parallel, and
publishes the checkpoint afterwards.

```go
// Builder integrates batches of leaf hashes into a tree stored as
// tiles.
//
// A Builder keeps only the right edge of the tree: the partial tile
// at each of the eight levels. Its state is at most 8 x 255 digests,
// 64 KiB with SHA-256 and 96 KiB with SHA-384, whatever the size of
// the tree.
//
// # Concurrency
//
// Not safe for concurrent use. One writer integrates.
//
// # Allocation contract
//
// Integrate into a reused Update is zero-alloc once the Update's
// buffers have grown to the batch size. Node hashes fold in scratch
// the Builder allocates once, and an Update keeps its tile data in a
// core arena.Arena that each Integrate resets.
type Builder struct{ /* unexported fields */ }

// NewBuilder returns a Builder for the tree of the given size, stored
// in r. It reads the partial tiles at the tree's right edge in one
// ReadTiles call, so a writer resumes after a restart from the last
// published size. A size of zero reads nothing.
func NewBuilder(ctx context.Context, h crypto.Hasher, size uint64, r TileReader) (*Builder, error)

// Integrate computes the tree that appending leaves gives, and writes
// into u every tile that tree has and the Builder's tree does not have
// in the same form: the full tiles the batch completes, and the
// partial tiles at the new right edge. The Builder does not change
// until Commit. Returns ErrLeafSize for a leaf whose size is not the
// digest size of the Builder's hasher, and ErrRange when the tree
// would pass 2^64 - 1 leaves.
func (b *Builder) Integrate(leaves []crypto.Digest, u *Update) error

// Commit makes u's tree the Builder's tree. A writer calls it after
// every tile of u is durable. Returns ErrStale when u was not computed
// from the Builder's current tree.
func (b *Builder) Commit(u *Update) error

// Size returns the number of leaves in the Builder's tree.
func (b *Builder) Size() uint64

// Root returns the Merkle Tree Hash of the Builder's tree.
func (b *Builder) Root() crypto.Digest

// Update is the result of one Integrate call. Its zero value is ready
// to use, and reusing one across batches reuses its buffers.
type Update struct{ /* unexported fields */ }

// Size and Root describe the tree the update produces.
func (u *Update) Size() uint64
func (u *Update) Root() crypto.Digest

// Tiles iterates over the tiles to write. The data is valid until the
// next Integrate into u.
func (u *Update) Tiles() iter.Seq2[Tile, []byte]
```

An update contains one digest per leaf of the batch in its level-0
tiles, one per 256 leaves at level 1, and so on, plus at most eight
partial tiles. A batch of 4,096 leaves under SHA-256 that starts on a
tile boundary writes 16 full level-0 tiles, 128 KiB. It also writes at
most one partial tile per level, each at most 8 KiB.

`Integrate` costs 100 to 105 ns per leaf for a batch of 4,096 leaves
and allocates nothing, so one core integrates about ten million leaves
a second. Hashing is 54% of the profile, as two SHA-256 compressions
per node. A proof verifies in under 2 µs.

### Write order and crash safety

A writer keeps three steps in order. A crash between any two of them
leaves a consistent log.

1. Write the entry bundles of the batch.
2. Write every tile of the update, concurrently, then call `Commit`.
3. Publish the new size and root in a signed checkpoint.

A reader starts from a checkpoint and reads only tiles at or below its
size, so tiles written before a crash and not yet covered by a
checkpoint are invisible. Tile bytes are a function of the leaf
hashes, so a writer that resumes with `NewBuilder` at the last
published size and integrates the same leaves again writes the same
bytes to the same paths. A full tile never changes once written, which
is the immutability C2SP tlog-tiles requires.

### Proofs from stored tiles

```go
// TileReader reads tile data from storage. It describes IO, and ctx
// governs cancellation and the deadline of the whole batch.
type TileReader interface {
    // ReadTiles appends the data of tiles[i] to dst[i] for every i:
    // tiles[i].Width hashes of the tree's digest size. dst has one
    // entry per tile. An implementation over remote storage reads the
    // tiles concurrently. A missing tile returns an error classified
    // errs.NotFound. The contents of dst are unspecified after an
    // error.
    ReadTiles(ctx context.Context, tiles []Tile, dst [][]byte) error
}

// BlobTiles returns a TileReader over the objects under prefix in s,
// at the paths Tile.Path gives. It reads up to limit tiles
// concurrently through task.Each, and returns ErrRange when dst has
// fewer entries than tiles.
func BlobTiles(s blob.Store, prefix string, limit int) TileReader

// TreeRoot returns the root of the tree of size leaves stored in r.
func TreeRoot(ctx context.Context, h crypto.Hasher, r TileReader, size uint64) (crypto.Digest, error)

// ProveInclusion appends to dst the audit path for the leaf at index
// in the tree of size leaves stored in r. It computes the set of tiles
// the path needs first and reads them in one ReadTiles call.
func ProveInclusion(ctx context.Context, h crypto.Hasher, r TileReader, size, index uint64, dst []crypto.Digest) ([]crypto.Digest, error)

// ProveConsistency appends to dst the proof that the tree of oldSize
// leaves stored in r is a prefix of the tree of newSize leaves. It
// reads its tiles in one ReadTiles call.
func ProveConsistency(ctx context.Context, h crypto.Hasher, r TileReader, oldSize, newSize uint64, dst []crypto.Digest) ([]crypto.Digest, error)
```

A hash at a tree level that is a multiple of 8 is read from a tile. A
hash between those levels is the root of part of a tile, computed from
that tile's data. A proof reads at most two tiles per tile level: the
tile on the leaf's path and the partial tile at the right edge. A tree
of 10^9 leaves has four tile levels, since 256^4 is 4.3 x 10^9, so a
proof reads at most eight tiles.

The prove functions borrow their tile buffers from a pool of core
`arena.Arena` values, sized with one `Alloc` per proof, so a proof
allocates nothing beyond what the reader allocates and what `dst`
needs to grow. Rebuilding the hashes inside the tiles is the cost of a
proof: about 500 node hashes, 60 to 65 µs for a tree of 70,000 leaves
read from memory. Against a storage round trip of 100 ms that is noise, and
a server that proves at a higher rate caches the interior hashes of
full tiles, which never change.

On object storage the batch read is what makes a proof fast. AWS
states first-byte latencies of about 100 to 200 ms for small objects
in S3 Standard. Eight sequential reads would cost up to 1.6 s, and one
concurrent batch costs one round trip. Full tiles are immutable, and
C2SP tlog-tiles states their "caching headers SHOULD be long-lived", so
a cache or a CDN in front of the store serves most reads.

The prove functions read tiles and trust them. A proof built from a
corrupted tile fails `VerifyInclusion` or `VerifyConsistency` at the
verifier. A caller that caches tiles fetched from an untrusted server
verifies a proof against a signed root before it keeps them, as Russ
Cox describes for authenticating tiles.

### Errors

```go
var (
    // ErrProof reports that a proof does not recompute the root. It
    // classifies as errs.Integrity.
    ErrProof = errs.WithClass(errors.New("tlog: proof does not verify"), errs.Integrity)

    // ErrRange reports an index or size outside the tree. It
    // classifies as errs.Invalid.
    ErrRange = errs.WithClass(errors.New("tlog: index or size out of range"), errs.Invalid)

    // ErrTilePath reports a path that names no tile. It classifies as
    // errs.Invalid.
    ErrTilePath = errs.WithClass(errors.New("tlog: malformed tile path"), errs.Invalid)

    // ErrTileSize reports tile data whose length is not the tile's
    // width times the digest size. It classifies as errs.Integrity.
    ErrTileSize = errs.WithClass(errors.New("tlog: tile data has the wrong length"), errs.Integrity)

    // ErrLeafSize reports a leaf hash whose size differs from the
    // digest size of the Builder's hasher. It classifies as
    // errs.Invalid.
    ErrLeafSize = errs.WithClass(errors.New("tlog: leaf hash has the wrong size"), errs.Invalid)

    // ErrEntrySize reports an entry longer than 65,535 bytes. It
    // classifies as errs.Invalid.
    ErrEntrySize = errs.WithClass(errors.New("tlog: bundle entry longer than 65535 bytes"), errs.Invalid)

    // ErrBundle reports an entry bundle whose lengths run past its
    // data. It classifies as errs.Integrity.
    ErrBundle = errs.WithClass(errors.New("tlog: malformed entry bundle"), errs.Integrity)

    // ErrStale reports an Update that was not computed from the
    // Builder's current tree. It classifies as errs.Conflict.
    ErrStale = errs.WithClass(errors.New("tlog: update computed from another tree"), errs.Conflict)
)
```

### Example

A writer integrates a batch whose entry bundles it has already
written, stores the update's tiles concurrently, commits, and
publishes the checkpoint:

```go
leaves = leaves[:0]
for _, entry := range batch {
    leaves = append(leaves, tlog.LeafHash(h, entry))
}
if err := b.Integrate(leaves, &u); err != nil {
    return err
}
err := task.Run(ctx, 16, func(ctx context.Context, g *task.Group) error {
    for t, data := range u.Tiles() {
        err := g.Go(func(ctx context.Context) error {
            _, err := blob.PutBytes(ctx, s, prefix+t.Path(), data, blob.PutOptions{})
            return err
        })
        if err != nil {
            return err
        }
    }
    return nil
})
if err != nil {
    return err
}
if err := b.Commit(&u); err != nil {
    return err
}
return publish(ctx, b.Size(), b.Root())
```

A reader proves and checks one entry against a published root:

```go
r := tlog.BlobTiles(s, prefix, 8)
proof, err := tlog.ProveInclusion(ctx, h, r, size, index, nil)
if err != nil {
    return err
}
return tlog.VerifyInclusion(h, index, size, tlog.LeafHash(h, entry), root, proof)
```

### Conformance and tests

- Vectors recorded from `x/mod/sumdb/tlog` v0.40.0 for SHA-256: the
  root of every tree up to 64 leaves, a digest of every inclusion and
  consistency proof up to 64 leaves, and a digest of every tile of
  twelve trees from 1 to 70,000 leaves. The recorded values are in
  `tlog/testdata/vectors.txt`, so the tests do not import x/mod.
- A differential test integrates 70,000 leaves in random batches with
  `Builder`, and requires the root at every commit to equal the root in
  memory and every tile to equal the tile of one batch. The prove
  functions must return the in-memory proofs.
- A writer that integrates a batch, stops before `Commit`, and resumes
  with `NewBuilder` at the last published size writes the same tile
  bytes to the same paths.
- `ProveInclusion` and `ProveConsistency` call `ReadTiles` once, with
  at most two tiles per tile level.
- Tile paths round-trip through `Path` and `ParseTilePath`, including
  the three-digit groups and the partial suffix.
- `VerifyInclusion` and `VerifyConsistency` reject every proof with one
  hash changed, one hash removed or one hash added.
- Allocation tests cover `LeafHash`, `NodeHash`, `Root`, the two
  verifiers, `Builder.Integrate` into a reused `Update`, and the prove
  functions over a reader that allocates nothing.

## Alternatives considered

### A. Import `golang.org/x/mod/sumdb/tlog`

The Go checksum database uses it in production, and it implements RFC
6962 trees, proofs and tiles.

**Why not:** its module requires `golang.org/x/tools`, which ADR-0015
does not admit. Its `Hash` is fixed at 32 bytes, and its tile paths
include the height, which C2SP tlog-tiles omits.

### B. Tagged roles for the node hash

The tree would hash nodes with `CombineTagged` and a binary role, as
core's own chains do.

**Why not:** the bytes would not be RFC 9162's. No C2SP witness could
verify a consistency proof over such a tree. Every verifier would also
need core's role registry.

### C. A Merkle Mountain Range

An MMR keeps a list of perfect subtrees and needs no rebalancing. Its
incremental state is its peaks.

**Why not:** an MMR is not RFC 9162. It has no witness ecosystem and no
standard proof format. Tiles give an RFC 9162 tree the same constant
writer memory, and a proof reads a bounded number of tiles.

### D. A tree interface

`tlog` would define a `Tree` interface. Each implementation would
choose its own shape.

**Why not:** the value of the package is that its bytes are the
standard's. An interface admits trees whose roots the verifier
rejects. The seam is `TileReader`, where storage genuinely varies.

## Drawbacks

- `NodeHash` passes a node through `HashTagged` with role `0x01`, a
  role in the unary half, over two concatenated digests. This is the
  one place in core where a binary operation uses a unary role, and it
  exists because RFC 6962 predates core's arity bit.
- The package adds about 25 exported identifiers: eight errors, the
  `Tile`, `Builder` and `Update` types, the `TileReader` seam and its
  blob adapter, and the hashing, proof and bundle functions.
- A tree over a hasher other than SHA-256 is RFC 9162 but not C2SP, so
  public witnesses do not verify it.
- A proof costs one round trip to storage, 100 to 200 ms on S3
  Standard without a cache. A reader that serves many proofs puts a
  cache in front of the store.
- An update contains a digest per leaf of its batch, so the batch size
  bounds a writer's memory. A writer sizes its batches to its memory,
  about 32 bytes per leaf under SHA-256.
- A crash between writing tiles and publishing a checkpoint leaves
  tiles beyond the published size. Readers never read them, and the
  next integration overwrites them with the same bytes. Storage keeps
  them until then.
- C2SP limits a bundle entry to 65,535 bytes. A caller with larger
  entries stores them elsewhere and logs their digest.

## Open questions

None.

## Unresolved / future work

- C2SP signed notes, checkpoints and cosignatures, and the tlog-policy
  format for witness quorums. Each is a text format over this tree and
  fits a separate RFC.
- A witness client. It speaks HTTP, so it belongs to a consumer
  module under ADR-0008.
- A cache of verified tiles for readers.

## References

- RFC 6962, "Certificate Transparency", section 2.1,
  <https://www.rfc-editor.org/rfc/rfc6962.html>.
- RFC 9162, "Certificate Transparency Version 2.0", sections 2.1.1 to
  2.1.4, <https://www.rfc-editor.org/rfc/rfc9162.html>, and erratum
  8670, <https://www.rfc-editor.org/errata/eid8670>.
- C2SP tlog-tiles, <https://c2sp.org/tlog-tiles>.
- C2SP tlog-checkpoint, <https://c2sp.org/tlog-checkpoint>.
- C2SP tlog-witness, <https://c2sp.org/tlog-witness>.
- Russ Cox, "Transparent Logs for Skeptical Clients",
  <https://research.swtch.com/tlog>.
- `golang.org/x/mod/sumdb/tlog` at v0.40.0: `tlog.go`, `tile.go` and
  `go.mod`.
- transparency-dev, Trillian Tessera, <https://github.com/transparency-dev/tessera>:
  batched integration and parallel tile writes in its storage
  drivers.
- AWS, "Performance guidelines for Amazon S3",
  <https://docs.aws.amazon.com/AmazonS3/latest/userguide/optimizing-performance-guidelines.html>.
- RFC-0029, domain-separated tree hashing.
- ADR-0013, tagged tree hashing on the hasher interface.
- ADR-0015, the dependency rule.
