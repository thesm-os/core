---
rfc: 0056
title: Proofs from Cached Node Tiles
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-10-11
updated: 2026-10-11
discussion: none
supersedes: none
superseded-by: none
produces-adr: tbd
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# RFC-0056: Proofs from Cached Node Tiles

## Summary

We propose `tlog.Prover`. It builds the proofs and roots of
`ProveInclusion`, `ProveConsistency` and `TreeRoot` from a cache of node
tiles. A node tile is a tile together with the hash of every perfect
subtree inside it. The node tile of a full tile has 511 hashes. The
Prover reads and folds each tile once, and keeps its node tile in a
`cache.Cache` that bounds the bytes of the node tiles. A proof that finds
its node tiles in the cache does not read storage. It hashes only the
nodes above the perfect subtrees of its spans.

- An inclusion proof in a tree of 2^20 leaves takes 0.40 µs on one core
  when the cache contains every node tile. `ProveInclusion` takes
  51.5 µs. A root takes 0.97 µs, and `TreeRoot` takes 27.5 µs.
- A proof that finds its node tiles in the cache does not take a lock or
  write memory that other proofs read. On four cores, the Prover builds
  10.5 million inclusion proofs per second.
- The cache keeps the widest node tile that a proof read for each tile
  position. That node tile serves every narrower tile of its position.
- Concurrent proofs that miss one position share one read of its tile.

The tiles in storage keep the format of C2SP tlog-tiles. The Prover does
not write to storage, and readers of the tiles see the same objects.

## Motivation

### A proof from tiles hashes about 500 nodes

C2SP tlog-tiles stores every eighth level of the tree. The levels between
them "are reconstructed by hashing the leaves" (`tlog-tiles.md:112-114`).
`ProveInclusion` reconstructs them for each proof. In a tree of 2^20
leaves, the audit path of a leaf has twenty hashes, and `ProveInclusion`
computes them from three tiles:

| Tile | Hashes of the path | Node hashes |
|---|---|---|
| The full tile of the leaf at level 0 | 8, the roots of 1, 2, 4 and up to 128 hashes | 247 |
| The full tile at level 1 | 8 | 247 |
| The partial tile of width 16 at level 2 | 4 | 11 |

A node hash of a fold takes 98 ns with SHA-256 on one core. The 505 node
hashes take 49.7 µs of the 51.5 µs of `ProveInclusion`. One core builds
about 19,400 such proofs per second.

### Logs build proofs on request

RFC 9162 defines an endpoint that returns a consistency proof between two
tree heads (section 5.3), one that returns an inclusion proof for a leaf
hash (section 5.4), and one that returns both (section 5.5). A log on core
that serves these endpoints builds a proof from tiles for each request. A
log also sends each witness a consistency proof with each new checkpoint,
and `witness.Client.AddCheckpoint` takes that proof. At 19,400 inclusion
proofs per second, a log that serves 100,000 proofs per second needs more
than five cores for the node hashes alone.

RFC-0032 states the remedy for this cost: "a server that proves at a
higher rate caches the interior hashes of full tiles, which never change".

### Tiles never change

tlog-tiles states for the tile endpoint that "This endpoint is immutable,
so its caching headers SHOULD be long-lived" (`tlog-tiles.md:96`). A
partial tile has its width in its path, so a partial tile of one width
does not change either. A node tile is a function of the bytes of its
tile, so a cache of node tiles does not need invalidation. The tree only
appends. A wider tile of one position contains every narrower tile of that
position as a prefix.

### Prior art

- `golang.org/x/mod/sumdb/tlog` stores every node of the tree, about two
  hashes per record (`tlog.go:114-173`). Its `ProveRecord` reads the
  twenty hashes of a path in 0.94 µs with 13 allocations. It doubles the
  storage of the tree to save the node hashes.
- Trillian rebuilds the interior nodes of every full tile that a
  transaction loads (`log_tile.go:33-95`). It keeps them in a cache that
  is not safe for concurrent use (`subtree_cache.go:39-45`), and each
  transaction creates its own cache (`log_storage.go:185-190`).
- The client of Trillian Tessera builds proofs from a cache of 65,536 node
  hashes per tree size. A miss locks a mutex of the tile, fetches the tile
  and adds its 2w − 1 nodes to the cache (`client/client.go:436-499`).
- The sumdb client of x/mod keeps every tile that it read. Concurrent
  reads of one tile share one fetch (`client.go:94` and `:549-604`).

