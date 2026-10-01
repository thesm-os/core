// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package note

import (
	"bytes"
	"cmp"
	"slices"
	"strings"
)

// Keyring builds the [Verifier] of each key of a configuration through a
// [Resolver], and keeps the Verifiers for the next load of the
// configuration, so that a configuration that loads again builds no
// Verifier twice.
//
// A load starts with [Keyring.Reset], and [Keyring.Verifier] returns the
// Verifiers of its keys. Verifier returns the Verifier that the Keyring
// built for a key with the same name, type and public key in the load
// before or in the current load, while the Resolver of the load has an
// entry for the type. Otherwise the Resolver of the load builds a new
// one. Reset drops each Verifier that the load before it did not ask for.
// The Keyring assumes that the entry of a type builds the same Verifier
// in every load, so a caller that replaces the entry of a type uses a new
// Keyring.
//
// The zero Keyring is empty and has no Resolver, so its Verifier returns
// [ErrUnknownType] for every key, as an empty Resolver does.
//
// # Concurrency
//
// Not safe for concurrent use. The Verifiers that it returns are.
//
// # Allocation contract
//
// Verifier allocates nothing for a key whose Verifier the Keyring keeps.
// Otherwise it allocates what the Resolver allocates, and the growth of
// the Keyring, which [Keyring.Grow] makes room for. Reset allocates
// nothing.
type Keyring struct {
	// resolver builds the Verifiers of the current load.
	resolver Resolver

	// entries are the Verifiers that the Keyring keeps, sorted by the
	// name, the type and the public key of their keys.
	entries []keyringEntry
}

// keyringEntry is one Verifier of a [Keyring].
type keyringEntry struct {
	// verifier verifies the signatures of the key.
	verifier Verifier

	// loaded reports whether the current load asked for the Verifier.
	loaded bool
}

// Reset starts a load whose Verifiers r builds. It drops each Verifier
// that the load before did not ask for, and keeps the others for the new
// load.
func (k *Keyring) Reset(r Resolver) {
	k.entries = slices.DeleteFunc(k.entries, func(e keyringEntry) bool { return !e.loaded })

	for i := range k.entries {
		k.entries[i].loaded = false
	}

	k.resolver = r
}

// Grow makes room in k for the Verifiers of n more keys, so that a caller
// that knows the number of keys of its configuration loads it with one
// allocation for the Keyring.
func (k *Keyring) Grow(n int) {
	k.entries = slices.Grow(k.entries, n)
}

// Verifier returns the Verifier of key in the current load: the Verifier
// that k keeps for a key with the same name, type and public key when the
// Resolver of the load has an entry for the type, and otherwise a new
// Verifier that the Resolver builds, which k keeps.
//
// Returns the errors of [Resolver.Verifier].
func (k *Keyring) Verifier(key Key) (Verifier, error) {
	i, found := slices.BinarySearchFunc(k.entries, key, func(e keyringEntry, key Key) int {
		return compareKeys(e.verifier.Key(), key)
	})

	if found && k.resolver[key.Type] != nil {
		k.entries[i].loaded = true

		return k.entries[i].verifier, nil
	}

	v, err := k.resolver.Verifier(key)
	if err != nil {
		return nil, err
	}

	k.entries = slices.Insert(k.entries, i, keyringEntry{verifier: v, loaded: true})

	return v, nil
}

// compareKeys orders keys by name, then by type, then by public key.
func compareKeys(a, b Key) int {
	return cmp.Or(
		strings.Compare(string(a.Name), string(b.Name)),
		strings.Compare(string(a.Type), string(b.Type)),
		bytes.Compare(a.PublicKey, b.PublicKey),
	)
}
