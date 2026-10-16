---
rfc: 0060
title: Approximate-Membership Filters and XXH3 Hashing
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-10-15
updated: 2026-10-15
discussion: none
supersedes: none
superseded-by: none
produces-adr: tbd
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# RFC-0060: Approximate-Membership Filters and XXH3 Hashing

## Summary

We propose a package `filter` with two approximate-membership filters over
64-bit hashes, and a package `hash/xxh3` with a stable 64-bit hash for the
filters that a caller persists.

- `filter.Bloom` is the split block Bloom filter of Parquet. A key sets one
  bit in each of the eight 32-bit words of one 256-bit block, so a query
  reads one block and has no branch. Goroutines can add and query at the
  same time, and a batch query overlaps the cache misses of 16 keys.
- `filter.Fuse` is a 3-wise binary fuse filter, built once from a complete
  set of hashes. At 8 bits per fingerprint it takes 9.0 bits per key for a
  false-positive rate of 0.39%, where a Bloom filter that reads one cache
  line takes about 14 bits per key. Its encoding is its memory layout, and
  the encoding is frozen at its first release.
- `xxh3.Hash` computes XXH3-64 with seed 0. A port of its short-input path
  without `unsafe` hashes 8 to 64 bytes in 2.0-2.8 ns.

## Motivation

### Where filters apply

A filter reports whether a set may contain a key. It reports true for every
key of the set, and for an absent key at a rate that the caller chooses.
Stores query a filter before they read a block from disk, caches before
they admit a key, and joins before they probe a table. Core has no filter,
so each consumer writes its own or imports one.

The Go libraries are slower than designs that read one cache line, and the
fastest designs need either `unsafe` or a module that core does not import.

### Method of the measurements

A probe outside this module ran each benchmark with Go 1.27.2 on four cores
of an AMD Ryzen 9 9950X3D in a capped scope, three times for 200 ms, not
pinned to CPUs. Each filter takes a 64-bit hash of a random 64-bit key. The
false-positive rate is measured over 2^20 absent keys. The filters of 2^27
keys take 168 MB, beyond the 96 MB L3 cache of the larger complex of the
processor. Tests checked that no filter loses a key, and that the batched,
atomic and SIMD queries return what the plain queries return.

### Filters of 10 bits per key

| Filter | False positives | Query at 2^20 keys, absent and present | Query at 2^27 keys, absent and present | Insert at 2^20 keys |
|---|---|---|---|---|
| Standard Bloom, k=7, double hashing | 0.81-0.87% | 12.6-12.9, 7.0-7.2 ns | 37.3-37.8, 44.9-45.8 ns | 8.2 ns |
| 512-bit blocks, k=6, exit at the first unset bit | 0.96-0.97% | 10.0-10.1, 4.8-4.9 ns | 23.5-23.9, 30.8-31.3 ns | 5.7-5.8 ns |
| 512-bit blocks, k=6, without a branch | 0.96-0.97% | 5.3-5.4, 5.3 ns | 33.0-33.1, 32.2-32.8 ns | — |
| Split block, the layout of Parquet | 1.25-1.28% | 4.7, 4.7 ns | 33.2-34.1, 33.0-33.1 ns | 6.1-6.3 ns |
| Register-blocked 64-bit, k=4 | 1.87-1.90% | 3.1, 3.0 ns | 19.9-21.2, 22.3-23.0 ns | 2.9 ns |
| Cache-sectorized 512-bit, z=2, k=6 | 1.09-1.12% | 5.6-5.7, 4.9-5.0 ns | 32.5-34.7, 31.6-32.0 ns | 4.7-4.8 ns |

The Go libraries queried in 37.1-40.3 ns for bits-and-blooms, 16.5-20.3 ns
for holiman/bloomfilter, 5.7-10.9 ns for blobloom and 11.1-17.9 ns for
Pebble v2.1.7, which includes Pebble's hash of the key. The cuckoo filter
libraries took 8.8-25.5 ns.

### Static filters