## Detailed design

### Components

| Component | Responsibility |
|---|---|
| `Prover`, `ProverConfig`, `NewProver` | The hasher and the `TileReader` of one tree, and the cache of its node tiles |
| `Prover.InclusionProof` | The audit path of a leaf, equal to the proof of `ProveInclusion` |
| `Prover.ConsistencyProof` | The proof that one tree is a prefix of another, equal to the proof of `ProveConsistency` |
| `Prover.Root` | The root of a tree, equal to the root of `TreeRoot` |
| `ErrConfig` | The error of `NewProver` for an invalid `ProverConfig` |
| Node tiles | A tile and the hash of every perfect subtree inside it, in one buffer |
| The cache | A `cache.Cache` from tile position to node tile, whose cost is the bytes of the node tile |
| Flights | The shared read of one tile position by the proofs that miss it concurrently |

The Prover adds `cache` and `clock` to the imports of `tlog`.

### API

```go
// ProverConfig configures a Prover. Hasher, Tiles, Clock and Capacity are
// required, so the zero ProverConfig is invalid.
type ProverConfig struct {
    // Hasher is the hash function of the tree.
    Hasher crypto.Hasher

    // Tiles reads the tiles of the tree.
    Tiles TileReader

    // Clock is the clock that cache.New requires for the cache of node
    // tiles. The cache reads it once for each node tile that it adds,
    // because node tiles do not expire.
    Clock clock.Clock

    // Capacity bounds the sum of the bytes of the node tiles in the
    // cache. A node tile has 511 hashes: 16,352 bytes with SHA-256.
    Capacity int64
}

// Prover builds the proofs and roots of one tree stored as tiles, from a
// cache of node tiles. A node tile is a tile and the hash of every perfect
// subtree inside it. When a proof finds its node tiles in the cache, it
// does not read storage, and it hashes only the nodes above the perfect
// subtrees of its spans.
//
// The cache keeps the widest node tile that a proof read for each tile
// position. Such a node tile serves every narrower tile of its position,
// because the tree only appends. A proof reads the tiles that the cache
// does not serve in one ReadTiles call, and the call reads at most two
// tiles per tile level. Concurrent proofs that miss one position share one
// read.
//
// The Prover does not verify the tiles that its TileReader returns, and
// ProveInclusion does not verify them either. The node tile of a wrong
// tile remains in the cache until the cache evicts it, and the proofs
// built from it do not verify. Every size that a caller passes is a size
// that the log published.
//
// # Concurrency
//
// Safe for concurrent use. A proof that finds its node tiles in the cache
// does not take a lock or write memory that other proofs read.
//
// # Allocation contract
//
// A proof that finds its node tiles in the cache allocates only to grow
// dst. For each tile that it reads, a proof allocates the node tile, its
// buffer and the entry of the cache. The node tile is also the flight of
// its read. The first proof that waits for a flight allocates its
// channel. A read also allocates what ReadTiles allocates.
type Prover struct{ /* unexported fields */ }

// NewProver returns a Prover with an empty cache. It returns ErrConfig for
// a nil Hasher, Tiles or Clock, and for a Capacity below one node tile of
// the digest size of the Hasher.
func NewProver(cfg ProverConfig) (*Prover, error)

// InclusionProof appends to dst the audit path for the leaf at index in the
// tree of size leaves, and returns the extended slice. The proof is the
// proof that ProveInclusion returns.
//
// Returns dst unchanged and ErrRange when index is not below size,
// ErrTileSize when a tile read has the wrong length, and the error of
// ReadTiles when it fails. Returns the cause of ctx when ctx ends while the
// proof waits for the flight of another proof.
func (p *Prover) InclusionProof(ctx context.Context, size, index uint64, dst []crypto.Digest) ([]crypto.Digest, error)

// ConsistencyProof appends to dst the proof that the tree of oldSize leaves
// is a prefix of the tree of newSize leaves, and returns the extended
// slice. The proof is the proof that ProveConsistency returns.
//
// Returns dst unchanged and ErrRange unless 0 < oldSize <= newSize. It
// returns the other errors of InclusionProof.
func (p *Prover) ConsistencyProof(ctx context.Context, oldSize, newSize uint64, dst []crypto.Digest) ([]crypto.Digest, error)

// Root returns the root of the tree of size leaves, as TreeRoot does. For a
// size of zero, it returns HASH() and does not read. It returns the errors
// of InclusionProof other than ErrRange.
func (p *Prover) Root(ctx context.Context, size uint64) (crypto.Digest, error)

// ErrConfig reports a ProverConfig without a Hasher, Tiles or Clock, or
// with a Capacity below one node tile. It classifies as errs.Invalid.
var ErrConfig = errs.WithClass(errors.New("tlog: invalid prover configuration"), errs.Invalid)
```

