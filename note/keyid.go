// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package note

import (
	"crypto/sha256"
	"encoding/binary"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/sign"
)

// hashChunk is the size of the buffer on the stack through which [KeyID]
// and [Key.ID] copy a string to a hash.
const hashChunk = 64

// Algorithm is the algorithm that every note [Verifier] reports, and that
// every [sign.Signature] built from a signature line carries. A signature
// line names its key and not its algorithm: the type of the key selects
// the algorithm, and the key ID commits to the type. A [sign.Policy] of
// note keys therefore compares equal algorithms for every line, and a
// stored signature of a note names the format that a note [Resolver]
// rebuilds its key under.
const Algorithm crypto.Algorithm = "signed-note"

// KeyID returns the [sign.KeyID] of the note key with the given name and
// key ID: the key ID in four big-endian bytes, followed by the first 12
// bytes of SHA-256(name).
//
// signed-note identifies a key by its name and its key ID, and a
// signature line names its key by the same pair, so a line converts to a
// [sign.Signature] without a table of keys. Two keys with one name and
// one key ID have one KeyID, and [sign.NewPolicyTree] refuses them, as
// golang.org/x/mod refuses an ambiguous key. The derivation is a persisted
// encoding.
//
// # Allocation contract
//
// Zero-alloc for a name of any length. It copies the name to the hash
// through a buffer on the stack, as [Key.ID] does.
func KeyID(name Name, id uint32) sign.KeyID {
	h := sha256.New()

	var chunk [hashChunk]byte

	for s := string(name); s != ""; {
		n := copy(chunk[:], s)
		h.Write(chunk[:n])
		s = s[n:]
	}

	var kid sign.KeyID
	binary.BigEndian.PutUint32(kid[:], id)

	var sum [sha256.Size]byte
	copy(kid[keyIDSize:], h.Sum(sum[:0]))

	return kid
}