| Filter | Bits per key | False positives | Query at 2^20 keys | Query at 2^27 keys | Construction |
|---|---|---|---|---|---|
| Binary fuse, 8-bit fingerprints, FastFilter/xorfilter v0.5.1 | 9.0 | 0.387-0.388% | 4.2-4.3 ns | 26.9-27.6 ns | 26.0-27.0 ns per key at 2^20 with a reused builder and no allocation, 43.0 ns per key at 2^27 |
| Xor, 8-bit fingerprints, the same module | 9.84 | 0.393% | 4.2-4.3 ns | — | 38.7-39.8 ns per key, 9 allocations |

Pebble's simulation of its cache-local Bloom filter measured 0.365% at 14
bits per key and 0.245% at 16. A query whose next key depends on its result
took 86.0-87.3 ns in DRAM for a 512-bit blocked filter, 84.7-93.9 ns for
the split block filter and 99.3-101.0 ns for the binary fuse filter, which
issues its three loads at once.

### Batches, atomics and SIMD

- A batch query loads one word of the block of each of 16 keys before it
  tests any of them. At 2^27 keys it took 9.9-10.4 ns per key for the split
  block filter, against 30.3-31.1 ns for single queries.
- An atomic query of the split block filter took 5.7-5.8 ns at 2^20 keys
  against 4.8 ns for the plain query, which the probe unrolled. At 2^27 keys
  both took 32.6-35.6 ns. An atomic load compiles to a plain `MOV` on amd64.
- An atomic insert that loads each word first and issues `atomic.Uint32.Or`
  only for a word that lacks its bit took 10.6 ns against 6.8-6.9 ns for the
  plain insert. Four goroutines inserted at 2.06-2.11 ns per insert in
  total.
- `simd/archsimd` tests a split block with one 256-bit operation. It took
  1.84-2.08 ns at 2^20 keys and 10.5-13.3 ns at 2^27 keys. The package
  exists only under `GOEXPERIMENT=simd`, and core does not use a package
  that exists only under an experiment.

### Hashes of short keys

Each hash ran through a function value:

| Hash | 8 bytes | 16 bytes | 32 bytes | 64 bytes |
|---|---|---|---|---|
| `maphash.Bytes` | 4.3-4.5 ns | 4.3 ns | 4.3-4.6 ns | 4.5-4.8 ns |
| CRC32C, 32 bits | 4.0-4.2 ns | 4.2-4.4 ns | 4.6-4.9 ns | 5.8 ns |
| FNV-1a 64 | 3.1 ns | 5.9-6.0 ns | 14.0-14.2 ns | 28.7-28.9 ns |
| XXH64, cespare/xxhash v2.3.0 with assembly | 2.6 ns | 3.1 ns | 5.4 ns | 6.3-6.5 ns |
| XXH3-64, zeebo/xxh3 v1.1.0 | 2.2-2.5 ns | 2.1-2.5 ns | 2.5-2.7 ns | 3.1-3.2 ns |
| XXH3-64, a port on `encoding/binary` and `math/bits` | 2.2-2.3 ns | 2.0 ns | 2.1-2.2 ns | 2.8 ns |

The port covers inputs of up to 128 bytes, and it returned the value of
zeebo/xxh3 for each such input among 20,000 random inputs of 0 to 300
bytes.

A filter that a store persists needs a hash whose values do not change
between processes. A `maphash.Seed` "is local to a single process and
cannot be serialized or otherwise recreated in a different process", and
a 32-bit hash degrades the false-positive rate of a filter of millions of
keys.

### Prior art

- Lang, Neumann, Kemper and Boncz compared Bloom and cuckoo filters by the
  work that a negative lookup saves. High-throughput workloads should use a
  cache-sectorized Bloom filter, and workloads where precision is essential
  should use a cuckoo filter (PVLDB 12(5), 2019).
- Graf and Lemire's 3-wise binary fuse filter has about 1.125n fingerprints,
  within 13% of the storage lower bound, against 23% for xor filters and 44%
  for Bloom filters. Its construction is more than twice as fast as that of
  an xor filter.
- Dillinger and Walzer's Ribbon filters are 0.5-20% above the lower bound,
  by configuration. At about 1% false positives and 10^6 keys, a
  homogeneous Ribbon filter of width 64 queried in 39 ns and a standard one
  of width 128 in 58 ns, against 15 ns for an xor filter and 10-14 ns for
  blocked Bloom filters.
