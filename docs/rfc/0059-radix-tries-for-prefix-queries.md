---
rfc: 0059
title: Radix Tries for Prefix Queries
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-10-15
updated: 2026-10-16
discussion: none
supersedes: none
superseded-by: none
produces-adr: ADR-0056
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# RFC-0059: Radix Tries for Prefix Queries

## Summary

We propose a package `radix` with an adaptive radix tree over byte-string
keys. It finds the longest stored prefix of a key, and every stored key
that is a prefix of a key, in one descent. A B-tree needs one search for
each candidate length of the prefix, and a hash map needs one lookup for
each.

The nodes are pointer-free records in `arena.List` chunks, linked by
`uint32` handles. A leaf stores a key of up to 12, 28 or 60 bytes in place,
as the caller configures the map, and a longer key keeps its bytes in a
region of an `arena.RefSlabs`. Lookups, prefix matches and scans do not
allocate. A map whose number of keys does not grow reuses the records and
the regions that its deletes free.

Core keeps `btree` for ordered maps and range scans, and `idmap` and Go's
map for point lookups, because each is faster than a trie at those
operations.

## Motivation

### Queries that a B-tree cannot do in one descent

- **Longest-prefix match.** A route table keyed by address prefixes, an
  access rule table keyed by path prefixes and a zone table keyed by
  reversed domain labels each look up the longest stored prefix of a key.
- **The stored prefixes of a key.** A policy evaluator applies every rule
  whose path is a prefix of the request path, from the shortest to the
  longest.

The longest stored prefix of a key is not its predecessor in sorted order.
For the stored keys `a` and `ab`, the predecessor of `ac` is `ab`, and its
longest stored prefix is `a`. So a `btree.Map` looks up one candidate
length after another, from the longest down. A key of 56 bytes takes up to
56 lookups, and one lookup in 1M URL keys took 581.6-594.4 ns.

### Published results

- Hash tables look keys up faster than tries. Cuckoo hashing with a
  multiplicative hash has at least twice the lookup throughput of ART for
  64-bit keys, 2.8 times at 1B keys, and 4.8 times its insert throughput
  (Alvarez et al., ICDE 2015).
- B-trees scan faster than tries. An optimized B-tree scans 85-219% faster
  than ART and HOT. For string keys, HOT looks up 5% faster to 14% slower
  than that B-tree, and ART is 34-37% slower. For sparse 32-bit keys, ART
  looks up 129% faster (Müller, Benson and Leis, SIGMOD 2025).
- Binna et al. and Wang et al. measured ART at the highest lookup and
  insert throughput for integer keys among the designs that Go can build,
  both at SIGMOD 2018. HOT, START and the Cuckoo Trie look up faster in
  some measurements. HOT extracts partial keys with `PEXT`, START rewires
  pages with `memfd_create` and `mmap`, and the Cuckoo Trie depends on
  software prefetch. Go 1.27.2 offers none of the three to a package
  outside the standard library.

### Method of the Go measurements

A probe outside this module ran each benchmark with Go 1.27.2 on four cores
of an AMD Ryzen 9 9950X3D in a capped scope, three times for 200 ms. The
runs were not pinned to CPUs, so the timings are indicative. Allocation
counts do not depend on that.

### Go tries and core's `btree`

With 131,072 random keys of 16 bytes, or random IPv4 `/32` keys for bart
and kentik:

| Module | Insert | Allocations per insert |
|---|---|---|
| gaissmai/bart v0.30.0 | 92-107 ns | 2.41 |
| plar/go-adaptive-radix-tree v2.0.4 | 118-126 ns | 4.67 |
| kentik/patricia v1.2.2 | 272-316 ns | 0.0081 |
| armon/go-radix v1.0.0 | 342-352 ns | 5.41 |
| hashicorp/go-immutable-radix v2.1.0, one transaction | 454-463 ns | 7.82 |

With 2^20 URL-like keys of 56.1 bytes on average:

