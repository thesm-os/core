// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

// Package snappy encodes and decodes blocks in the Snappy block format, in Go
// without assembly and without package unsafe.
//
// A store that compresses its blocks decompresses every block that it reads
// from disk. A block of 32 KiB decodes in about 2 µs. Package compress/flate
// of the standard library takes about 70 µs for the same block. The package
// implements the block format only. It does not implement the framing format
// of streams with its chunks and checksums.
//
// # Format
//
// A block starts with its decoded length, at most [MaxBlockLen], as a
// varint. Elements follow, and the low two bits of the tag byte of an element
// are its kind:
//
//   - 00: a literal of 1 to 60 bytes whose length minus one is in the upper
//     six bits of the tag, or a longer literal whose length minus one is in
//     the next 1 to 4 bytes, little-endian, for the upper bits 60 to 63.
//   - 01: a copy of 4 to 11 bytes at an offset of 11 bits.
//   - 10: a copy of 1 to 64 bytes at a little-endian offset of 16 bits.
//   - 11: a copy of 1 to 64 bytes at a little-endian offset of 32 bits.
//
// A copy repeats the bytes that lie offset bytes before the end of the
// decoded bytes. When its length exceeds its offset, it repeats a run, as in
// LZ77.
//
// # Encoding
//
// [AppendEncode] writes the same bytes as golang/snappy v1.0.0. It writes
// literals and copies with offsets of 1 and 2 bytes, which every decoder of
// the format reads. [MaxEncodedLen] bounds the length of an encoding.
//
// # Decoding
//
// [AppendDecode] checks every element against the block and against the
// decoded length that the preamble states, and returns [ErrCorrupt] for a
// block that the format does not allow. [DecodedLen] reads the preamble
// alone, so a caller can refuse a length before AppendDecode allocates it.
//
// # Concurrency
//
// The functions are safe for concurrent use. They do not keep state between
// calls.
//
// # Allocation contract
//
//   - AppendEncode does not allocate when dst has room for
//     MaxEncodedLen(len(src)) more bytes.
//   - AppendDecode does not allocate when dst has room for the decoded
//     bytes.
//   - DecodedLen and MaxEncodedLen do not allocate.
//
// # Dependency position
//
// Imports encoding/binary, errors, math, math/bits and slices from the
// standard library, and go.thesmos.sh/core/errs from this module.
package snappy