- RocksDB's in-memory `DynamicBloom` inserts concurrently by loading a word
  and issuing an atomic OR only when the word lacks a bit of the key. Its
  batch query prefetches the line of each key before it tests any key.
- Pebble added binary fuse filters in 2026, with fingerprints of 4, 8, 10,
  12 and 16 bits and keys hashed by XXH3. It gives smaller levels of its
  tree better filters, from 16 bits per fingerprint at L0 to 4 at L6.
- Parquet specifies the split block layout, its salts and its block
  selection, and needs 10.5 bits per insert for 1%, 16.9 for 0.1% and 26.4
  for 0.01%.

## Detailed design

### Components

| Component | Responsibility |
|---|---|
| `filter.Bloom` | A split block Bloom filter of a fixed size, safe for concurrent `Add` and queries |
| `filter.FuseBuilder` | Builds the encoding of a binary fuse filter from a set of hashes, with reusable scratch |
| `filter.Fuse` | Queries a binary fuse filter in place over its encoding |
| `filter.ParseFuse` | Checks an encoding and returns the `Fuse` over it |
| `filter` sentinels | `ErrBitsPerKey`, `ErrTooLarge`, `ErrFingerprintBits`, `ErrBuild` and `ErrCorrupt` |
| `xxh3.Hash`, `xxh3.HashString` | XXH3-64 with seed 0 and the default secret |

`filter` imports `go.thesmos.sh/core/errs` and the standard library:
`encoding/binary`, `errors`, `math` and `math/bits`, `slices` and
`sync/atomic`. `hash/xxh3` imports `encoding/binary` and `math/bits` only.

### API of filter

```go
// Bloom is a split block Bloom filter over 64-bit hashes. A hash selects
// one 256-bit block and sets one bit in each of its eight 32-bit words.
// The zero Bloom has no blocks, and its MayContain reports true for every
// hash.
//
// # Concurrency
//
// Add, MayContain and MayContainBatch are safe for concurrent use. Reset
// is not safe concurrently with any other method.
type Bloom struct{ /* unexported */ }

// NewBloom returns an empty filter with bitsPerKey bits for each of keys
// keys, rounded up to whole blocks. A keys below 1 counts as 1.
//
// Error modes:
//
//   - A bitsPerKey that is NaN, at most 0 or above 256 returns
//     ErrBitsPerKey.
//   - A filter of more than 2^32 blocks returns ErrTooLarge.
func NewBloom(keys int, bitsPerKey float64) (*Bloom, error)

// Add adds the hash h.
func (f *Bloom) Add(h uint64)

// MayContain reports whether the filter may contain h. It returns true for
// every h that Add added.
func (f *Bloom) MayContain(h uint64) bool

// MayContainBatch sets out[i] to MayContain(hs[i]) for every i below the
// shorter length of hs and out, and returns that length. It loads the
// blocks of a group of 16 hashes before it tests them.
func (f *Bloom) MayContainBatch(hs []uint64, out []bool) int

// Reset removes every hash.
func (f *Bloom) Reset()

// Size returns the size of the filter in bytes.
func (f *Bloom) Size() int

// FuseBuilder builds binary fuse filters. It keeps its scratch memory
// between builds, so a builder that built a set of n hashes builds another
// set of up to n hashes without an allocation. The zero FuseBuilder is
// ready to use.
type FuseBuilder struct{ /* unexported */ }

// Append builds a binary fuse filter of hashes with fingerprints of bits
// bits, appends its encoding to dst and returns the extended slice. Append
// does not modify hashes, and a hash that hashes contains more than once
// counts once.
//
// Error modes:
//
//   - A bits other than 4, 8, 10, 12 or 16 returns ErrFingerprintBits.
//   - More hashes than fit 2^32 fingerprints returns ErrTooLarge.
//   - No seed of 1,024 that yields a filter returns ErrBuild.
func (b *FuseBuilder) Append(dst []byte, hashes []uint64, bits int) ([]byte, error)

// Fuse is a binary fuse filter over its encoding. It reads the encoding in
// place and keeps a reference to it.
type Fuse struct{ /* unexported */ }

// ParseFuse checks the encoding data and returns the filter over it. It
// does not copy data, so the caller must not change data while it uses the
// filter.
//
// Error modes:
//
//   - An unknown version, an unsupported width of fingerprints, a segment
//     exponent outside 2 to 18, a length that does not match the header,
//     or a padding byte that is not zero returns ErrCorrupt.
func ParseFuse(data []byte) (Fuse, error)

// MayContain reports whether the filter may contain h. It returns true for
// every hash of the set that built the filter.
func (f Fuse) MayContain(h uint64) bool

// Bits returns the width of the fingerprints in bits.
func (f Fuse) Bits() int
```

