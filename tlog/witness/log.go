// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
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

	// Keys are the key sets of the log. A checkpoint needs a valid
	// signature of every key of one set. A set contains one key for a log
	// that signs with one algorithm, and one key per algorithm for a
	// hybrid log. The sets are alternatives, such as the keys before and
	// after a rotation.
	Keys [][]note.Key
}

// check returns nil for a Log with a hasher and at least one key set,
// whose sets each contain at least one Valid key.
//
// Returns an error that wraps [ErrConfig] for any other Log.
func (l *Log) check() error {
	if l.Hasher == nil || len(l.Keys) == 0 {
		return fmt.Errorf("%w: a log needs a hasher and a key set", ErrConfig)
	}

	for _, set := range l.Keys {
		if len(set) == 0 {
			return fmt.Errorf("%w: a key set of a log without a key", ErrConfig)
		}

		for _, k := range set {
			if !k.Valid() {
				return fmt.Errorf("%w: a key of the log %s is not valid", ErrConfig, k.Name)
			}
		}
	}

	return nil
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
// for the life of the Server.
//
// # Concurrency
//
// Safe for concurrent use: a mutex guards the [note.Keyring].
type keyring struct {
	// ring keeps the Verifiers.
	ring note.Keyring

	// mu guards ring.
	mu sync.Mutex
}

// verifier returns the Verifier of k, which the keyring builds once.
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
// text of n, and every key of at least one set of l has such a line.
// signed-note requires the verification of every line of a known key, so
// verifyLog verifies them all before it decides.
//
// states contains the result of each line of n, at the index of the line.
// verifyLog reuses a result that the same Verifier made, and records each
// result that it makes.
//
// Returns an error that wraps [ErrSignature] for an invalid line of a key
// of l, and for a note without a valid line of every key of one set.
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
		all := true

		for _, k := range set {
			found, err := r.verifyKey(n, k, states)
			if err != nil {
				return err
			}

			all = all && found
		}

		satisfied = satisfied || all
	}

	if !satisfied {
		return fmt.Errorf("%w: no valid line of every key of one key set of the log", ErrSignature)
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
