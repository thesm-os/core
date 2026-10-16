---
rfc: 0061
title: Block Compression in the Snappy Format
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-10-15
updated: 2026-10-16
discussion: none
supersedes: none
superseded-by: none
produces-adr: tbd
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# RFC-0061: Block Compression in the Snappy Format

## Summary

We propose a package `compress/snappy` that encodes and decodes blocks in
the Snappy block format, in Go without assembly or `unsafe`. A Snappy
decoder in pure Go decompresses a block of 32 KiB in 2.72-2.80 µs, and the
standard library's `compress/flate` takes 76.5-78.2 µs. A store that
compresses its blocks decompresses every block that it reads from disk, so
the standard library has no codec fit for the read path.

## Motivation

### Method of the measurements

A probe outside this module ran each benchmark with Go 1.27.2 on four cores
of an AMD Ryzen 9 9950X3D in a capped scope, three times for 200 ms, not
pinned to CPUs. The blocks had the data-block layout of LevelDB: sorted
16-digit decimal keys with prefix compression and a restart point every 16
keys, and values of 100 bytes whose second half repeats the first 50
random printable bytes. A test checked every round trip before the timing.

### Codecs on blocks of 32 KiB

| Codec | Compression | Ratio | Decompression |
|---|---|---|---|
| `compress/flate`, BestSpeed | 481-493 MB/s | 0.480 | 420-429 MB/s, 76.5-78.2 µs per block, 3 allocations |
| `compress/flate`, HuffmanOnly | 1.04-1.09 GB/s | 0.827 | 261-265 MB/s |
| Snappy, golang/snappy v1.0.0 with assembly | 2.74-2.77 GB/s | 0.604 | 14.5-15.5 GB/s |
| Snappy, golang/snappy v1.0.0 with `-tags noasm` | 1.46-1.48 GB/s | 0.604 | 11.7-12.0 GB/s, 2.72-2.80 µs per block |
| zstd fastest, klauspost/compress v1.20.3 with `-tags noasm` | 829-845 MB/s | 0.478 | 1.35-1.36 GB/s, 24.2-24.3 µs per block |

On blocks of 4 KiB, the pure-Go Snappy decoder ran at 16.0-16.2 GB/s and
`compress/flate` at 379-383 MB/s. golang/snappy v1.0.0 imports `unsafe` in
no file, and its build tag `noasm` selects its Go paths.

### What compression costs a store