The sentinels classify as `errs.Invalid`, except `ErrCorrupt`, which
classifies as `errs.Integrity`:

```go
var (
	ErrBitsPerKey      = errs.WithClass(errors.New("filter: bits per key out of range"), errs.Invalid)
	ErrTooLarge        = errs.WithClass(errors.New("filter: filter too large"), errs.Invalid)
	ErrFingerprintBits = errs.WithClass(errors.New("filter: unsupported fingerprint width"), errs.Invalid)
	ErrBuild           = errs.WithClass(errors.New("filter: no seed yields a filter"), errs.Invalid)
	ErrCorrupt         = errs.WithClass(errors.New("filter: corrupt fuse encoding"), errs.Integrity)
)
```

`ErrBuild` classifies as `errs.Invalid` because the builder chooses its
seeds deterministically, so the same set fails again.

### API of hash/xxh3

```go
// Hash returns XXH3-64 of b with seed 0 and the default secret, as xxHash
// 0.8 computes it. Its values do not change between processes, platforms
// or releases.
func Hash(b []byte) uint64

// HashString returns Hash of the bytes of s without converting s.
func HashString(s string) uint64
```

The package implements the four paths of XXH3 by input length. Inputs of
up to 16 bytes, of 17 to 128 bytes and of 129 to 240 bytes take paths of
their own. Longer inputs are hashed in stripes of 64 bytes and blocks of
1,024 bytes over the secret of 192 bytes.

### The split block Bloom filter

A filter of z blocks selects block `((h >> 32) * z) >> 32` for the hash h.
The low 32 bits x of h set bit `(x * salt[i]) >> 27` of word i of the block,
for the eight odd constants of Parquet:

```text
0x47b6137b 0x44974d91 0x8824ad5b 0xa2b7289d
0x705495c7 0x2df1424b 0x9efc4947 0x5c6bfb31
```

- The words are `atomic.Uint32` values in an array of `[8]atomic.Uint32`
  blocks, which contains no pointer.
- `MayContain` loads the eight words and combines the eight tests with
  bitwise AND. It has no branch on the data.
- `Add` loads each word and calls `Or` only on a word that lacks its bit, so
  an `Add` of a hash that the filter contains does not write.
- `MayContainBatch` computes the blocks of 16 hashes and loads the first
  word of each before it tests any of them. A remainder of fewer than 16
  hashes takes single queries.

The filter has no encoding. A store that persists a filter persists a
`Fuse`.

### The binary fuse filter

The builder mixes each hash with a seed, `x = murmur64(h + seed)`. The
fingerprint of x is the low bits of `x ^ x >> 32`. For a segment length
2^e and S segments, the three positions of x are:

```text
p0 = high 64 bits of x * (S * 2^e)
p1 = (p0 + 2^e) ^ ((x >> 18) & (2^e - 1))
p2 = (p0 + 2 * 2^e) ^ (x & (2^e - 1))
```

The filter has (S + 2) * 2^e fingerprints. A query reports true when the
fingerprint of x equals the exclusive or of the fingerprints at p0, p1 and
p2. The builder finds the fingerprints by peeling: it repeatedly removes a
hash that is the only one at one of its positions, and assigns the
fingerprints in the reverse order of the removals.

A build must produce the same bytes on every platform, so a golden test can
pin them. `math.Log` runs assembly on amd64 and Go code elsewhere, and the
Go compiler may fuse floating-point operations on arm64. So the builder
takes its parameters from two integer tables:

- The segment exponent e is the largest k from 2 to 18 whose threshold is
  at most n. The thresholds are the smallest n with
  ⌊ln n / ln 3.33 + 2.25⌋ ≥ k: 1, 3, 9, 28, 92, 304, 1,010, 3,362, 11,193,
  37,273, 124,118, 413,310, 1,376,322, 4,583,150, 15,261,887, 50,822,082
  and 169,237,530.
