// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package sign

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"go.thesmos.sh/core/crypto"
)

const (
	// maxStackKeys is the largest number of keys whose bookkeeping
	// [Policy.Check] keeps on the stack.
	maxStackKeys = 64

	// maxStackRules is the largest number of rules whose bookkeeping
	// [Policy.Check] keeps on the stack.
	maxStackRules = 128

	// maxDepth is the largest number of rules on the path from the root
	// of a [Policy] to a key set. C2SP tlog-policy asks an
	// implementation to accept 32 groups, and a chain of 32 groups over
	// one witness is 33 rules deep.
	maxDepth = 64
)

// excluded marks the keys of an excluded party in Check's bookkeeping.
// A real entry is zero or a position plus one, so any negative value
// is free.
const excluded = math.MinInt

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

// Party is a signer that counts once toward the threshold of a policy
// that [NewPolicy] builds. A party has one or more keys and counts only
// when every one of its keys has a valid signature. A party with an
// Ed25519 key and an ML-DSA key is a hybrid signer, and counts only
// when both sign.
type Party struct {
	// Name identifies the party in the caller's diagnostics.
	Name string

	// Keys are the party's keys. Every key must sign for the party to
	// count.
	Keys []Verifier
}

// Rule is one node of a signature policy: a set of keys that must all
// sign, or a threshold over child rules. [AllOf] and [AtLeast] build a
// Rule without validating it, and [NewPolicyTree] validates the whole
// tree. The zero Rule is invalid in a policy.
//
// A Rule contains copies of its keys and children and has no exported
// fields, so it contains only rules built before it, and no rule is its
// own descendant.
type Rule struct {
	// name identifies the rule in the errors of NewPolicyTree.
	name string

	// keys are the keys of an AllOf rule, and nil for an AtLeast rule.
	keys []Verifier

	// children are the child rules of an AtLeast rule, and nil for an
	// AllOf rule.
	children []Rule

	// threshold is the number of children that must count.
	threshold int
}

// AllOf returns a rule that counts when every one of keys has a valid
// signature: one signer, or one hybrid signer with a classical and a
// post-quantum key. name identifies the rule in the errors of
// [NewPolicyTree]. AllOf copies keys.
//
// # Allocation contract
//
// One allocation for the copy of keys, and none when keys is empty.
func AllOf(name string, keys ...Verifier) Rule {
	return Rule{name: name, keys: slices.Clone(keys)}
}

// AtLeast returns a rule that counts when at least threshold of
// children count: a quorum of parties, or the alternatives of one
// party with threshold 1. name identifies the rule in the errors of
// [NewPolicyTree]. AtLeast copies children.
//
// # Allocation contract
//
// One allocation for the copy of children, and none when children is
// empty.
func AtLeast(name string, threshold int, children ...Rule) Rule {
	return Rule{name: name, children: slices.Clone(children), threshold: threshold}
}

// Policy requires valid signatures from a tree of rules. An [AllOf]
// rule is a key set that counts when every one of its keys signs, and
// an [AtLeast] rule counts when a threshold of its children count. The
// policy is satisfied when its root counts. It expresses a threshold of
// approvers, a quorum of witnesses, a hybrid signature that needs both
// a classical and a post-quantum key, and the alternatives of one
// party.
//
// The parties of a policy are the children of its root, or the root
// itself when the root is an AllOf rule. A key that [Policy.Check]
// excludes removes its party.
//
// Build a Policy with [NewPolicy] or [NewPolicyTree]. The zero Policy
// refuses every check with [ErrPolicy].
//
// # Concurrency
//
// Immutable and safe for concurrent use.
type Policy struct {
	// index maps each key's KeyID to its position in keys.
	index map[KeyID]int

	// keys contains every key of the tree in depth-first order, so the
	// keys of each rule are contiguous.
	keys []Verifier

	// party[k] is the position in rules of the party of key k.
	party []int

	// rules contains every rule of the tree in depth-first order, the
	// root first, so each rule precedes the rules of its subtree.
	rules []rule
}

// NewPolicy returns a Policy that requires valid signatures from at
// least threshold of parties: an [AtLeast] rule over one [AllOf] rule
// per party. The parties' key slices are copied.
//
// Returns [ErrPolicy], classified [errs.Invalid], when threshold is not
// between 1 and len(parties), when a party has no keys or a nil key,
// and when one key appears twice, in one party or in two. The key check
// compares KeyIDs and encoded public keys, so one key cannot be listed
// under two identities.
func NewPolicy(threshold int, parties ...Party) (Policy, error) {
	rules := make([]Rule, len(parties))
	for j, party := range parties {
		rules[j] = AllOf(party.Name, party.Keys...)
	}

	return NewPolicyTree(AtLeast("parties", threshold, rules...))
}

