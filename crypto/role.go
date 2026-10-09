// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package crypto

// roleArityBit is the bit of a [Role] the mechanism reserves to
// encode arity. It is deliberately the high bit: a protocol
// enumerating roles from 0x01 upward stays inside the unary half
// until it has assigned 127 of them, so the reservation costs
// nothing before it is noticed.
const roleArityBit = 0x80

// Role is a one-byte domain separator distinguishing the roles a
// digest plays inside one protocol's commitment structures — a leaf
// from an interior node, a chain link from a tree node.
//
// Without a separator, hashing bytes and combining two digests are
// the same function over different arities: a caller-chosen payload
// of exactly two digest widths hashes to a legitimate interior node.
// Anyone holding two sibling digests from an honest proof can then
// present their concatenation as a leaf, serve a shortened
// authentication path, and have an entry that was never written
// verify. Prefixing both operations with a distinct byte closes that
// class, as certificate transparency closed it for Merkle trees.
//
// # Arity is reserved
//
// The high bit belongs to the mechanism, not to the protocol. Roles
// 0x00–0x7F are unary and accepted only by [Hasher.HashTagged];
// roles 0x80–0xFF are binary and accepted only by
// [Hasher.CombineTagged]; each method panics on the other half, so a
// leaf role and a node role cannot share a first byte.
//
// The reservation is what makes the separation structural. A
// caller-assigned prefix alone would not: HashTagged(r, left ‖
// right) and CombineTagged(r, left, right) are the same bytes, so a
// protocol assigning one role to both a leaf and a node would have
// the original forgery back. 128 roles remain per arity, which no
// plausible protocol exhausts.
//
// One rule the mechanism cannot check: a unary role must be applied
// to inputs of one kind. A protocol hashing both caller-supplied
// bytes and a digest under a single unary role hands a caller who
// controls a digest-width payload a preimage of the other
// construction. Arity is a property of the call site and checkable
// there; what a role is applied to is a property of the protocol's
// registry, visible only in its design review.
//
// # Scope
//
// Role separates roles WITHIN a protocol. It does not separate
// protocols from each other — two protocols independently choosing
// 0x01 are not distinguished by it. Cross-protocol separation is
// [Domain] and [Framer], which carry a name and a version for
// exactly that reason; a protocol whose digests may be presented to
// another protocol's verifier needs both.
//
// This package ships no Role constants, as it ships no domain
// constants for [HashDomain]. The vocabulary belongs to the
// protocol: only its designer knows how many roles exist and which
// values are already written down. That count is usually larger than
// it first looks — a hash-chained log with batching needs distinct
// roles for the payload commitment, the record commitment over it,
// the chain's genesis and link forms, a fork into another chain, and
// each tree it feeds — which is why a byte is the width rather than
// a bit.
//
// # Allocation contract
//
// Value type; pass by value. Zero alloc.
type Role uint8

// IsUnary reports whether r is a unary role — one accepted by
// [Hasher.HashTagged] and refused by [Hasher.CombineTagged].
//
// Useful to a protocol validating its own role registry before the
// first digest is written, which is the only moment the assignment
// is still free to change.
//
// # Allocation contract
//
// Zero alloc.
func (r Role) IsUnary() bool { return r&roleArityBit == 0 }

// IsBinary reports whether r is a binary role — one accepted by
// [Hasher.CombineTagged] and refused by [Hasher.HashTagged].
//
// # Allocation contract
//
// Zero alloc.
func (r Role) IsBinary() bool { return r&roleArityBit != 0 }
