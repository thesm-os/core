// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
	"bytes"
	"cmp"
	"fmt"
	"sync"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/note"
)

// Log is a log that the witness accepts. [ServerConfig.Logs] returns it
// for an origin.
//
// A deployment whose logs share their keys and hasher returns one Log
// value for their origins. The server then verifies each line of a note
// over many of their checkpoints once.
type Log struct {
	// Hasher is the hash of the log's tree, which its consistency proofs
	// use. A C2SP log uses SHA-256.
	Hasher crypto.Hasher

	// Keys are the key sets of the log. A checkpoint needs one set that
	// counts. The sets are alternatives, such as the keys before and after
	// a rotation, and each set has a threshold of its own.
	Keys []KeySet
}

// KeySet is one key set of a [Log]: its parties, and the number of them
// whose signatures a checkpoint needs.
//
// A party counts when every one of its keys has a valid signature line. A
// party has one key for a log that signs with one algorithm, and one key
// per algorithm for a hybrid log, so a hybrid party counts only with both
// algorithms. A log whose checkpoints need the signatures of any k of n
// custodians lists the n custodians as parties, with the Threshold k.
//
// Advance and the add-checkpoint handler refuse a set that lists one
// public key twice, because the key would count for two parties. Two sets
// can list one public key.
type KeySet struct {
	// Parties are the parties of the set, each with its keys.
	Parties [][]note.Key

	// Threshold is the number of Parties that must count, from 1 to their
	// number. Zero requires every party.
	Threshold int
}

// check returns nil for a Log with a hasher and at least one key set,
// whose sets each have a party, a Threshold from 0 to the number of their
// parties, and parties of Valid keys, with no public key twice in one set.
//
// Returns an error that wraps [ErrConfig] for any other Log.
//
// # Allocation contract
//
// Zero-alloc for a Log that it accepts. It compares the public keys of
// each set in pairs.
func (l *Log) check() error {
	if l.Hasher == nil || len(l.Keys) == 0 {
		return fmt.Errorf("%w: a log needs a hasher and a key set", ErrConfig)
	}

	for _, set := range l.Keys {
		if len(set.Parties) == 0 || set.Threshold < 0 || set.Threshold > len(set.Parties) {
			return fmt.Errorf("%w: a key set needs a party, and a threshold from 0 to the number of its parties",
				ErrConfig)
		}

		for i, party := range set.Parties {
			if len(party) == 0 {
				return fmt.Errorf("%w: a party of a key set without a key", ErrConfig)
			}

			for j, k := range party {
				if !k.Valid() {
					return fmt.Errorf("%w: a key of the log %s is not valid", ErrConfig, k.Name)
				}

				if listed(set.Parties, i, j, k) {
					return fmt.Errorf("%w: a key set lists the public key of %s twice", ErrConfig, k.Name)
				}
			}
		}
	}

	return nil
}

// listed reports whether a key of the parties before position i of
// parties, or a key before position j of the party at position i, has the
// public key of k.
func listed(parties [][]note.Key, i, j int, k note.Key) bool {
	for p, party := range parties[:i+1] {
		if p == i {
			party = party[:j]
		}

		for _, o := range party {
			if bytes.Equal(o.PublicKey, k.PublicKey) {
				return true
			}
		}
	}

	return false
}

// lineState is the result of the verification of one signature line: the
// Verifier that verified it, and whether it verified. A call keeps one per
// line of its note, so that it verifies each line once for the logs of all
// its updates.
type lineState struct {
	// verifier verified the line, and is nil before a verification.
	verifier note.Verifier

	// valid reports whether the line verified.
	valid bool
}

// keyring builds the Verifier of each key of the logs once, and keeps it
// while calls ask for it.
//
// The Server resets the keyring each time its state takes a snapshot. A
// reset drops each Verifier that no call asked for since the reset
// before, so the keyring keeps the keys that the calls of the last two
// snapshot intervals verified. A deployment that names the keys of each
// origin after the origin then keeps the keys of its active origins, and
// not the keys of every origin that the Server verified.
//
// # Concurrency
//
// Safe for concurrent use: a mutex guards the [note.Keyring].
type keyring struct {
	// resolver is the Resolver of the configuration, which each reset
	// passes to ring.
	resolver note.Resolver

	// ring keeps the Verifiers.
	ring note.Keyring

	// mu guards ring.
	mu sync.Mutex
}

// reset starts a new load of the ring, which drops each Verifier that no
// call asked for since the reset before.
//
// # Allocation contract
//
// Zero-alloc.
func (r *keyring) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.ring.Reset(r.resolver)
}

// verifier returns the Verifier of k, which the keyring builds once and
// keeps while calls ask for it.
//
// Returns the errors of [note.Keyring.Verifier].
func (r *keyring) verifier(k note.Key) (note.Verifier, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	v, err := r.ring.Verifier(k)
	if err != nil {
		return nil, fmt.Errorf("witness: resolve the key of %s: %w", k.Name, err)
	}

	return v, nil
}

// verifyLog returns nil when the signature lines of n satisfy l: every line
// whose key name and key ID equal those of a key of l verifies over the
// text of n, and one key set of l has its threshold of parties that count.
// A party counts when every one of its keys has such a line. signed-note
// requires the verification of every line of a known key, so verifyLog
// verifies them all before it decides.
//
// states contains the result of each line of n, at the index of the line.
// verifyLog reuses a result that the same Verifier made, and records each
// result that it makes.
//
// Returns an error that wraps [ErrSignature] for an invalid line of a key
// of l, and for a note without the threshold of parties of one key set.
// Returns the error of the keyring for a key that the Resolver does not
// resolve.
//
// # Allocation contract
//
// Zero-alloc for a note whose lines the keyring's Verifiers verify without
// an allocation, apart from the errors.
func (r *keyring) verifyLog(n *note.Note, l *Log, states []lineState) error {
	satisfied := false

	for _, set := range l.Keys {
		counted := 0

		for _, party := range set.Parties {
			all := true

			for _, k := range party {
				found, err := r.verifyKey(n, k, states)
				if err != nil {
					return err
				}

				all = all && found
			}

			if all {
				counted++
			}
		}

		satisfied = satisfied || counted >= cmp.Or(set.Threshold, len(set.Parties))
	}

	if !satisfied {
		return fmt.Errorf("%w: no key set of the log with the threshold of its parties", ErrSignature)
	}

	return nil
}

// verifyKey verifies every line of n whose key name and key ID are those
// of k, as verifyLog describes, and reports whether n has one.
//
// Returns an error that wraps [ErrSignature] for an invalid line, and the
// error of the keyring.
func (r *keyring) verifyKey(n *note.Note, k note.Key, states []lineState) (bool, error) {
	id := k.ID()
	found := false

	for i := range n.Signatures {
		line := &n.Signatures[i]
		if line.Name != k.Name || line.ID != id {
			continue
		}

		v, err := r.verifier(k)
		if err != nil {
			return false, err
		}

		if states[i].verifier != v {
			states[i] = lineState{verifier: v, valid: v.Verify(n.Text, line.Value)}
		}

		if !states[i].valid {
			return false, fmt.Errorf("%w: an invalid line of %s", ErrSignature, k.Name)
		}

		found = true
	}

	return found, nil
}
