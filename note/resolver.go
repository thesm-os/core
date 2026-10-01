// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package note

import (
	"bytes"
	"fmt"
)

// Resolver maps a signature type to a function that builds the [Verifier]
// of a key of that type. Each entry combines a message format, such as
// [Text], with the [sign.Resolver] entry of an algorithm:
//
//	r := note.Resolver{
//		note.TypeEd25519: note.Text(ed25519.Resolve),
//		pq:               note.Text(mldsa.Resolver(mldsa.MLDSA87, "example.com/checkpoint")),
//	}
//
// The caller builds a Resolver from the types it trusts, as a map
// literal. There is no global registry and there are no default entries,
// so a verifier key selects only a type that the caller listed, and an
// empty Resolver resolves nothing. Apply a Resolver only to keys that the
// caller trusts, such as the keys of its policy file, and never to a key
// that a note contains.
//
// # Concurrency
//
// Safe for concurrent use once built, as for any map that is only read.
type Resolver map[Type]func(Key) (Verifier, error)

// Verifier returns the [Verifier] of k, which the entry of k.Type builds.
//
// Returns [ErrUnknownType], classified [errs.Unsupported], when r has no
// entry for k.Type, when the entry returns neither a Verifier nor an
// error, and when the entry builds a Verifier for another key: one whose
// Key is not k, whose KeyID is not [KeyID](k.Name, k.ID()), whose
// PublicKey is not k.PublicKey, or whose Algorithm is not [Algorithm]. The
// last two are a Resolver whose table maps a type to the wrong entry.
// Returns the error of the entry for a key that the entry refuses.
//
// # Allocation contract
//
// What the entry allocates, and the error of a key that it refuses. A
// [Keyring] builds the Verifier of a key once.
func (r Resolver) Verifier(k Key) (Verifier, error) {
	entry := r[k.Type]
	if entry == nil {
		return nil, fmt.Errorf("%w: no entry for %s", ErrUnknownType, k.Type)
	}

	v, err := entry(k)
	if err != nil {
		return nil, err
	}

	if v == nil {
		return nil, fmt.Errorf("%w: the entry for %s built no Verifier", ErrUnknownType, k.Type)
	}

	if !sameKey(v.Key(), k) || v.KeyID() != KeyID(k.Name, k.ID()) || !bytes.Equal(v.PublicKey(), k.PublicKey) ||
		v.Algorithm() != Algorithm {

		return nil, fmt.Errorf("%w: the entry for %s built a Verifier of another key", ErrUnknownType, k.Type)
	}

	return v, nil
}

// sameKey reports whether a and b have the same name, type and public key.
func sameKey(a, b Key) bool {
	return a.Name == b.Name && a.Type == b.Type && bytes.Equal(a.PublicKey, b.PublicKey)
}
