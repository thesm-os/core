// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package checkpoint

import (
	"fmt"
	"slices"
	"strings"

	"go.thesmos.sh/core/crypto/sign"
	"go.thesmos.sh/core/note"
)

const (
	// checkpointRule is the name of the root rule of the tree of each
	// origin under a quorum.
	checkpointRule = "checkpoint"

	// anyLog is the threshold of the log rule of an origin: one of its log
	// keys signs, as tlog-policy accepts a checkpoint that any one of the
	// listed logs signs.
	anyLog = 1

	// logAndQuorum is the threshold of the root rule: the log rule and the
	// quorum both count.
	logAndQuorum = 2

	// stackLogs is the number of log keys of one origin whose rules
	// [Verifier.Reset] collects on the stack.
	stackLogs = 8
)

// Verifier verifies checkpoints against a [Policy]. It keeps one
// [sign.Policy] per origin, the key name of one or more logs. The tree of
// an origin requires the signature of one of its log keys and the
// cosignatures of the quorum:
//
//   - A witness is an [sign.AllOf] rule of its key.
//   - A group is an [sign.AtLeast] rule of its members, with its
//     threshold.
//   - The root is AtLeast("checkpoint", 2, logs, quorum), where logs is
//     an AtLeast rule with threshold 1 over one AllOf rule per log key of
//     the origin.
//   - Under [QuorumNone], the root is the logs rule alone.
//
// A checkpoint therefore counts only under the logs of its origin, and a
// checkpoint whose origin is the key name of no log fails before any
// verification.
//
// [NewVerifier] returns a new Verifier, and [Verifier.Reset] sets one in
// its own memory to another policy. The zero Verifier has no tree, and
// its Verify returns [ErrOrigin] for every checkpoint.
//
// # Concurrency
//
// Safe for concurrent use while no goroutine calls Reset on it. A caller
// that replaces the policy of goroutines that verify resets a second
// Verifier and swaps the two.
//
// # Allocation contract
//
// Verify allocates nothing apart from what the Verifiers of the keys
// allocate, as [Verifier.Verify] documents. Reset allocates nothing for
// the policy of the Reset before it, as on the reload of an unchanged
// policy file.
type Verifier struct {
	// keys builds the note Verifier of each key of the policy, and keeps
	// it for the next Reset.
	keys note.Keyring

	// rules is the memory of the rules of the trees.
	rules sign.Rules

	// order is the memory of the positions of the logs of the policy,
	// sorted by origin.
	order []int

	// trees are the trees of the origins of the policy, sorted by origin.
	// Reset reuses the memory of each tree.
	trees []tree
}

// tree is the policy of the checkpoints of one origin.
type tree struct {
	// origin is the origin, the key name of the logs of the tree. Verify
	// sets it in the Body, so that it allocates no string for the origin.
	origin Origin

	// policy requires the signature of a log of the origin and the
	// quorum.
	policy sign.Policy
}

// NewVerifier returns a new Verifier for p, with each key resolved through
// r, as [Verifier.Reset] sets it. The Verifier does not refer to p.
//
// Returns the errors of Reset.
//
// # Allocation contract
//
// Allocates the Verifier, and the memory of its Keyring, rules and trees
// at the size of p, once each. Allocates the Verifier of each key, which
// is two allocations for an Ed25519 key, and the memory of the
// [sign.Policy] of each origin, which is four allocations for up to 8
// keys.
func NewVerifier(p *Policy, r note.Resolver) (*Verifier, error) {
	v := &Verifier{}
	v.keys.Grow(len(p.Logs) + len(p.Witnesses))

	if err := v.Reset(p, r); err != nil {
		return nil, err
	}

	return v, nil
}

// Reset sets v to the Verifier of p, with each key resolved through r, and
// reuses the memory of v: the Verifier of each key that the policy before
// has, through a [note.Keyring], the rules, and the [sign.Policy] of each
// tree. It builds the quorum once, and the tree of each origin over it.
// The Verifier does not refer to p.
//
// The Keyring of v builds the Verifier of a key once, and assumes that
// the entry of a type in r builds the same Verifier in every Reset. A
// caller that replaces the entry of a type builds a new Verifier.
//
// Returns [ErrPolicy], classified [errs.Invalid], for a Policy that is not
// Valid and for a policy without logs. Returns the error of
// [note.Resolver.Verifier] for a key that r cannot resolve, and the error
// of [sign.Policy.Reset], which wraps [sign.ErrPolicy], classified
// errs.Invalid, for a tree that it refuses, such as one deeper than 64
// rules. After an error v has no tree, and keeps its memory for the next
// Reset.
//
// # Allocation contract
//
// Zero-alloc when v has room for the rules and the trees of p and keeps
// the Verifier of each key, as after a Reset of v to p. Otherwise
// allocates the Verifier of each new key, and the growth of the memory of
// v. It collects the rules of up to 8 log keys of one origin on the
// stack.
func (v *Verifier) Reset(p *Policy, r note.Resolver) error {
	if err := v.reset(p, r); err != nil {
		v.trees = v.trees[:0]

		return err
	}

	return nil
}