`ProveInclusion`, `ProveConsistency` and `TreeRoot` keep their contracts
and their cost. They serve a caller that proves rarely and keeps no
state. The Prover computes its spans with the functions that they use.

### Node tiles

For each fold level k from 0 to 8, the node tile of a tile of width w has
w >> k hashes. Hash j of level k is the root of the hashes 2^k·j to
2^k·(j + 1) − 1 of the tile. Level 0 is the tile itself. Level 8 of a full
tile is the root of the tile, and the tile at the level above contains the
same hash. A full node tile has 2 × 256 − 1 = 511 hashes.

The buffer of every node tile has room for a full node tile. Level k
starts at hash 512 − 512 >> k for every width, and a partial tile uses the
layout of a full one. A lookup computes the position of a hash without a
loop. The root of the perfect subtree at tree level 8L + k and index i is
in the tile at level L and index i >> (8 − k). It is hash i mod 2^(8−k)
of fold level k.

A proof reads a tile into the level 0 part of the buffer, and then folds
it. For each k from 1 to 8, the fold hashes the hashes of level k − 1 in
pairs. Each node hash is one call to the hasher's `Hash` over the node
prefix and one pair. A full tile costs 255 node hashes, which take
25.1 µs with SHA-256 on one core.

### The widest node tile of a position

The position of a tile is its level and its index. The cache keys each
node tile by the position of its tile. A proof needs each tile of its spans at the width that its tree size gives
the tile. The width is 256 below the right edge of the tree, and
floor(size / 256^L) mod 256 at the edge. A node tile of width w serves
every width up to w. The subtrees inside the first v hashes of a tile
depend only on those hashes. The tree only appends, so a wider tile of a
position contains the node tile of every narrower one.

A node tile that a proof reads replaces a narrower node tile of its
position. As the log grows, the node tiles at the right edge of each new
tree head replace the node tiles of the previous edge. A proof at an older
size then finds the wider node tiles of its positions. tlog-tiles lets a
log delete a partial tile once the full tile exists
(`tlog-tiles.md:131-137`). A proof at an older size that finds a wider
node tile of its position does not need the partial tile, and the log can
delete it.

A proof adds a node tile to the cache only when the cache does not
contain a node tile of that position at least as wide. Concurrent proofs
that read two widths of one position can still store the narrower node
tile last. The next proof at the larger size then reads the tile again.

### A proof

A proof runs in six steps:

1. **Plan.** The proof computes its spans and splits each span into
   perfect subtrees. It lists the position of the root of each subtree and
   the width that its tree size gives the position. RFC-0032 bounds the
   reads of a proof at two tiles per tile level, so the list has at most
   two positions per tile level.
2. **Look up.** For each position, `cache.Get` returns the node tile. The
   proof uses a node tile whose width is at least the width that the
   position needs.
3. **Coalesce.** For the remaining positions, the proof locks the mutex of
   the Prover. It joins the running flight of a position when that flight
   reads a width at least as large as its own. It starts a flight for each
   other position, and a flight of a wider tile replaces the narrower
   flight in the map of the Prover. The proof then unlocks the mutex.
4. **Read and fold.** With one `ReadTiles` call, the proof reads the
   tiles of its own flights into the buffers of their node tiles. It checks
   the length of each tile, folds each node tile, adds it to the cache under
   the rule of the preceding section, and completes its flight. A flight
   that completes passes its node tile to the proofs that joined it.
5. **Wait.** The proof waits for each flight that it joined, or for the
   end of its context. A joined flight can end because the context of its
   leader ended. The proof then reads the tiles of such flights in one
   more `ReadTiles` call, and does not join another flight.
6. **Hash.** The proof combines the roots of the subtrees of each span
   from the right with node hashes, and appends the hash of the span to
   dst.

A proof that finds its node tiles in the cache runs steps 1, 2 and 6.
Step 6 is then its only hashing: popcount(size) − 1 node hashes for a
root, and none for an inclusion proof in a tree whose size is a power of
two.