| Operation | `btree.Map` | plar's ART | Go's map |
|---|---|---|---|
| Lookup | 581.6-594.4 ns | 737.8-754.8 ns | 85.8-87.2 ns |
| Insert | 455-461 ns | 596-873 ns | 137-141 ns |
| Full scan, per key | 5.04-5.10 ns | 206-218 ns | — |
| Allocations of the scan | 0 | 1,560,204 | — |

plar dispatches through two interface methods on each node visit, keeps
each node and each leaf in a heap object, and visits every key of the tree
for a prefix scan.

### Garbage collection

`runtime.GC` with a 4-ary tree of 2^20 nodes live took:

| Storage of the nodes | Green Tea collector | Without Green Tea |
|---|---|---|
| One heap object per node | 12.7-15.8 ms | 13.9-16.1 ms |
| One slice, linked by pointer | 2.26-2.32 ms | 6.88-7.48 ms |
| One slice, linked by `uint32` index | 0.045-0.052 ms | 0.045-0.046 ms |
| No tree, an empty heap | 0.043-0.047 ms | 0.044-0.046 ms |

### Search in a node of 16 keys

Over 1,024 nodes of 1 to 16 sorted keys and 4,096 queries, all in the L1
cache:

| Search | Present byte | Absent byte |
|---|---|---|
| Index loop | 2.01-2.13 ns | 2.85-3.10 ns |
| Binary search | 2.19-2.32 ns | 2.26-2.29 ns |
| SWAR over two words with a lane mask | 3.34-3.53 ns | 3.04-3.07 ns |
| SWAR over two words without a lane mask | 2.17-2.20 ns | 1.75-1.77 ns |
| `simd/archsimd` under `GOEXPERIMENT=simd` | 1.45-1.46 ns | 1.36-1.47 ns |

A lane mask needs a compare and a mask for each variable shift, because Go
defines a shift by 64 bits or more as zero. The search without the mask
takes the lowest slot that matches. An unused slot is 0 and has no child,
so a match there does not find a child. The SWAR searches compile without
bounds checks.

### Prior art

- ART, by Leis, Kemper and Neumann, has four node kinds by fan-out, hybrid
  path compression and lazy expansion. It requires that no key is a prefix
  of another, and terminates each string with a byte "which does not
  appear anywhere else in any string".
- bart is a routing table of `netip.Prefix` keys. It keeps the prefixes
  and the children of each 8-bit stride in popcount-compressed arrays of
  256 slots.
- kentik/patricia moved its nodes into one slice linked by index, and its
  README reports that this cut the pointers of a tree of 1M tags from 3
  million to 3.
- dshulyak/art and WenyXu/sync-adaptive-radix-tree read their nodes
  optimistically with plain loads, and switch to a mutex under the race
  detector, which reports those reads.

## Detailed design

### Components

| Component | Responsibility |
|---|---|
| `radix.Key` | The type set `~string \| ~[]byte` |
| `radix.Map[K, V]` | An adaptive radix tree from keys to values, for one writer |
| `radix.Config` | The limit of keys of a map and the longest key that a leaf stores in place |
| `radix.New` | The constructor of a `Map` over a `Config` |
| `radix.MaxLen` | The largest limit of a `Map` |
| `radix.ErrFull` | The error of a `Set` of a new key into a map that contains its limit |
| `radix.ErrTooLong` | The error of a `Set` of a key of 2^32 bytes or more |
| Node records | Pointer-free `node4`, `node16`, `node48`, `node256` and `leaf` records, one `arena.List` per kind |
| Key regions | The bytes of each key longer than the inline length of its map, in an `arena.RefSlabs` |

`radix` imports `go.thesmos.sh/core/arena`, `go.thesmos.sh/core/errs` and
the standard library: `cmp`, `encoding/binary`, `errors`, `iter`,
`math/bits` and `slices`.

### API

