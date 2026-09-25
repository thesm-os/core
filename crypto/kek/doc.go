// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package kek provides a [crypto.Keeper] over an intermediate
// key-encryption key (KEK): a 256-bit key that a parent Keeper, such as a
// key in a hosted KMS, wraps.
//
// A [Keeper] unwraps its KEK once, through the parent. It then wraps each
// data encryption key (DEK) in process memory, without a call to the
// parent, under a wrapping key derived from the KEK. A caller makes one
// custodian call per KEK instead of one per DEK.
//
// # The stored KEK
//
// The parent wraps a [RecordSize]-byte record: the KEK, then the SHA-256
// digest of its framed key ID. [New] and [NewAAD] refuse a record bound
// to another key ID with [ErrKeyIDMismatch], so a stored record swapped
// with the record of another key ID does not open.
//
// A tenant that can call a parent key it shares with other tenants can
// wrap a record for another tenant's key ID. [GenerateAAD] and [NewAAD]
// pass the key ID to a parent that implements [crypto.AADKeeper], so the
// custodian's access policy can refuse such a record. A parent key per
// tenant gives the same isolation on every custodian. The caller chooses
// between the two. A record from [Generate] opens only with New. A
// record from GenerateAAD opens only with NewAAD.
//
// # Wrapping keys
//
// A Keeper derives a wrapping key with HKDF-SHA-256 from the KEK and a
// random 16-byte salt at its first [Keeper.Wrap], and derives a new one
// after every 2^30 wraps. No wrapping key seals more than 2^30 DEKs. For
// 2^30 random 96-bit AES-GCM nonces, the probability that two are equal
// is below 2^-37. Each derivation reads a new salt, so no other Keeper
// uses the same wrapping key. [Keeper.Unwrap] keeps the ciphers of up to
// 1,024 salts.
//
// # Exposure
//
// The KEK and the current wrapping key are in process memory from the
// constructor until [Keeper.Close]. A disclosure of that memory exposes
// every DEK wrapped under the KEK. The parent protects the KEK only at
// rest.
//
// # Erasure
//
// A caller erases a KEK by deleting every copy of its wrapped record,
// including the copies in backups, and by closing every open Keeper over
// it. A copy in a backup opens for as long as its parent key exists.
// Destroying the parent key through [crypto.Destroyer] erases every KEK
// under it, including the copies in backups.
package kek
