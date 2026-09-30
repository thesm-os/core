// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package uuidtext encodes and decodes the text form that every UUID
// version of RFC 9562 shares: 32 hexadecimal digits in groups of 8, 4, 4,
// 4 and 12, separated by hyphens, as in
// "f81d4fae-7dec-11d0-a765-00a0c91e6bf6".
//
// The UUID packages of [go.thesmos.sh/core/id] implement their Format and
// Parse functions with this package. Each passes its own sentinel errors
// to [Parse].
//
// # Allocation contract
//
// [Parse] does not allocate. [Format] allocates the returned string.
package uuidtext