```go
// Key is the type set of the keys of a Map: strings and byte slices. A
// Map orders keys by their bytes, as bytes.Compare does.
type Key interface {
	~string | ~[]byte
}

// MaxLen is the largest number of keys that a Map stores.
const MaxLen = 1 << 29

// ErrFull is returned by Map.Set for a new key when the map contains its
// limit of keys. It classifies as errs.Transient.
var ErrFull = errs.WithClass(errors.New("radix: map is full"), errs.Transient)

// ErrTooLong is returned by Map.Set for a key of 2^32 bytes or more. A leaf
// stores the length of its key in 32 bits. It classifies as errs.Invalid.
var ErrTooLong = errs.WithClass(errors.New("radix: key is too long"), errs.Invalid)

// Map is an adaptive radix tree from byte-string keys to values.
//
// A Map finds the longest stored prefix of a key, and every stored key
// that is a prefix of a key, in one descent from the root.
//
// # Concurrency
//
// A Map is not safe for concurrent use. Any number of goroutines may read
// a Map that no goroutine writes.
//
// The zero Map is empty and ready to use. It has the limit MaxLen, and its
// leaves store keys of up to 12 bytes in place.
type Map[K Key, V any] struct{ /* unexported */ }

// Config configures a Map. The zero Config is the configuration of the
// zero Map.
type Config struct {
	// Limit bounds the number of keys, and with it the memory of a map
	// that a caller fills from untrusted input. A Limit of 0 or less, or
	// above MaxLen, is MaxLen.
	Limit int

	// InlineKeyLen is the length up to which a leaf stores a key in place.
	// New rounds it up to 12, 28 or 60 bytes. Leaves of 16, 32 and 64 bytes
	// store keys of those lengths. A longer key keeps its bytes in a region
	// of the map's arena.RefSlabs. An InlineKeyLen of 12 or less is 12, and
	// one above 60 is 60.
	InlineKeyLen int
}

// New returns an empty Map over cfg.
func New[K Key, V any](cfg Config) *Map[K, V]

// Get returns the value of key and whether the map contains key.
func (m *Map[K, V]) Get(key K) (V, bool)

// Set stores value under key. It returns the previous value and true when
// the map contained key. Set copies the bytes of key.
//
// Error modes:
//
//   - A key of 2^32 bytes or more returns ErrTooLong, and the map is
//     unchanged.
//   - A new key in a map that contains its limit of keys returns ErrFull,
//     and the map is unchanged.
func (m *Map[K, V]) Set(key K, value V) (V, bool, error)

// Delete removes key. It returns the removed value and true when the map
// contained key.
func (m *Map[K, V]) Delete(key K) (V, bool)

// Len returns the number of keys in the map.
func (m *Map[K, V]) Len() int

// Reset removes every key and keeps the memory of the map for later keys.
func (m *Map[K, V]) Reset()

// LongestPrefix returns the length n of the longest stored key that is a
// prefix of key, and its value. key[:n] is that stored key. ok is false
// when no stored key is a prefix of key.
func (m *Map[K, V]) LongestPrefix(key K) (n int, value V, ok bool)

// Prefixes returns an iterator over every stored key that is a prefix of
// key, from the shortest to the longest. It yields the length n of each,
// so key[:n] is the stored key, and its value.
func (m *Map[K, V]) Prefixes(key K) iter.Seq2[int, V]

// Scan returns an iterator over every stored key that starts with prefix,
// in the order of the bytes of the keys. Each yielded key is valid until
// the yield returns or the loop body writes to the map.
func (m *Map[K, V]) Scan(prefix K) iter.Seq2[[]byte, V]

// All returns an iterator over every key in the order of their bytes, as
// Scan with an empty prefix.
func (m *Map[K, V]) All() iter.Seq2[[]byte, V]
```

An empty key is a valid key. It is the key of the root, and it is a prefix
of every key. The root is an inner node that does not skip bytes. The first
`Set` creates it, and a `Delete` never removes it.

### Handles

A child slot, a term slot and the root are `uint32` handles. The low 3
bits of a handle are its kind, and the high 29 bits are an index into the
`arena.List` of that kind:

| Low 3 bits | Kind | Index |
|---|---|---|
| 0 | No record | The handle is 0 |
| 1 | `leaf` | Index into the leaves |
| 2 | `node4` | Index into the node4 records |
| 3 | `node16` | Index into the node16 records |
| 4 | `node48` | Index into the node48 records |
| 5 | `node256` | Index into the node256 records |