- The capacity is ⌈n * q / 1024⌉ for the factor q of the bit length of n.
  q is 1,024 times max(1.125, 0.875 + 0.25 * ln 10^6 / ln m), rounded up,
  for the smallest m of that bit length. It runs from 5,999 for n of 2 or 3
  to 1,152 from 2^20 up. A set of one hash has the capacity 0.
- S is the capacity divided by 2^e, rounded up, at least 3, minus 2.

A test regenerates both tables from the formulas with `math.Log`, so the
tables cannot drift from the formulas that they replace.

The seeds come from a splitmix64 sequence that starts at 1. A seed that
leaves a hash without a removable position gives way to the next seed.
From the second attempt on, every fourth attempt for a set of 5 to 999,999
hashes halves the segment length and sets S to 2S + 2, which keeps the
number of fingerprints. The attempt after it restores both, as the authors'
implementation does. After a failed attempt, the builder sorts the hashes
in its scratch and removes the repeated ones before the next attempt.

### Encoding of a binary fuse filter

The encoding is frozen from the release that first contains it. Integers
are big-endian:

| Offset | Bytes | Field |
|---|---|---|
| 0 | 1 | Version, 1 |
| 1 | 1 | Bits per fingerprint: 4, 8, 10, 12 or 16 |
| 2 | 1 | Segment exponent e, from 2 to 18 |
| 3 | 4 | Segment count S |
| 7 | 8 | Seed |
| 15 | ⌈F * bits / 8⌉ | F = (S + 2) * 2^e fingerprints, or none when S is 0 |
| 15 + ⌈F * bits / 8⌉ | 3 | Zero bytes |

Fingerprint i occupies bits i * bits to (i + 1) * bits - 1 of the
fingerprint bytes, counted from the most significant bit of the first byte.
A query reads the big-endian 32-bit word at byte ⌊i * bits / 8⌋ and shifts
the fingerprint out of it, so every width takes one load. The three zero
bytes let the last fingerprint take the same load.

A set of no hashes encodes with the segment exponent 2, S of 0, the seed 0
and no fingerprints, and its `MayContain` reports false for every hash.

### Invariants

- A `Bloom` and a `Fuse` report true for every hash of their set.
- `MayContainBatch` sets `out[i]` to what `MayContain(hs[i])` returns.
- `Add` on one goroutine and `MayContain` of the same hash on another,
  started after `Add` returned, report true.
- `FuseBuilder.Append` produces the same bytes for the same sequence of
  hashes and the same width on every platform.
- `ParseFuse` accepts every encoding that `Append` produces, and returns
  `ErrCorrupt` for every encoding whose header and length do not agree.
- `xxh3.Hash` returns the values of xxHash 0.8.

### Bounds

| Quantity | Bound |
|---|---|
| Size of a `Bloom` | 32 bytes per block, ⌈keys * bitsPerKey / 256⌉ blocks, at most 2^32 blocks |
| False positives of a `Bloom` | 1% at 10.5 bits per key, 0.1% at 16.9, 0.01% at 26.4 |
| Memory reads of a `Bloom` query | One block of 32 bytes |
| Size of a `Fuse` | (S + 2) * 2^e * bits / 8 + 18 bytes. The fingerprints tend to 1.125n for large n |
| False positives of a `Fuse` | 2^-bits: 6.25% at 4 bits, 0.39% at 8, 0.098% at 10, 0.024% at 12, 0.0015% at 16 |
| Memory reads of a `Fuse` query | Three 32-bit words, issued at once |
| Scratch of a `FuseBuilder` | About 34 bytes per hash: a copy of the hashes, the mixed hashes in their order of removal, and per position its count, the exclusive or of its hashes, its place in the queue and its fingerprint |
| Attempts of a build | At most 1,024 seeds |

### Failure handling

