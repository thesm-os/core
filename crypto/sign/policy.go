// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package sign

import (
	"fmt"
	"slices"

	"go.thesmos.sh/core/crypto"
)

// Signature is one signature with the identity of the key that made
// it, as a [Policy] receives it.
type Signature struct {
	// Algorithm is the algorithm the signature claims. [Policy.Check]
	// counts the signature only when it equals its key's algorithm.
	Algorithm crypto.Algorithm

	// Value is the signature bytes.
	Value []byte

	// KeyID names the key that made the signature.
	KeyID KeyID
}

// Party is a signer that counts once toward a [Policy]'s threshold. A
// party has one or more keys and counts only when every one of its
// keys has a valid signature. A party with an Ed25519 key and an
// ML-DSA key is a hybrid signer, and counts only when both sign.
type Party struct {
	// Name identifies the party in the caller's diagnostics.
	Name string

	// Keys are the party's keys. Every key must sign for the party to
	// count.
	Keys []Verifier
}

// Policy requires valid signatures from at least a threshold of its
// parties. It expresses a threshold of approvers, a quorum of
// witnesses, and a hybrid signature that needs both a classical and a
// post-quantum key.
//
// Build a Policy with [NewPolicy]. The zero Policy refuses every
// check with [ErrPolicy].
//
// # Concurrency
//
// Immutable and safe for concurrent use.
type Policy struct {
	// index maps each key's KeyID to its position in keys.
	index map[KeyID]int

	// keys contains every key, grouped by party in party order.
	keys []Verifier

	// ends[j] is one past the position of party j's last key.
	ends []int

	// owner[k] is the party of key k.
	owner []int

	threshold int
}

// maxStackKeys is the largest number of keys whose bookkeeping
// [Policy.Check] keeps on the stack.
const maxStackKeys = 64

// excluded marks the keys of an excluded party in Check's bookkeeping.
const excluded = -1

// NewPolicy returns a Policy that requires valid signatures from at
// least threshold of parties. The parties' key slices are copied.
//
// Returns [ErrPolicy], classified [errs.Invalid], when threshold is not
// between 1 and len(parties), when a party has no keys or a nil key,
// and when one key appears twice, in one party or in two. The key check
// compares KeyIDs and encoded public keys, so one key cannot be listed
// under two identities.
func NewPolicy(threshold int, parties ...Party) (Policy, error) {
	if threshold < 1 || threshold > len(parties) {
		return Policy{}, fmt.Errorf("%w: threshold %d of %d parties", ErrPolicy, threshold, len(parties))
	}

	n := 0
	for _, party := range parties {
		n += len(party.Keys)
	}

	p := Policy{
		index:     make(map[KeyID]int, n),
		keys:      make([]Verifier, 0, n),
		ends:      make([]int, 0, len(parties)),
		owner:     make([]int, 0, n),
		threshold: threshold,
	}
	pubs := make(map[string]struct{}, n)

	for j, party := range parties {
		if len(party.Keys) == 0 {
			return Policy{}, fmt.Errorf("%w: party %d %q has no keys", ErrPolicy, j, party.Name)
		}

		for _, key := range party.Keys {
			if key == nil {
				return Policy{}, fmt.Errorf("%w: party %d %q has a nil key", ErrPolicy, j, party.Name)
			}

			id := key.KeyID()
			if _, dup := p.index[id]; dup {
				return Policy{}, fmt.Errorf("%w: key %s is listed twice", ErrPolicy, id)
			}

			pub := string(key.PublicKey())
			if _, dup := pubs[pub]; dup {
				return Policy{}, fmt.Errorf("%w: key %s repeats another key's public key", ErrPolicy, id)
			}

			p.index[id] = len(p.keys)
			pubs[pub] = struct{}{}
			p.keys = append(p.keys, key)
			p.owner = append(p.owner, j)
		}

		p.ends = append(p.ends, len(p.keys))
	}

	return p, nil
}

// Check reports whether sigs satisfy p for message.
//
// Check considers at most one signature per key of p: the first in
// sigs whose KeyID names that key. It ignores later signatures for the
// same key, and signatures for keys outside p, without verifying them.
// A considered signature counts when its algorithm equals its key's
// and it verifies over message. A party counts when every one of its
// keys has a counting signature. A party with a key in exclude does
// not count, which keeps a requester from approving their own request.
//
// Check stops verifying as soon as the threshold of parties count, and
// as soon as the parties left can no longer meet the threshold. It
// verifies no signature for a party that lacks a signature for one of
// its keys. The number of verifications is at most the number of keys
// of p, whatever the number of signatures in sigs.
//
// Returns nil when at least the threshold of parties count. Otherwise
// returns [ErrThreshold], classified [errs.Integrity], wrapped with the
// most parties that could count and the threshold. Returns [ErrPolicy]
// for the zero Policy.
//
// # Allocation contract
//
// Zero-alloc when it returns nil for a policy of at most 64 keys,
// apart from what each Verifier allocates. A larger policy allocates
// its bookkeeping, and a failed check allocates the returned error.
func (p Policy) Check(message []byte, sigs []Signature, exclude ...KeyID) error {
	if p.threshold == 0 {
		return fmt.Errorf("%w: the zero Policy checks nothing", ErrPolicy)
	}

	// first[k] is one more than the position in sigs of the first
	// signature for key k, zero when there is none, or excluded.
	var (
		stack [maxStackKeys]int
		first []int
	)

	if len(p.keys) <= maxStackKeys {
		first = stack[:len(p.keys)]
	} else {
		first = make([]int, len(p.keys))
	}

	candidates := len(p.ends)

	for _, id := range exclude {
		k, ok := p.index[id]
		if !ok {
			continue
		}

		start, end := p.bounds(p.owner[k])
		if first[start] == excluded {
			continue
		}

		for i := start; i < end; i++ {
			first[i] = excluded
		}

		candidates--
	}

	for i := range sigs {
		if k, ok := p.index[sigs[i].KeyID]; ok && first[k] == 0 {
			first[k] = i + 1
		}
	}

	counted, left := 0, candidates
	for j := range p.ends {
		if counted >= p.threshold || counted+left < p.threshold {
			break
		}

		start, end := p.bounds(j)
		if first[start] == excluded {
			continue
		}

		left--

		if counts(message, sigs, first[start:end], p.keys[start:end]) {
			counted++
		}
	}

	if counted >= p.threshold {
		return nil
	}

	return fmt.Errorf("%w: at most %d of %d required parties", ErrThreshold, counted+left, p.threshold)
}

// bounds returns the positions of party j's first key and one past its
// last.
func (p Policy) bounds(j int) (start, end int) {
	if j > 0 {
		start = p.ends[j-1]
	}

	return start, p.ends[j]
}

// counts reports whether a party with keys counts. first holds the
// position plus one of each key's signature in sigs. The party counts
// only when every key has a signature, so a missing one costs no
// verification.
func counts(message []byte, sigs []Signature, first []int, keys []Verifier) bool {
	if slices.Contains(first, 0) {
		return false
	}

	for i, key := range keys {
		s := &sigs[first[i]-1]
		if s.Algorithm != key.Algorithm() || !key.Verify(message, s.Value) {
			return false
		}
	}

	return true
}