### Flights

A flight is the read of one tile position that one proof starts and other
proofs join. The proof that starts a flight is its leader. The Prover keeps
its running flights in a map under one mutex. A proof that missed locks
the mutex in steps 3 and 4, and a proof without a miss does not lock it.
Each flight records its position, its width,
its node tile and its error. The first proof that joins a flight creates
the channel that the joining proofs wait on. A leader reads its tiles
before it waits for the flights that it joined, so no two proofs wait for
each other.

When the context of the leader ends during its read, the leader returns
the error of `ReadTiles`. Each proof that joined the flight then reads the
tile itself. The end of one caller's context must not fail the calls of
other callers.

When the reader fails, or a tile has the wrong length, the leader does not
add the node tile to the cache. The leader and every proof that joined the
flight return the error. A second read of the position from the
same store fails the same way, so a retry by each joined proof would
repeat the failure once per proof.

A proof whose own context ends while it waits returns the cause of its
context. The flight continues for the other proofs.

Each new tree head creates partial tiles that every proof at the new size
needs, so concurrent misses occur at the right edge after each tree head.
With a store of 100 ms and 10,000 proofs per second at the new size, about
1,000 proofs miss each new position before its first read returns. Their
flight turns 1,000 reads and folds into one.

### Trust

The Prover does not verify the tiles that its `TileReader` returns.
`ProveInclusion` does not verify them either, and a proof built from a
wrong tile does not verify at the verifier (RFC-0032). Until the cache
evicts the node tile, a wrong tile affects every proof of its position
instead of one proof. A Prover reads the storage of its own log. A reader
that does not trust the tiles checks leaves against a signed root with
`TileVerifier`.

As for `ProveInclusion`, every size that a caller passes is a size that
the log published. A writer that crashed before it published a tree head
can have written tiles beyond the published size. A writer that
integrates the same leaves again writes the same bytes, as RFC-0032
states. When a caller proves at such a size and the writer then
integrates other leaves, the cache keeps node tiles that the published
tree does not contain.

### Sizing the cache

A full node tile has 511 hashes and takes twice the bytes of its tile. It
takes 16,352 bytes with SHA-256, 24,528 with SHA-384 and 32,704 with SHA-512.
With SHA-256, every node tile of a tree takes about 64 bytes per leaf. The
node tiles above level 0 take about 0.25 bytes per leaf:

| Leaves | Every node tile | Node tiles above level 0 |
|---|---|---|
| 2^20 | 67.3 MB | 0.28 MB |
| 2^24 | 1.08 GB | 4.2 MB |
| 2^30 | 68.9 GB | 269 MB |
| 2^32 | 275 GB | 1.08 GB |

Each proof reads one tile at each level of the path of its leaf, so many
proofs read each node tile above level 0. S3-FIFO admits a new entry to a
small queue, and moves the entry to the main queue when a lookup finds it
again (RFC-0049). Under proofs of uniformly random leaves, the node tiles
above level 0 move to the main queue. Most node tiles of level 0 leave
from the small queue. A proof of a leaf whose tile at level 0 a recent
proof read finds every node tile in the cache.

### Bounds

| Quantity | Bound |
|---|---|
| Bytes of the node tiles in the cache | `Capacity` |
| Node tiles in memory beyond `Capacity` | The node tiles that the cache evicted while proofs in progress read them, at most 16 per proof |
| Memory per node tile beyond its hashes | The node tile and the entry of the cache, about 130 bytes in the prototype, under 1% of a full node tile with SHA-256 |
| Tiles that one proof reads | 2 per tile level, 16 in all |
| `ReadTiles` calls of one proof | 1, and 1 more for the tiles of flights whose leader's context ended |
| Node hashes to fold a tile | 255 for a full tile, and fewer than its width for a partial tile |
| Node hashes of a proof that finds its node tiles in the cache | Those of step 6 |
| Flights in the map of the Prover | 1 per tile position |

### Failure handling