| Condition | Function | Result |
|---|---|---|
| A bitsPerKey that is NaN, at most 0 or above 256 | `NewBloom` | `ErrBitsPerKey`, nil filter |
| More than 2^32 blocks | `NewBloom` | `ErrTooLarge`, nil filter |
| A width other than 4, 8, 10, 12 or 16 bits | `FuseBuilder.Append` | `ErrFingerprintBits`, dst unchanged |
| More hashes than fit 2^32 fingerprints | `FuseBuilder.Append` | `ErrTooLarge`, dst unchanged |
| No seed of 1,024 yields a filter | `FuseBuilder.Append` | `ErrBuild`, dst unchanged |
| A malformed or truncated encoding | `ParseFuse` | `ErrCorrupt` |
| No hashes | `FuseBuilder.Append` | The encoding of an empty filter |

### Allocation contract

| Call | Objects |
|---|---|
| `NewBloom` | The filter and its blocks |
| `Bloom.Add`, `MayContain`, `MayContainBatch`, `Reset`, `Size` | 0 |
| `FuseBuilder.Append` | 0 when dst has room for the encoding and the builder built at least as many hashes before. Otherwise the growth of dst and of the scratch |
| `ParseFuse`, `Fuse.MayContain`, `Fuse.Bits` | 0 |
| `xxh3.Hash`, `xxh3.HashString` | 0 |

### Tests

| # | Guarantee |
|---|---|
| 1 | A `Bloom` and a `Fuse` report true for every hash of generated sets, including sets with duplicates, a set of one hash and the empty set |
| 2 | The false-positive rate over 2^20 absent hashes lies within a binomial band around 1.25% for a `Bloom` of 10 bits per key, and around 2^-bits for each fuse width |
| 3 | `MayContainBatch` returns what `MayContain` returns for every length of hs and out |
| 4 | Concurrent histories of `Add` and `MayContain` are linearizable, under the race detector, against a spec in which `MayContain` may report true for any hash and reports false only for a hash that no `Add` has added |
| 5 | Golden files pin the encoding of fuse filters of fixed sequences of hashes for each width, on every platform that CI runs |
| 6 | `ParseFuse` returns `ErrCorrupt` for every truncation of an encoding, for a change of the version, the width, the exponent or the segment count that the length does not match, and for a padding byte that is not zero. A changed seed parses, because the encoding has no checksum. A fuzz target checks that `ParseFuse` and `MayContain` never panic |
| 7 | A test regenerates the tables of the builder from the formulas |
| 8 | `xxh3.Hash` returns every value of `XSUM_XXH3_testdata` of xxHash v0.8.4 with seed 0, 4,161 inputs of 0 to 4,160 bytes of the buffer that its sanity test generates |
| 9 | `HashString(s)` equals `Hash([]byte(s))` over generated strings |
| 10 | `TestBloomAllocs`, `TestFuseAllocs`, `TestHashAllocs` and the benchmarks state the counts of the allocation contract |
| 11 | The suites cover every statement, and dokimi-mutate-go detects every mutant of both packages that an annotation does not suppress |

## Alternatives considered

### A. 512-bit blocks with k from the bits per key

The mutable filter would be the cache-line blocked Bloom filter of RocksDB
and Pebble.

**Why not:** a query without a branch takes 5.3-5.4 ns against 4.7 ns, and
its atomic insert took 38.8-39.6 ns against 10.6 ns. It measured
0.96-0.97% false positives at 10 bits per key against 1.25-1.28%, which
saves about 5% of the space at 1%. A consumer whose filter memory dominates
at 8 to 12 bits per key reverses this.

### B. Register-blocked filters

**Why not:** a query takes 3.0-3.1 ns in cache, but the filter measured
1.87-1.90% false positives at 10 bits per key against 1.25-1.28% for the
split block filter. We did not measure how many bits per key it needs for
the same rate.

### C. A standard Bloom filter

**Why not:** a query reads up to seven cache lines, and took 37.3-45.8 ns
in DRAM against 33.0-34.1 ns for the split block filter.

### D. Cuckoo and quotient filters

**Why not:** no caller of this proposal deletes a key from a filter, and
deletion is what these filters add. For a static set, a cuckoo filter with
12-bit fingerprints is 40.3% above the lower bound against 13% for the
binary fuse filter, and Lang et al. find Bloom filters faster at high
throughput. A consumer that deletes keys from a filter reverses this.

### E. Ribbon filters

**Why not:** a standard Ribbon filter is 6-8% above the lower bound against
13%. In Dillinger and Walzer's measurements its query took 58 ns against
15 ns for an xor filter, and RocksDB states 3-4 times the CPU of Bloom
filters. A consumer whose filter memory dominates and whose builds run off
the critical path reverses this.