A map with keys has at most one inner node per key, so no kind of record
needs more indexes than the leaves. `MaxLen` is 2^29 keys, one per index of
29 bits.

Each kind keeps a stack of the indexes of its free records. A delete frees
a record, and so does a node that grows or shrinks to another kind. An
insert takes an index from the stack before it appends a record. A `switch` on the kind
bits selects the record, so a node visit does not call an interface method
or a function value.

### Records

```go
// header is the part that every inner node has.
type header struct {
	term   uint32  // the leaf of the key that ends at this node, or 0
	skip   uint32  // the number of key bytes between the parent's branch byte and this node's branch byte
	prefix [8]byte // the first min(skip, 8) of those bytes
	count  uint16  // the number of children
}

type node4 struct { // 40 bytes
	header
	keys [4]byte   // the branch bytes of the children, sorted
	kids [4]uint32 // the children, in the order of keys
}

type node16 struct { // 100 bytes
	header
	keys [16]byte // sorted branch bytes, and 0 in every unused slot
	kids [16]uint32
}

type node48 struct { // 504 bytes
	header
	present [4]uint64  // bit b is set when branch byte b has a child
	index   [256]uint8 // the slot of the child of branch byte b, plus one
	kids    [48]uint32
}

type node256 struct { // 1,080 bytes
	header
	present [4]uint64
	kids    [256]uint32 // the child of branch byte b at index b
}

// leaf16, leaf32 and leaf64 are the records of the leaves of the three
// widths. A record stores a key of up to its size minus 4 bytes in place.
// For a longer key, its first 8 bytes are the arena.Ref of the region of
// the key, little-endian. Its last 4 bytes are the length of the key,
// little-endian.
type (
	leaf16 [16]byte
	leaf32 [32]byte
	leaf64 [64]byte
)
```

A map keeps its leaves in one `arena.List` of the width that `New`
chooses. Each read of a leaf takes the bytes of its record from a `switch`
on the width. The width is the same for every leaf of the map, so the
branch predicts. The `switch` does not add a call through a function value.

The values are in an `arena.List[V]`, at the index of their leaf. A delete
sets the value to the zero `V`, so the map does not keep what the value
referenced.

A node grows to the next kind when a child arrives and its slots are full:
a `node4` at the fifth child, a `node16` at the seventeenth and a `node48`
at the forty-ninth. It shrinks with a margin, so a key that comes
and goes at a boundary does not resize the node each time: a `node256` to
a `node48` at 40 children, a `node48` to a `node16` at 12 and a `node16`
to a `node4` at 3. A `node4` with one child and no term merges into its
child. An inner node other than the root has at least two items among its
children and its term.

### Path compression

A node skips the key bytes that every key below it shares, so a chain of
nodes with one child each does not exist. The header contains the first 8
skipped bytes, which an insert compares. A lookup passes over the skipped
bytes without reading them. Where a subtree would contain a single key, a
leaf takes its place, so a lookup ends at a leaf as soon as the key has no
sibling.

A lookup compares the key with the stored key of the leaf where it ends,
and finds the leaf only when the two keys are equal. A key that differs
inside a skip does not find a key. `LongestPrefix` and `Prefixes`
compare each term on the path with the key from the last compared position
up to the length of the term's key. A term whose key differs from the key is not a prefix,
and every term below it differs too. So the descent compares each byte of
the key at most once.

An insert that needs a skipped byte beyond the first 8 reads it from the
key of the smallest leaf below the node. The descent to that leaf takes the
term of a node where the node has one, and otherwise the child of its
smallest branch byte. A delete passes over the skipped bytes of each node
on its path and compares the whole key with the key of the term or the
leaf where it ends, so it does not read a skipped byte.

### Search in a node

