// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

// Package uuidtext encodes and decodes the text form that every UUID
// version of RFC 9562 shares: 32 hexadecimal digits in groups of 8, 4, 4,
// 4 and 12, separated by hyphens, as in
// "f81d4fae-7dec-11d0-a765-00a0c91e6bf6". It also encodes and decodes the
// binary form of a UUID value: its 16 bytes, and no bytes for the zero
// value.
//
// The UUID packages of [go.thesmos.sh/core/id] implement their Format,
// AppendFormat and Parse functions and the methods of their value types
// with this package. Each passes its own sentinel errors to [Parse] and
// [DecodeBinary].
//
// # Allocation contract
//
// [Parse] and [DecodeBinary] do not allocate, and Parse reads a byte slice in
// place. [Format] allocates the returned string. [AppendFormat] and
// [AppendBinary] do not allocate when their buffer has room for the text or
// the bytes.
package uuidtext
