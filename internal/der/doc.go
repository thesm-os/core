// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package der reads and writes the Distinguished Encoding Rules of ASN.1,
// ITU-T X.690, for the formats of this module that use them, without an
// allocation.
//
// A [Reader] reads the elements of an encoding in sequence and returns
// their contents as slices of the input, so a parser of a time-stamp token
// or a certificate walks the bytes that it received and copies nothing. A
// [Builder] appends elements to a byte slice of the caller, and writes the
// length of a constructed element when the caller closes it.
//
// # Strictness
//
// The Reader accepts DER and no other encoding of BER: a tag in one byte,
// a definite length in its shortest form, and a length within the input.
// The value decoders, such as [Uint64] and [GeneralizedTime], accept only
// the one DER encoding of each value. A parser therefore accepts exactly
// one encoding of each structure, and a signature over the bytes covers
// the value that the parser returns.
//
// # Allocation contract
//
// Reading, decoding and appending into a slice with room do not allocate.
// The Builder grows its slice with append when the slice has no room.
//
// # Dependency position
//
// Imports encoding/binary, math/bits and time from the standard library.
// The package is internal to the module.
package der