| Kind | Lookup of a branch byte | Insert of a branch byte |
|---|---|---|
| `node4` | A loop over 4 bytes | A loop to the insert position |
| `node16` | SWAR over two 64-bit words, where an unused slot is 0 and has no child | A loop to the insert position |
| `node48` | `index[b]` | The first free slot, then `index[b]` |
| `node256` | `kids[b]` | `kids[b]` |

### Iteration

`Scan` descends to the node or the leaf that covers the prefix, compares
the prefix with the key of a leaf below it, and walks that subtree. A walk
yields a node's term before its children, and the children in the order of
their branch bytes. `node48` and `node256` find their next child in the
`present` bitmap with `bits.TrailingZeros64`. The walk recurses once per
level, so its depth is the height of the tree, at most the length of the
longest key plus one.

The iterator yields a key that its leaf stores in place as a slice of the
leaf record. It yields a longer key as a slice of its region. The capacity
of each slice is the length of the key, so an append in the loop body
copies the key. Each slice is valid until the yield returns or the loop
body writes to the map.

The map counts its structural writes. A `Set` of a new key, a `Delete` of a
present key and a `Reset` increment the count, and a `Set` that replaces a
value does not. After each yield, an iterator compares the count with the
value it read at its start, and it ends when the count grew. So a loop
body may replace values during a scan, and a body that adds or removes
keys ends the scan.

### Invariants

- Every inner node other than the root has at least two items among its
  children and its term.
- The `count` of a node equals its children. The `present` bits of a
  `node48` or a `node256` match its occupied slots, and every unused slot
  of a `node4` or a `node16` is 0 and has no child.
- The `skip` bytes of a node are the bytes that every key below the node
  has at those positions, and `prefix` contains the first 8 of them.
- A leaf stores a key of up to the inline length of its map in place, and
  a longer key in a region of the map's `RefSlabs`.
- The stacks of free indexes contain no index of a live record.
- `All` yields the keys in the order of `bytes.Compare`.

### Bounds

| Quantity | Bound |
|---|---|
| Keys | The limit of the map, at most `MaxLen`, 2^29 |
| Length of a key | Less than 2^32 bytes |
| Leaf | 16, 32 or 64 bytes per key, for an inline length of 12, 28 or 60 bytes |
| Key bytes | None for a key of up to the inline length. The size class of the key for a longer key, at least 64 bytes, and less than 1.125 times its length above 64 bytes |
| Inner nodes | At most one per key. Apart from the root, a `node48` with 13 children has the most bytes per item, 504 / 13 = 38.8 bytes |
| Node visits of a lookup | At most the length of the key plus one |
| Depth of an iteration's recursion | The height of the tree, at most the length of the longest key plus one |

### Failure handling

| Condition | Function | Result |
|---|---|---|
| A new key in a map that contains its limit | `Set` | `ErrFull`, the map unchanged |
| A key of 2^32 bytes or more | `Set` | `ErrTooLong`, the map unchanged |
| A key, a node or a slab that the map cannot allocate | `Set` | The Go runtime's out-of-memory failure, as for any allocation |
| A leaf whose region the slabs refuse to take back | `Delete`, `Reset` | `Delete` does not reuse the leaf. `Reset` replaces the slabs with empty slabs. Only a record that the map did not write has such a region |
| A write that adds or removes a key during an iteration | `Prefixes`, `Scan`, `All` | The iteration ends after the loop body returns |
| An empty key | Every method | A valid key, which sorts before every other key |
| A `Limit` of 0 or less, or above `MaxLen` | `New` | A map with the limit `MaxLen` |
| An `InlineKeyLen` of 0 or less, or above 60 | `New` | A map with leaves of 16 bytes, or of 64 bytes |

### Allocation contract

| Call | Objects |
|---|---|
| `New` | The map |
| `Get`, `Len`, `LongestPrefix` | 0 |
| A range loop over `Prefixes`, `Scan` or `All` | 0 when the compiler inlines the iterator into the loop |
| `Set` | 0 when the free stacks and the `RefSlabs` have room. Otherwise the growth of a list of records or values, one slab of 1 MiB for long keys, or the growth of a stack or a free list |
| `Delete` | 0 when the free stacks and the free lists of the `RefSlabs` have room. Otherwise the growth of a stack or a free list |
| `Reset` | 0 when the free lists of the `RefSlabs` have room for the regions of the long keys. Otherwise the growth of a free list |