### F. 4-wise binary fuse filters

**Why not:** they are 7.5% above the lower bound, but a query reads four
words, and Graf and Lemire measured them slightly slower than 3-wise
filters.

### G. Xor filters

**Why not:** they are 23% above the lower bound against 13%, and their
construction took 38.7-39.8 ns per key against 26.0-27.0 ns.

### H. SIMD queries

**Why not:** `simd/archsimd` exists only under `GOEXPERIMENT=simd`. The
package leaving the experiment reverses this, for a query of 1.84-2.08 ns
in cache.

### I. A persisted `Bloom` in Parquet's encoding

**Why not:** a store persists the filter of a complete set, and a `Fuse` of
that set is smaller. A `Fuse` of 8-bit fingerprints takes 9.0 bits per key
for 0.39%, where a `Bloom` takes 10.5 bits per key for 1%. A consumer that
writes filters for Parquet readers reverses this, and needs XXH64 with seed
0 as well.

### J. Filters that hash their keys

**Why not:** an in-process filter hashes with `hash/maphash`, whose seed
prevents chosen collisions, and a persisted filter hashes with
`xxh3.Hash`. A filter that took keys would choose for the caller.

### K. FNV-1a or XXH64 as the stable hash

**Why not:** FNV-1a 64 took 28.7-28.9 ns for 64 bytes and XXH64 7.3-7.9 ns
in its port, against 2.8 ns for the XXH3 port.

### L. Import zeebo/xxh3 or FastFilter/xorfilter

**Why not:** production code imports only the standard library, core and
golang.org/x modules without requirements, and xorfilter imports `unsafe`.

## Drawbacks

- The module gains two packages: `filter` with two filter types, a builder,
  a parse function and five sentinels, and `hash/xxh3` with two functions.
- The encoding of a `Fuse` is frozen at its first release. A change of the
  layout is a version 2 that readers of version 1 keep reading.
- A `Bloom` of 10 bits per key has 1.25-1.28% false positives where a filter
  of 512-bit blocks has 0.96-0.97%.
- An atomic insert costs 10.6 ns where a plain insert costs 6.8-6.9 ns.
- A `FuseBuilder` keeps about 34 bytes of scratch per hash after its
  largest build.
- `hash/xxh3` implements the long-input path of XXH3, with its secret of
  192 bytes, and core maintains it.

## Open questions

None.

## Unresolved / future work

None.

## References

- H. Lang, T. Neumann, A. Kemper and P. Boncz, "Performance-Optimal
  Filtering: Bloom Overtakes Cuckoo at High Throughput", PVLDB 12(5):
  502-515, 2019, <https://www.vldb.org/pvldb/vol12/p502-lang.pdf>.
- T. Mueller Graf and D. Lemire, "Binary Fuse Filters: Fast and Smaller
  Than Xor Filters", <https://arxiv.org/abs/2201.01174>.
- P. C. Dillinger and S. Walzer, "Ribbon filter: practically smaller than
  Bloom and Xor", <https://arxiv.org/abs/2103.02515>.
- Apache Parquet, "Parquet Bloom Filter",
  <https://github.com/apache/parquet-format/blob/master/BloomFilter.md>.
- RocksDB wiki, "RocksDB Bloom Filter",
  <https://github.com/facebook/rocksdb/wiki/RocksDB-Bloom-Filter>, and
  `util/dynamic_bloom.h`.
- cockroachdb/pebble, `sstable/tablefilters/binaryfuse` and
  `sstable/tablefilters/bloom/simulation.md` at a7b8321, and `options.go`
  at 652a081db94b.
- FastFilter/xorfilter v0.5.1, `binaryfusefilter.go`.
- xxHash v0.8.4, `doc/xxhash_spec.md` and
  `tests/sanity_test_vectors.h`, <https://github.com/Cyan4973/xxHash>.
- Go 1.27.2, `src/math/log_amd64.s`, `src/math/log_asm.go` and
  `src/math/log_stub.go`, and `go doc hash/maphash.Seed`.
- ADR-0016, persisted encodings are frozen before 1.0.
- ADR-0015, dependency-free golang.org/x modules in production code.