// NewPolicyTree returns a Policy that requires root to count. It copies
// the tree into the Policy.
//
// Returns [ErrPolicy], classified [errs.Invalid], when a threshold is
// not between 1 and the number of its rule's children, when a rule has
// neither keys nor children, when a key is nil, when the tree is deeper
// than 64 rules, and when one key appears twice anywhere in the tree.
// The key check compares KeyIDs and encoded public keys, so one key
// cannot be listed under two identities. The error reports the path of
// the offending rule: the names of the rules from the root to it,
// joined by "/", such as "X-and-Y/X-witnesses".
func NewPolicyTree(root Rule) (Policy, error) {
	b := builder{index: map[KeyID]int{}, pubs: map[string]struct{}{}}
	if err := b.add(root, nil, 0); err != nil {
		return Policy{}, err
	}

	return Policy{index: b.index, keys: b.keys, party: b.party, rules: b.rules}, nil
}

// Check reports whether sigs satisfy p for message.
//
// Check considers at most one signature per key of p: the first in
// sigs whose KeyID names that key. It ignores later signatures for the
// same key, and signatures for keys outside p, without verifying them.
// Check counts by these rules:
//
//   - A considered signature counts when its algorithm equals its key's
//     and it verifies over message.
//   - An [AllOf] rule counts when every one of its keys has a counting
//     signature.
//   - An [AtLeast] rule counts when at least its threshold of children
//     count.
//   - p is satisfied when its root counts.
//
// A party with a key in exclude does not count. This keeps a requester
// from approving their own request. The parties are the root's
// children, or the root itself when it is an AllOf rule, so one
// excluded key removes every key set of its party.
//
// Check bounds its verifications:
//
//   - It verifies no signature when the considered signatures cannot
//     satisfy the root even if every one of them is valid.
//   - It verifies no signature of a rule that the considered signatures
//     cannot make count.
//   - A rule stops verifying once it counts, and once its remaining
//     children can no longer make it count.
//   - The number of verifications is at most the number of keys of p,
//     whatever the number of signatures in sigs.
//
// Returns nil when the root counts. Otherwise returns [ErrThreshold],
// classified [errs.Integrity], wrapped with the most parties that could
// count and the number of parties the root requires. Returns
// [ErrPolicy] for the zero Policy.
//
// # Allocation contract
//
// Zero-alloc when it returns nil for a policy of at most 64 keys and
// 128 rules, apart from what each Verifier allocates. A larger policy
// allocates its bookkeeping, and a failed check allocates the returned
// error.
func (p Policy) Check(message []byte, sigs []Signature, exclude ...KeyID) error {
	if len(p.rules) == 0 {
		return fmt.Errorf("%w: the zero Policy checks nothing", ErrPolicy)
	}

	var (
		keyStack  [maxStackKeys]int
		ruleStack [maxStackRules]int
	)

	b := bookkeeping{
		first: scratch(keyStack[:], len(p.keys)),
		reach: scratch(ruleStack[:], len(p.rules)),
	}

	for _, id := range exclude {
		if k, ok := p.index[id]; ok {
			r := &p.rules[p.party[k]]
			marked := b.first[r.lo:r.hi]
			for i := range marked {
				marked[i] = excluded
			}
		}
	}

	for i := range sigs {
		if k, ok := p.index[sigs[i].KeyID]; ok && b.first[k] == 0 {
			b.first[k] = i + 1
		}
	}

	// A rule's subtree follows it, so a pass from the last rule to the
	// root counts every child before its parent.
	for n := range len(p.rules) {
		i := len(p.rules) - 1 - n
		b.reach[i] = p.reachable(i, &b)
	}

	root := &p.rules[0]
	counted, left, required := 0, 0, 1

	if root.children == 0 {
		if b.reach[0] == root.need && p.counts(0, message, sigs, &b) {
			counted = 1
		}
	} else {
		counted, left = p.tally(0, message, sigs, &b)
		required = root.need
	}

	if counted >= required {
		return nil
	}

	return fmt.Errorf("%w: at most %d of %d required parties", ErrThreshold, counted+left, required)
}

// reachable returns the number of rule i's children, or of a key set's
// keys, that could count if every signature recorded in b were valid.
// It reads the counts of i's children from b.reach.
func (p Policy) reachable(i int, b *bookkeeping) int {
	r := &p.rules[i]
	n := 0

	if r.children == 0 {
		for _, f := range b.first[r.lo:r.hi] {
			if f > 0 {
				n++
			}
		}

		return n
	}

	c := i + 1
	for range r.children {
		if b.reach[c] >= p.rules[c].need {
			n++
		}

		c = p.rules[c].next
	}

	return n
}