A map whose number of keys does not grow reuses the records and the
regions that its deletes free. Once its stacks and free lists have grown to
that working set, its writes do not allocate.

### Tests

| # | Guarantee |
|---|---|
| 1 | A stateful machine of `Set`, `Get`, `Delete`, `LongestPrefix`, `Prefixes`, `Scan` and `Reset` returns what a model of a Go map and its sorted keys returns, for each of the three leaf widths. Its keys extend stems on both sides of each inline length, and its fills of up to 80 keys grow a node to every kind |
| 2 | A machine of writes checks every invariant after each step, for each of the three leaf widths. It requires a `node16`, a `node48`, a `node256`, a skip of more than 8 bytes and a key in a region, each in at least 5% of its cases |
| 3 | `All` and `Scan` yield keys in the order of `bytes.Compare`, and `Prefixes` yields lengths in increasing order |
| 4 | A body that adds or removes a key ends the iteration after it returns, and a body that replaces a value does not |
| 5 | A `Set` of a new key into a map that contains its limit returns `ErrFull` and leaves the map unchanged, and a `Set` that replaces a value in a full map succeeds |
| 6 | A `Set` of a key of 2^32 bytes returns `ErrTooLong` and stores no key |
| 7 | `TestMapAllocs` and the benchmarks state the counts of the allocation contract |
| 8 | The benchmarks measure `Get`, `LongestPrefix`, a `Set` that replaces a value, `All` and `runtime.GC` with 1M keys live. They compare `LongestPrefix` with a `btree.Map` and one lookup per length |
| 9 | The suites cover every statement, and dokimi-mutate-go detects every mutant of `radix` that an annotation does not suppress |

## Alternatives considered

### A. Longest-prefix match over `btree`

A caller would look up each candidate length of the prefix in a
`btree.Map`.

**Why not:** a key of length L costs up to L lookups. A key of 56 bytes
costs up to 56 lookups of 581.6-594.4 ns in 1M URL keys.

### B. Popcount-compressed nodes, as in bart and the qp-trie

A node would keep a bitmap of its children and a packed array, and find a
child by counting the bits below it.

**Why not:** no source compares such a byte trie with ART on one machine,
and ART has the highest published throughput among the designs that Go can
build. A prototype that measures a popcount-compressed node at an equal
lookup time and with less memory on the same keys reverses this.

### C. HOT, START and the Cuckoo Trie

**Why not:** HOT extracts partial keys with a parallel bit extract and
searches its nodes with AVX2. START rewires pages, and the Cuckoo Trie
depends on software prefetch. Go 1.27.2 offers none of them to a package
outside the standard library.

### D. One heap object per node

**Why not:** with 2^20 nodes live, a collection took 12.7-16.1 ms against
0.045-0.052 ms for nodes in a slice linked by index, and plar allocates
4.67 objects per insert.

### E. 64-bit handles

A handle would have 61 bits of index, so only `Config.Limit` would bound a
map.

**Why not:** every child slot doubles, so a `node256` takes 2,104 bytes
instead of 1,080 and a `node4` 64 instead of 40.

### F. A `Set` without an error

**Why not:** at the limit, a `Set` of a new key would have no result that
tells the caller that the key is missing.

### G. Iterations that continue after a write, as in `btree`

The iterator would descend again to the key after the last key it yielded.

**Why not:** that descent needs the bytes of the last yielded key, and a
`Delete` in the loop body frees the region of that key. A copy of keys
longer than a fixed buffer allocates on every iteration.

### H. Readers during writes

The nodes would use optimistic lock coupling with a version word.

**Why not:** optimistic lock coupling costs about 65% on one thread (Leis
et al., DaMoN 2016). Go requires every node word that a reader reads during
a write to be a `sync/atomic` type, and the Go implementations that read
plain words fall back to a mutex under the race detector.