// Verify sets b to the body of the checkpoint in n when the signatures of
// n satisfy the tree of its origin: the signature of a log whose key name
// is the origin, and the cosignatures of the quorum. It parses the body
// before it selects a tree, because the tree depends on the origin, and
// reuses the memory of b as [Body.UnmarshalText] does.
//
// Returns the error of [ParseBody], which wraps [ErrBody], when n.Text is
// not a body, and [ErrOrigin], classified [errs.Integrity], for an origin
// that is not the key name of a log of the policy. Returns the error of
// [note.Note.Check], which wraps [sign.ErrThreshold], classified
// errs.Integrity, when the signatures do not satisfy the tree. b is
// unchanged on an error.
//
// # Allocation contract
//
// Zero-alloc, apart from what the Verifiers of the keys allocate, for a
// note of at most 16 lines whose extension lines equal those of b: Verify
// finds the tree of the origin by a binary search, sets the origin of the
// tree in b, and Note.Check converts the lines on the stack. An error
// allocates itself.
func (v *Verifier) Verify(n *note.Note, b *Body) error {
	l, err := splitBody(n.Text)
	if err != nil {
		return err
	}

	i, ok := slices.BinarySearchFunc(v.trees, l.origin, compareOrigin)
	if !ok {
		return fmt.Errorf("%w: %q", ErrOrigin, l.origin)
	}

	t := &v.trees[i]
	if err := n.Check(t.policy); err != nil {
		return err
	}

	b.Origin = t.origin
	b.assign(l, n.Text)

	return nil
}

// reset is Verifier.Reset without the removal of the trees on an error. It
// builds the quorum, sorts the positions of the logs by origin, and builds
// the tree of each run of logs of one origin.
func (v *Verifier) reset(p *Policy, r note.Resolver) error {
	if err := p.validate().err(); err != nil {
		return err
	}

	if len(p.Logs) == 0 {
		return fmt.Errorf("%w: a verifier needs a log", ErrPolicy)
	}

	v.keys.Reset(r)
	v.rules.Reset()
	v.rules.Grow(p.ruleSize())

	var quorum sign.Rule

	if p.Quorum != QuorumNone {
		q, err := p.quorumRule(&v.rules, &v.keys)
		if err != nil {
			return err
		}

		quorum = q
	}

	order := slices.Grow(v.order[:0], len(p.Logs))
	for i := range p.Logs {
		order = append(order, i)
	}

	slices.SortStableFunc(order, func(a, b int) int {
		return strings.Compare(string(p.Logs[a].Key.Name), string(p.Logs[b].Key.Name))
	})

	v.order = order
	trees := v.trees[:0]

	for start, end := 0, 0; start != len(order); start = end {
		origin := p.Logs[order[start]].Key.Name

		var stack [stackLogs]sign.Rule

		leaves := stack[:0]

		for end != len(order) && p.Logs[order[end]].Key.Name == origin {
			key, err := v.keys.Verifier(p.Logs[order[end]].Key)
			if err != nil {
				return err
			}

			leaves = append(leaves, v.rules.AllOf(string(origin), key))
			end++
		}

		root := v.rules.AtLeast(string(origin), anyLog, leaves...)
		if p.Quorum != QuorumNone {
			root = v.rules.AtLeast(checkpointRule, logAndQuorum, root, quorum)
		}

		trees = slices.Grow(trees, 1)[:len(trees)+1]
		t := &trees[len(trees)-1]
		t.origin = Origin(origin)

		if err := t.policy.Reset(root); err != nil {
			return err
		}
	}

	v.trees = trees

	return nil
}

// compareOrigin orders the tree t against the origin line o by the order
// of [strings.Compare]. It compares with the string operators, which
// convert o without a copy.
func compareOrigin(t tree, o []byte) int {
	if string(t.origin) < string(o) {
		return -1
	}

	if string(t.origin) > string(o) {
		return 1
	}

	return 0
}