| Condition | Method | Error | Class |
|---|---|---|---|
| A nil Hasher, Tiles or Clock, or a Capacity below one node tile | `NewProver` | `ErrConfig` | `Invalid` |
| An index not below the size, or sizes outside 0 < oldSize <= newSize | `InclusionProof`, `ConsistencyProof` | `ErrRange` | `Invalid` |
| A tile of the wrong length | Every method, for the leader and each proof that joined its flight | `ErrTileSize` | `Integrity` |
| A failed `ReadTiles`, such as for a partial tile that the log deleted and that no wider node tile serves | Every method, for the leader and each proof that joined its flight | The error of the reader | Its class |
| The end of the leader's context during its read | Every method | The leader returns the error of the reader, and each proof that joined reads the tile itself | The class of the context's error |
| The end of a proof's context while it waits for a flight | Every method | The cause of the context | Its class |
| A wrong tile of the right length from the reader | Every method | None. The proofs of its position do not verify until the cache evicts the node tile | None |
| A proof at a size that the log has not published, whose tiles the writer then writes with other leaves | Every method | None. Proofs at the published size do not verify until the cache evicts the node tiles | None |

A failed read does not add a node tile to the cache.

### Allocation contract

| Call | Objects | Sites |
|---|---|---|
| A proof or a root that finds its node tiles in the cache | 0 | The state of a proof comes from a pool, and a proof allocates only to grow dst |
| Each tile that a proof reads | 3 | The node tile, which is also the flight of the read, its buffer, and the entry that `cache.Set` allocates |
| Each flight that another proof joins | 1 | The channel that the joining proofs wait on |
| A read | As `ReadTiles` | `BlobTiles` allocates the key of each tile and the objects of `task.Each` |
| `NewProver` | 2, and those of `cache.New` | The Prover, the map of the flights, and the Cache, its index and its ghost |

The collector reclaims the buffer of a node tile that the cache evicted.
A pool of buffers would need each proof to pin its node tiles, and a pin
writes to the entry of the node tile on every proof (alternative H).

### Throughput and latency

We measured a prototype of this design on an AMD Ryzen 9 9950X3D with Go
1.27.2. The tree has 2^20 leaves under SHA-256, and a reader serves its
tiles from memory. Each value is the median of six runs of 200 ms on one
core. The prototype has no flights. A proof enters a flight only after a
miss.

| Operation | Function without a cache | Prover, with every node tile in the cache |
|---|---|---|
| Inclusion proof of a random leaf | `ProveInclusion`: 51.5 µs | 0.40 µs |
| Consistency proof from a random size | `ProveConsistency`: 51.5 µs | 0.40 µs |
| Root of a random size | `TreeRoot`: 27.5 µs | 0.97 µs |

A fold of a full tile takes 25.1 µs. When the cache contains a share of
the node tiles of level 0, proofs of uniformly random leaves miss at
level 0:

| Node tiles of level 0 in the cache | Misses per proof | Inclusion proof |
|---|---|---|
| All | 0 | 0.40 µs |
| Half | 0.47 | 13.4 µs |
| A tenth | 0.87 | 24.6 µs |
| None, with `ProveInclusion` | — | 51.5 µs |

With four goroutines on four cores and a warm cache, the prototype built
an inclusion proof every 95.6 ns. That rate is 10.5 million proofs per
second and 3.9 times the rate of one goroutine. The same harness measured
0.94 µs on one core for the `ProveRecord` of x/mod over its stored
hashes.

### Tests

| # | Guarantee |
|---|---|
| 1 | `InclusionProof`, `ConsistencyProof` and `Root` return the proofs and roots of `ProveInclusion`, `ProveConsistency` and `TreeRoot` for every size up to 600 leaves, and for random sizes and leaves of a tree of 2^20 leaves. The caches have room for one node tile, for 64 and for every node tile |
| 2 | A node tile of one width serves a proof at a smaller size without a call to `ReadTiles`. A proof at a larger size reads the tile and replaces the node tile |
| 3 | A proof that finds its node tiles in the cache does not call `ReadTiles`. A proof over an empty cache calls it once, with at most two tiles per tile level |
| 4 | Proofs that miss one position while a reader blocks its read call `ReadTiles` once for that position, and each returns its proof. A channel blocks the reader and forces the interleaving |
| 5 | When the context of the leader ends during its read, the leader returns the error of the reader. Each proof that joined reads the tile itself and returns its proof |
| 6 | The leader and every proof that joined its flight return the error of the reader, and `ErrTileSize` for a tile of the wrong length. The cache remains empty, and the next proof reads the tile again |
| 7 | A proof whose context ends while it waits returns the cause of its context, and the other proofs of the flight return their proofs |
| 8 | `NewProver` returns `ErrConfig` for each invalid field, and `errs.Classify` reports `Invalid` |
| 9 | Concurrent proofs of random leaves at random sizes over a cache of 16 node tiles return the proofs of `ProveInclusion`, under the race detector |
| 10 | `TestProverAllocs` and the benchmarks state the counts of the allocation contract: none for a proof over a warm cache, and three for each tile that a proof reads |
| 11 | The suites cover every statement, and dokimi-mutate-go kills every mutant of the Prover |

