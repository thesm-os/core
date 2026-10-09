// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

// Package sha256 provides a SHA-256 [crypto.Hasher] implementation
// backed by [crypto/sha256].
//
// The implementation is stateless — the zero-value [Hasher] is
// usable. [New] is provided as a constructor for use sites that
// prefer one. [Hasher.Hash] calls [crypto/sha256.Sum256]
// directly; [Hasher.CombineTagged] writes the role byte and the two
// input digests into a stack-local 65-byte buffer and hashes that;
// [Hasher.HashTagged] streams the role byte ahead of arbitrary-
// length data through a pooled stream.
//
// All methods are zero-allocation on supported platforms.
package sha256