### I. `Clone` with shared nodes

**Why not:** a free stack reuses the index of a node that a clone still
refers to. The nodes of a persistent trie must be heap objects, with the
garbage collection cost of alternative D.

### J. Keys of any length in bits

**Why not:** a CIDR prefix such as a `/12` ends inside a byte, and bart's
stride-8 nodes serve it. Keys of this package are byte strings.

### K. One inline length for every map

Every leaf would store keys of up to 12 bytes in place.

**Why not:** a key of 16 bytes, such as an IPv6 address or a UUID, would
take a slab region of 64 bytes beside its leaf of 16, where a leaf of 32
bytes stores it in place. A fixed length of 28 bytes would double the leaf
of every key of up to 12 bytes instead.

## Drawbacks

- The module gains a package with one map type, a configuration, a
  constructor, a key constraint, a constant and two sentinels. The package
  depends on `arena.RefSlabs`.
- A key longer than the inline length of its map takes at least 64 bytes
  of a slab, the smallest size class.
- Leaves of 16, 32 and 64 bytes add a `switch` on the width to every read
  of a leaf. The machines of the tests run once for each width.
- A write that adds or removes a key ends every iteration in progress.
- `Set` returns three results. A caller checks the error even though `Set`
  returns it only for a map at its limit or for a key of 2^32 bytes or more.
- One goroutine writes, and no goroutine reads during a write.
- An iteration recurses once per level of the tree.
- A key is a byte string, so prefix lengths in bits are out.

## Open questions

None.

## Unresolved / future work

None.

## References

- V. Leis, A. Kemper and T. Neumann, "The Adaptive Radix Tree: ARTful
  Indexing for Main-Memory Databases", ICDE 2013,
  <https://db.in.tum.de/~leis/papers/ART.pdf>.
- V. Leis, F. Scheibner, A. Kemper and T. Neumann, "The ART of Practical
  Synchronization", DaMoN 2016,
  <https://db.in.tum.de/~leis/papers/artsync.pdf>.
- V. Alvarez, S. Richter, X. Chen and J. Dittrich, "A Comparison of
  Adaptive Radix Trees and Hash Tables", ICDE 2015,
  <https://bigdata.uni-saarland.de/publications/ARCD15.pdf>.
- M. Müller, L. Benson and V. Leis, "B-Trees Are Back: Engineering Fast and
  Pageable Node Layouts", SIGMOD 2025,
  <https://www.cs.cit.tum.de/fileadmin/w00cfj/dis/papers/btrees-are-back.pdf>.
- R. Binna, E. Zangerle, M. Pichl, G. Specht and V. Leis, "HOT: A Height
  Optimized Trie Index for Main-Memory Database Systems", SIGMOD 2018.
- Z. Wang et al., "Building a Bw-Tree Takes More Than Just Buzz Words",
  SIGMOD 2018.
- P. Fent, M. Jungmair, A. Kipf and T. Neumann, "START — Self-Tuning
  Adaptive Radix Tree", ICDE Workshops 2020.
- A. Zeitak and A. Morrison, "Cuckoo Trie: Exploiting Memory-Level
  Parallelism for Efficient DRAM Indexing", SOSP 2021,
  <https://arxiv.org/abs/2201.09331>.
- gaissmai/bart v0.30.0, `internal/nodes/bart.go`,
  <https://github.com/gaissmai/bart>.
- kentik/patricia v1.2.2, `README.md`, <https://github.com/kentik/patricia>.
- plar/go-adaptive-radix-tree v2.0.4,
  <https://github.com/plar/go-adaptive-radix-tree>.
- dshulyak/art, `olock_race.go`, and WenyXu/sync-adaptive-radix-tree,
  `optimistic_lock_race.go`.
- The Go Authors, "A Guide to the Go Garbage Collector",
  <https://go.dev/doc/gc-guide>.
- RFC-0043, B-tree ordered maps, for the method of measurement and the
  rejection of chunked nodes under `Clone`.
- ADR-0056, `arena.RefSlabs`.