## Alternatives considered

### A. Store the interior hashes beside the tiles

A writer would store the node tile of each full tile as an object of its
own. x/mod stores every node in the same way. A proof would read the node
tiles of its path and fold nothing.

**Why not:** storage and writes double, and the store contains objects
that no C2SP reader reads. Every proof still reads storage. The cache
gives a proof in 0.40 µs without a change of format. A deployment that
proves uniformly random leaves of a tree far larger than memory at a high
rate would reverse this verdict.

### B. A cache of tiles

A cache in front of the `TileReader` would keep the tiles that proofs
read.

**Why not:** a proof over tiles in memory still takes 51.5 µs for its node
hashes. A cache of tiles removes the reads of remote storage. A cache of
node tiles removes the reads and the node hashes.

### C. A cache of single nodes

Tessera's client keeps 65,536 node hashes in an LRU that it keys by node.
A miss adds every node of the tile to the LRU (`client/client.go:436-499`).

**Why not:** each hash of 32 bytes needs a map entry and list links of its
own, and they take more memory than the hash. A miss inserts 2w − 1 entries
under the lock of the LRU. The nodes of one tile leave the cache one at a
time. The next proof can then find some of them, and folds the tile again.
One node tile per entry keeps the memory beyond the hashes under 1%.

### D. A cache of proofs

A cache would keep each proof that the Prover returns.

**Why not:** a proof is valid for one leaf at one tree size, and the size
changes with each tree head. The proofs of one size share node tiles, not
proofs.

### E. Faster node hashes

A multi-buffer SHA-256 would hash the nodes of one fold level in parallel
lanes.

**Why not:** it needs assembly for each architecture outside the standard
library. Go's `crypto/sha256` already uses the SHA extensions of amd64 and
the SHA2 instructions of arm64 (`sha256block_amd64.go:15-29` and
`sha256block_arm64.go:14-24` in Go 1.27.2). Faster node hashes shorten
each hash, and the cache removes the hashes.

### F. `batch.Loader` for misses

`batch.Loader` would coalesce the tiles that concurrent proofs miss into
batches.

**Why not:** the window of a `Loader` adds its duration to every miss. A
store reads each tile with a request of its own, as `BlobTiles` does with
`task.Each`. A batch across proofs does not save a round trip. The batch
function also receives a context without the deadline and the values of
any caller. RFC-0054 rejects `batch.Loader` for its group commit because
of the window as well.

### G. `golang.org/x/sync/singleflight`

A `singleflight.Group` would coalesce the reads of one position under a
string key.

**Why not:** one call of the group reads one key. The misses of one proof
would become one read each instead of one `ReadTiles` call. A proof also
cannot join the flight of a wider tile of its position. Core does not
require a `golang.org/x` module.

### H. Pins and recycled buffers

Each proof would pin its node tiles with `cache.Pin`. The `Evicted`
callback of the cache would return the buffer of an evicted node tile to a
pool, and a read would allocate only the entry of the cache.

**Why not:** a pin and an unpin write to the entry of each node tile on
every proof, and every proof reads the node tiles above level 0. With pins,
four goroutines over a warm cache built a proof every 116.6 ns, against
95.6 ns with `Get`. The difference grows with the number of cores that
share those entries. At a tenth of the node tiles of level 0 in the cache,
recycling saved 1.6 µs of the 24.6 µs of a proof.

### I. One node tile per tile and width

The cache would use the `Tile` of each node tile as its key. A `Tile`
includes the width.

**Why not:** each tree head adds a node tile for each new partial tile. The
cache keeps the narrower node tiles of each position until it evicts them.
A proof at an older size also reads its own partial tiles, and tlog-tiles
lets a log delete them. One node tile per position serves every size from
the widest node tile.

### J. A mutex per tile position

Tessera locks a mutex of the tile while it fetches and folds the tile
(`client/client.go:473-499` and `:575-630`).