// counts reports whether rule i counts for message and sigs. It
// verifies the signatures of a key set, and tallies the children of a
// threshold rule. b.reach must show that rule i could count.
func (p Policy) counts(i int, message []byte, sigs []Signature, b *bookkeeping) bool {
	r := &p.rules[i]
	if r.children == 0 {
		for k, key := range p.keys[r.lo:r.hi] {
			s := &sigs[b.first[r.lo+k]-1]
			if s.Algorithm != key.Algorithm() || !key.Verify(message, s.Value) {
				return false
			}
		}

		return true
	}

	counted, _ := p.tally(i, message, sigs, b)

	return counted >= r.need
}

// tally evaluates the children of threshold rule i in order, and skips
// each child that b.reach shows cannot count. It stops once counted
// meets the rule's threshold, and once the children left can no longer
// meet it. It returns the number of children that counted, and the
// number that could count but that it did not evaluate.
func (p Policy) tally(i int, message []byte, sigs []Signature, b *bookkeeping) (counted, left int) {
	r := &p.rules[i]
	left = b.reach[i]

	c := i + 1
	for range r.children {
		if counted >= r.need || counted+left < r.need {
			break
		}

		if b.reach[c] >= p.rules[c].need {
			left--

			if p.counts(c, message, sigs, b) {
				counted++
			}
		}

		c = p.rules[c].next
	}

	return counted, left
}

// rule is one rule of a [Policy], in the depth-first order of
// Policy.rules.
type rule struct {
	// need is the number of children that must count, or for a key set
	// the number of its keys, which must all count.
	need int

	// children is the number of child rules, or 0 for a key set.
	children int

	// next is the position in Policy.rules one past the rule's subtree:
	// the position of its next sibling.
	next int

	// lo and hi bound the positions in Policy.keys of the keys of the
	// rule's subtree.
	lo, hi int
}

// bookkeeping is the state of one [Policy.Check]. It contains nothing
// that a Verifier receives, so escape analysis keeps the arrays behind
// first and reach on the stack of Check.
type bookkeeping struct {
	// first[k] is one more than the position in sigs of the first
	// signature for key k, zero when there is none, or excluded.
	first []int

	// reach[i] is the number of rule i's children, or of a key set's
	// keys, that could count if every recorded signature were valid.
	reach []int
}

// builder flattens a tree of [Rule] values into the arrays of a
// [Policy].
type builder struct {
	// index maps each KeyID seen so far to its position in keys.
	index map[KeyID]int

	// pubs contains the encoded public key of every key seen so far.
	pubs map[string]struct{}

	keys  []Verifier
	party []int
	rules []rule
}

// add appends r and its subtree in depth-first order. path contains the
// names of r's ancestors, and party is the position of the party that
// r belongs to. A child of the root starts a party of its own.
func (b *builder) add(r Rule, path []string, party int) error {
	path = append(path, r.name)
	if len(path) > maxDepth {
		return errRule(path, "deeper than %d rules", maxDepth)
	}

	if len(r.keys) == 0 && len(r.children) == 0 {
		return errRule(path, "neither keys nor children")
	}

	pos := len(b.rules)
	if len(path) == 2 {
		party = pos
	}

	b.rules = append(b.rules, rule{lo: len(b.keys)})

	if len(r.keys) > 0 {
		for _, key := range r.keys {
			if err := b.addKey(key, path, party); err != nil {
				return err
			}
		}

		b.rules[pos].need = len(r.keys)
	} else {
		if r.threshold < 1 || r.threshold > len(r.children) {
			return errRule(path, "threshold %d of %d rules", r.threshold, len(r.children))
		}

		for _, child := range r.children {
			if err := b.add(child, path, party); err != nil {
				return err
			}
		}

		b.rules[pos].need = r.threshold
		b.rules[pos].children = len(r.children)
	}

	b.rules[pos].hi = len(b.keys)
	b.rules[pos].next = len(b.rules)

	return nil
}

// addKey appends key, a key of the rule at path, to the party at
// position party.
func (b *builder) addKey(key Verifier, path []string, party int) error {
	if key == nil {
		return errRule(path, "a nil key")
	}

	id := key.KeyID()
	if _, dup := b.index[id]; dup {
		return errRule(path, "key %s listed twice", id)
	}

	pub := string(key.PublicKey())
	if _, dup := b.pubs[pub]; dup {
		return errRule(path, "key %s repeats another key's public key", id)
	}

	b.index[id] = len(b.keys)
	b.pubs[pub] = struct{}{}
	b.keys = append(b.keys, key)
	b.party = append(b.party, party)

	return nil
}

// scratch returns n zeroed ints: the first n of stack when they fit, or
// a new slice.
func scratch(stack []int, n int) []int {
	if n <= len(stack) {
		return stack[:n]
	}

	return make([]int, n)
}

// errRule returns [ErrPolicy] for the rule at path, with the reason
// that format and args describe.
func errRule(path []string, format string, args ...any) error {
	return fmt.Errorf("%w: rule %q: %s", ErrPolicy, strings.Join(path, "/"), fmt.Sprintf(format, args...))
}
