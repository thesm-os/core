// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

// Package filter implements approximate-membership filters over 64-bit
// hashes: a split block Bloom filter that grows by single additions, and a
// binary fuse filter that is built once from a complete set.
//
// A filter reports whether a set may contain a hash. It reports true for
// every hash of the set, and for a hash outside the set at a rate that its
// size sets. A store queries a filter before it reads a block from disk, so a
// key that the store does not have costs no read. The caller hashes its keys:
// an in-process filter takes hashes of hash/maphash, whose seed makes chosen
// collisions hard, and a persisted filter takes hashes of
// [go.thesmos.sh/core/hash/xxh3], whose values do not change between
// processes.
//
// # Filters
//
//   - [Bloom] is the split block Bloom filter of Parquet. A hash sets one bit
//     in each of the eight 32-bit words of one block of 256 bits, so a query
//     reads one block. Goroutines can add and query concurrently, and
//     [Bloom.MayContainBatch] overlaps the cache misses of 16 hashes.
//   - [Fuse] is a 3-wise binary fuse filter. At 8 bits per fingerprint it
//     takes about 9 bits per hash for 0.39% false positives, where a Bloom
//     filter takes about 14 bits per key for the same rate. [FuseBuilder]
//     builds its encoding, and [ParseFuse] queries the encoding in place.
//
// # Encoding of a fuse filter
//
// The encoding of a Fuse does not change between releases. Its integers are
// big-endian:
//
//   - byte 0: the version, 1
//   - byte 1: the width of a fingerprint in bits: 4, 8, 10, 12 or 16
//   - byte 2: the segment exponent e, from 2 to 18
//   - bytes 3 to 6: the segment count S
//   - bytes 7 to 14: the seed
//   - then (S + 2) * 2^e fingerprints, or none when S is 0, packed from the
//     most significant bit of the first byte
//   - then three zero bytes
//
// A query mixes a hash h with the seed into x, the finalizer of MurmurHash3
// of h + seed. Its fingerprint is the low bits of x ^ x >> 32, and its three
// positions are p0, the high 64 bits of x * (S * 2^e), then
// (p0 + 2^e) ^ ((x >> 18) & (2^e - 1)) and (p0 + 2 * 2^e) ^ (x & (2^e - 1)).
// The query reports true when the exclusive or of the fingerprints at the
// three positions equals the fingerprint of x.
//
// # Errors
//
// [NewBloom] returns [ErrBitsPerKey] and [ErrTooLarge].
// [FuseBuilder.Append] returns [ErrFingerprintBits], ErrTooLarge and
// [ErrBuild]. [ParseFuse] returns [ErrCorrupt].
//
// # Concurrency
//
// The methods of a Bloom other than [Bloom.Reset] are safe for concurrent
// use. A Fuse is safe for concurrent use while its encoding does not change.
// A FuseBuilder is not safe for concurrent use.
//
// # Allocation contract
//
//   - NewBloom allocates the filter and its blocks. The methods of a Bloom
//     do not allocate.
//   - FuseBuilder.Append allocates nothing when dst has room for the
//     encoding and the builder built at least as many hashes before.
//     [FuseBuilder.Footprint] returns the bytes of its scratch and does not
//     allocate.
//   - ParseFuse and the methods of a Fuse do not allocate.
//
// # Dependency position
//
// Imports encoding/binary, errors, math, math/bits, slices and sync/atomic
// from the standard library, and go.thesmos.sh/core/errs from this module.
package filter