A db_bench run of 1M keys in RocksDB measured 356,290 random reads per
second without compression, 342,497 with LZ4 and 176,724 with ZSTD at level
3, with tables of 73.5 MB, 38.3 MB and 32.9 MB (RocksDB PR #14287). The PR
does not state its hardware. A light codec gives back most of the read
throughput, and a heavy codec halves it.

### Prior art

- The Snappy format is "a LZ77-type compressor with a fixed, byte-oriented
  encoding". A block starts with its decompressed length as a varint, and
  continues with literals and copies whose tag byte gives their kind
  (google/snappy, `format_description.txt`).
- LevelDB compresses its blocks with Snappy by default.
- golang/snappy encodes in blocks of 64 KiB, and bounds an encoding of n
  bytes by 32 + n + n/6: a one-byte literal followed by a five-byte copy
  turns 6 bytes of input into 7.

## Detailed design

### Components

| Component | Responsibility |
|---|---|
| `snappy.AppendEncode` | Appends the Snappy block encoding of a source |
| `snappy.AppendDecode` | Appends the bytes that a Snappy block decodes to |
| `snappy.DecodedLen` | Returns the decompressed length that a block states |
| `snappy.MaxEncodedLen` | Returns the bound on the length of an encoding |
| `snappy.ErrTooLarge`, `snappy.ErrCorrupt` | The errors of a source beyond the format's limit and of a malformed block |

`compress/snappy` imports `go.thesmos.sh/core/errs` and the standard
library: `encoding/binary`, `errors`, `math`, `math/bits` and `slices`.

### API

```go
// MaxBlockLen is the largest length of the bytes that one block encodes,
// 2^32 - 1, the largest length that the preamble of the format states.
const MaxBlockLen = 1<<32 - 1

// MaxEncodedLen returns the largest length of the encoding of n bytes,
// 32 + n + n/6.
//
// Error modes:
//
//   - An n that is negative or above MaxBlockLen returns ErrTooLarge.
//   - A bound above the largest int returns ErrTooLarge.
func MaxEncodedLen(n int) (int, error)

// AppendEncode appends the Snappy block encoding of src to dst and returns
// the extended slice. dst and src must not overlap.
//
// Error modes:
//
//   - A src longer than MaxBlockLen returns ErrTooLarge, and dst unchanged.
func AppendEncode(dst, src []byte) ([]byte, error)

// DecodedLen returns the length of the bytes that the block src decodes to.
//
// Error modes:
//
//   - A preamble that is not a varint of at most MaxBlockLen returns
//     ErrCorrupt.
//   - A length above the largest int returns ErrTooLarge.
func DecodedLen(src []byte) (int, error)

// AppendDecode appends the bytes that the block src decodes to, to dst,
// and returns the extended slice. dst and src must not overlap.
//
// Error modes:
//
//   - A malformed block returns ErrCorrupt, and dst unchanged.
//   - A decoded length above the largest int returns ErrTooLarge.
func AppendDecode(dst, src []byte) ([]byte, error)

var (
	// ErrTooLarge classifies as errs.Invalid.
	ErrTooLarge = errs.WithClass(errors.New("snappy: block too large"), errs.Invalid)

	// ErrCorrupt classifies as errs.Integrity.
	ErrCorrupt = errs.WithClass(errors.New("snappy: corrupt block"), errs.Integrity)
)
```

The package implements the block format only. The Snappy framing format
of streams, with its chunks and checksums, is out.

### The format

A block is a varint of the decompressed length, at most 2^32 - 1, followed
by elements. The low two bits of the tag byte of an element are its kind:

| Low bits | Element | Length | Offset |
|---|---|---|---|
| 00 | Literal | (tag >> 2) + 1, from 1 to 60, when tag >> 2 is below 60. When tag >> 2 is 60 to 63, the length minus one follows in 1 to 4 little-endian bytes | — |
| 01 | Copy, 1-byte offset | ((tag >> 2) & 7) + 4, from 4 to 11 | 11 bits: the top 3 bits of the tag, then one byte |
| 10 | Copy, 2-byte offset | (tag >> 2) + 1, from 1 to 64 | A little-endian 16-bit integer |
| 11 | Copy, 4-byte offset | (tag >> 2) + 1, from 1 to 64 | A little-endian 32-bit integer |

A copy repeats the bytes at the offset before the current end of the
output. A length above the offset repeats a run, as in LZ77.

### The encoder

- The encoder grows dst once, to room for `MaxEncodedLen(len(src))` more
  bytes, writes the preamble and encodes src in blocks of 64 KiB.
- In each block it hashes each 4-byte sequence into a table of up to 2^14
  positions. When the sequence at the recorded position matches, it extends
  the match 8 bytes at a time and emits a copy. Otherwise it records the
  position and moves on.
- In a stretch without a match, the encoder steps over more positions
  after every 32 misses, so an incompressible block encodes at memory
  speed.
- The table is an array of `uint16` in the encoder's frame, so the encoder
  allocates only the growth of dst.
- The encoder emits only literals and copies with 1-byte and 2-byte
  offsets, because every offset inside a block of 64 KiB fits 2 bytes. The
  decoder accepts every kind.
- The encoding of n bytes is at most `MaxEncodedLen(n)`.
- For every source, the encoder writes the same bytes as golang/snappy
  v1.0.0. Golden files of golang/snappy's encodings pin these bytes.

### The decoder

- The decoder reads the preamble, grows dst once to room for the stated
  length, and decodes into the new bytes.
- It checks every element against the bounds of src and of the stated
  length before it copies. A copy with an offset of 0 or beyond the bytes
  decoded so far, a literal or a copy past the stated length, a truncated
  element, and an output shorter than the stated length each return
  `ErrCorrupt`, with dst at its previous length.
- While dst and src have room, a literal of up to 16 bytes moves as one
  move of 16 bytes. A literal of up to 64 bytes moves as one move of 64
  bytes. A copy of up to 16 bytes whose offset is at least its length moves
  as one move of 16 bytes. A move can write past the element. The elements
  after it write those bytes again.
- Every other element is one `copy` call. A copy whose offset is shorter
  than its length repeats the run byte by byte, because each byte depends on
  the byte that the copy wrote before it.

### Invariants

- `AppendDecode(nil, AppendEncode(nil, src))` returns src for every src of
  at most `MaxBlockLen` bytes.
- `len(AppendEncode(nil, src))` is at most `MaxEncodedLen(len(src))`.
- `AppendDecode` returns `ErrCorrupt` and leaves dst at its length for every
  block that the format does not allow, and it never panics.
- `DecodedLen(b)` equals the length of the bytes that b decodes to, for
  every block b that decodes.

### Bounds

| Quantity | Bound |
|---|---|
| Encoding of n bytes | 32 + n + n/6 bytes |
| Memory of the encoder | dst, and a table of 32 KiB in its frame |
| Memory of the decoder | dst only |
| Work | One pass over src and one over the output |

### Failure handling

| Condition | Function | Result |
|---|---|---|
| A src longer than `MaxBlockLen` | `AppendEncode`, `MaxEncodedLen` | `ErrTooLarge` |
| A malformed or truncated block | `AppendDecode`, `DecodedLen` | `ErrCorrupt`, dst unchanged |
| A stated length above the largest int | `AppendDecode`, `DecodedLen` | `ErrTooLarge` |
| An empty src | `AppendEncode` | The one-byte block of length 0 |

### Allocation contract

| Call | Objects |
|---|---|
| `AppendEncode` | 0 when dst has room for `MaxEncodedLen(len(src))` more bytes. Otherwise the growth of dst |
| `AppendDecode` | 0 when dst has room for the decoded length. Otherwise the growth of dst |
| `DecodedLen`, `MaxEncodedLen` | 0 |

### Tests

| # | Guarantee |
|---|---|
| 1 | `AppendDecode` of `AppendEncode` returns the source for generated inputs: empty, random, runs, and lengths around the 64 KiB boundary of a block |
| 2 | The encoding of Isaac Newton's Opticks from golang/snappy's test data decodes to its text. `AppendEncode` writes golang/snappy's bytes for the text and for six prefixes of 17 to 70,000 bytes of the repeated text |
| 3 | No encoding exceeds `MaxEncodedLen` of its source |
| 4 | Every truncation of the reference encoding, a copy with offset 0 and a copy past the decoded bytes return `ErrCorrupt` and leave dst at its length |
| 5 | A fuzz target checks that `AppendDecode` and `DecodedLen` never panic, and that a block that decodes re-encodes and decodes to the same bytes |
| 6 | `TestEncodeAllocs`, `TestDecodeAllocs` and the benchmarks state the counts of the allocation contract |
| 7 | The benchmarks measure `AppendDecode` and `AppendEncode` on blocks of 4 KiB and 32 KiB in the layout of LevelDB. A probe outside the module compares them with golang/snappy and `compress/flate` |
| 8 | The suites cover every statement, and dokimi-mutate-go detects every mutant that an annotation does not suppress |
| 9 | `AppendDecode` returns the bytes of the elements of generated valid blocks, with every kind of element and every form of the length of a literal |

## Alternatives considered

### A. `compress/flate` from the standard library

**Why not:** it decompresses a block of 32 KiB in 76.5-78.2 µs against
2.72-2.80 µs, and it allocates three objects for each block. Its ratio of
0.480 against 0.604 saves a fifth of the space.

### B. zstd

**Why not:** a pure-Go decoder took 24.2-24.3 µs for a block of 32 KiB,
nine times as long as Snappy's, for a ratio of 0.478. A zstd codec also
needs its entropy coders, so core would maintain a much larger package. A
consumer that needs the ratio on its read path reverses this.

### C. The LZ4 block format

**Why not:** the probe did not measure an LZ4 decoder in pure Go, and
LevelDB reads Snappy by default. In the RocksDB run, LZ4 gave 342,497
random reads per second against 356,290 without compression. A pure-Go LZ4
decoder that measures faster than Snappy on the same blocks reverses this.

### D. Import golang/snappy

**Why not:** production code imports only the standard library, core and
golang.org/x modules without requirements.

### E. The framing format

**Why not:** a sorted table stores each block with its own length and
checksum, so the chunks and checksums of the framing format would repeat
them.

### F. The search of s2

The Snappy-compatible encoder of s2 (klauspost/compress v1.20.3) hashes 6
bytes, probes three positions per load and tries the offset of the previous
copy first. In pure Go it encoded the blocks of 32 KiB 1.8x faster than
golang/snappy's encoder. Its blocks were 1.0-1.7% smaller.

**Why not:** its bytes differ from the bytes of golang/snappy. With its
search, a block that core writes would differ from the block that
golang/snappy writes for the same source. We choose identical bytes over
the speed. The format of s2 itself extends Snappy, and a Snappy decoder
does not read its blocks.

## Drawbacks

- The module gains a package with four functions, a constant and two
  sentinels, and core maintains a codec.
- Snappy's ratio of 0.604 on these blocks is worse than 0.480 for
  `compress/flate` and 0.478 for zstd, so a store keeps about a quarter
  more bytes on disk than with either.
- Core's encoder compresses blocks of 32 KiB at 1.99-2.04 GB/s on one CPU.
  golang/snappy's assembly compresses the same blocks at 3.02-3.09 GB/s.

## Open questions

None.

## Unresolved / future work

None.

## References

- google/snappy, `format_description.txt`, last revised 2011-10-05,
  <https://github.com/google/snappy/blob/main/format_description.txt>.
- golang/snappy v1.0.0, `encode.go`, `decode.go` and
  `testdata/Isaac.Newton-Opticks.txt.rawsnappy`,
  <https://github.com/golang/snappy>.
- google/leveldb, `include/leveldb/options.h`, the default compression.
- facebook/rocksdb PR #14287, "Separate keys and values in data blocks",
  <https://github.com/facebook/rocksdb/pull/14287>.
- klauspost/compress v1.20.3, `s2/encode_all.go`, `encodeBlockSnappyGo64K`,
  <https://github.com/klauspost/compress>.
- ADR-0015, dependency-free golang.org/x modules in production code.