**Why not:** a proof that waits for a `sync.Mutex` cannot stop waiting
when its context ends. A proof waits for a flight with a `select` on the
channel of the flight and on its context.

### K. Verification of each tile

The Prover would take the root of each size. It would verify the partial
tiles of the right edge against that root. It would verify each full tile
against its hash in the tile above. `TileVerifier` verifies tiles in the
same way.

**Why not:** a Prover reads the storage of its own log, and RFC-0032
builds proofs from that storage without verification. Each method would
also need a root, and the caller would look up the root of each size. A
reader that does not trust the tiles checks its leaves with
`TileVerifier`. A caller that builds proofs for third parties from tiles
that it does not trust would reverse this verdict.

### L. A Prover per tree size

Tessera builds a `ProofBuilder` with a cache of its own for each tree
size (`client/client.go:171-190`).

**Why not:** a full tile is the same at every size, and each new tree head
would start with an empty cache. One Prover per tree keeps its node tiles
across tree heads.

### M. Node tiles from the writer

A `Builder` hashes every node of each tile when it integrates a batch. A
writer would pass the node tiles of each update to the Prover, so the
first proofs at a new size would not read.

**Why not:** a new tree head costs one read and one fold for each new
position at the right edge. Most tree heads add one or two such positions,
and a flight shares each read among the proofs. The writer and the Prover
would also have to run in one process.

## Drawbacks

- The package adds seven exported identifiers: `Prover`, `ProverConfig`,
  `NewProver`, three methods and `ErrConfig`.
- A node tile takes twice the bytes of its tile. Every node tile of a tree
  of 2^30 leaves takes 68.9 GB with SHA-256.
- A proof of a uniformly random leaf of a tree far larger than the cache
  reads and folds one tile at level 0. When the cache contains a tenth of
  the node tiles of level 0, a proof takes 24.6 µs. `ProveInclusion` takes
  51.5 µs.
- Each read allocates three objects. The buffer of a node tile is 16 KiB
  with SHA-256.
- `ProverConfig` requires a `Clock` that only the cache reads, because
  `cache.New` requires one.
- A wrong tile from the reader affects every proof of its position until
  the cache evicts its node tile.
- The Prover does not report a hit ratio. An operator sizes `Capacity`
  from the size of the tree and the table in the section on sizing.
- Concurrent proofs that read two widths of one position can leave the
  narrower node tile in the cache. The next proof at the larger size then
  reads the tile again.

## Open questions

None.

## Unresolved / future work

None.

## References

- RFC 9162, "Certificate Transparency Version 2.0", sections 2.1 and 5.3
  to 5.5, <https://www.rfc-editor.org/rfc/rfc9162.html>.
- C2SP tlog-tiles at b2dd989,
  <https://github.com/C2SP/C2SP/blob/b2dd989/tlog-tiles.md>:
  - The immutable tile endpoint at line 96.
  - The levels between the levels of the tiles at lines 112-114.
  - The partial tiles that a log serves and deletes at lines 131-137.
- `golang.org/x/mod` at v0.42.0:
  - `sumdb/tlog/tlog.go`: `StoredHashIndex` at lines 114-131, and
    `StoredHashCount` at lines 162-173.
  - `sumdb/client.go`: the cache of tiles at line 94, and `readTile` at
    lines 549-604.
- transparency-dev/tessera at c3971f9, `client/client.go`:
  - `ProofBuilder` and `NewProofBuilder` at lines 171-190.
  - The node cache and `GetNode` at lines 436-499.
  - `fetchTileNodes` at lines 521-573.
  - `shardedMutex` at lines 575-630.
- google/trillian at 587494a:
  - `storage/cache/subtree_cache.go`: the cache that is not safe for
    concurrent use at lines 39-45.
  - `storage/cache/log_tile.go`: `PopulateLogTile` at lines 33-95.
  - `storage/mysql/log_storage.go`: a new cache for each transaction at
    lines 185-190.
- Go 1.27.2, `src/crypto/internal/fips140/sha256/`:
  `sha256block_amd64.go` at lines 15-29, and `sha256block_arm64.go` at
  lines 14-24.
- RFC-0024, request coalescing.
- RFC-0032, transparency log trees: the cost of a proof from tiles, and
  the proofs from tiles without verification.
- RFC-0049, the bounded cache.
- RFC-0054, checkpoint witnesses: its alternative H, `batch.Loader` for
  the group commit.
