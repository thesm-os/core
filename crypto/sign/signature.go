// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package sign

//go:generate go tool kanon -type=Signature -canonical

import "go.thesmos.sh/core/crypto"

// Signature is one signature with the identity of the key that made
// it, as a [Policy] receives it.
//
// # Encoding
//
// kanon generates the codec of Signature, so the record of every
// consumer encodes a signature with the field numbers that this
// package records: Algorithm 1, Value 2 and KeyID 3. A field keeps its
// number, and a new field takes a new number. [Signature.AppendBinary]
// and [Signature.MarshalBinary] write the same encoding.
//
// The decode of the codec accepts only the encoding that the encode
// writes. It returns an error that wraps
// [go.thesmos.sh/kanon.ErrNotCanonical] for any other input that the
// wire format lets a decoder accept, such as two fields in another
// order, a field at its zero value, or a field number that this package
// does not record. A reader built with an earlier version of this
// package rejects a field that a later version adds, so every reader
// upgrades before a writer sets a new field.
//
// The methods of the codec have pointer receivers, so encoding/gob
// encodes a Signature only when it can take its address, as it can for
// the value of a pointer or an element of a slice. gob fails for a
// Signature in a struct that it receives by value, with "gob:
// unaddressable value".
//
// # Allocation contract
//
// The methods of the codec follow the allocation contract of
// [go.thesmos.sh/kanon.Message].
type Signature struct {
	// Algorithm is the algorithm the signature claims. [Policy.Check]
	// counts the signature only when it equals its key's algorithm.
	Algorithm crypto.Algorithm

	// Value is the signature bytes.
	Value []byte

	// KeyID names the key that made the signature.
	KeyID KeyID
}

// Complete reports whether s has an algorithm, a value and a key ID, the
// fields that a verifier reads to check s. A record that persists a
// signature calls it to refuse a signature that no verifier can check.
// Complete does not verify s, and it accepts any algorithm name, because
// a [Policy] resolves the algorithm. A nil s is not complete.
//
// # Allocation contract
//
// Zero alloc.
func (s *Signature) Complete() bool {
	return s != nil && s.Algorithm != "" && len(s.Value) != 0 && s.KeyID != (KeyID{})
}
