// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package sign

//go:generate go tool kanon -type=Signature

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
