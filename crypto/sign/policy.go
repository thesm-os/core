// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package sign

import (
	"bytes"
	"fmt"
	"math"
	"slices"
	"strings"
)

const (
	// maxStackKeys is the largest number of keys whose bookkeeping
	// [Policy.Check] keeps on the stack, and whose order by public key
	// [Policy.Reset] keeps on the stack.
	maxStackKeys = 64

	// maxStackRules is the largest number of rules whose bookkeeping
	// [Policy.Check] keeps on the stack.
	maxStackRules = 128

	// maxDepth is the largest number of rules on the path from the root
	// of a [Policy] to a key set. C2SP tlog-policy asks an
	// implementation to accept 32 groups, and a chain of 32 groups over
	// one witness is 33 rules deep.
	maxDepth = 64

	// stackParties is the number of parties whose rules [NewPolicy]
	// builds on the stack.
	stackParties = 16
)

// excluded marks the keys of an excluded party in Check's bookkeeping.
// A real entry is zero or a position plus one, so any negative value
// is free.
const excluded = math.MinInt

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
// sign, or a threshold over child rules. [AllOf] and [AtLeast], and the
// methods of [Rules] that share their names, build a Rule without
// validating it, and [NewPolicyTree] validates the whole tree. The zero
// Rule is invalid in a policy.
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
// [Rules.AllOf] copies into memory that a caller reuses.
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
// empty. [Rules.AtLeast] copies into memory that a caller reuses.
func AtLeast(name string, threshold int, children ...Rule) Rule {
	return Rule{name: name, children: slices.Clone(children), threshold: threshold}
}

// Rules is the memory of the rules of a tree. Its AllOf and AtLeast
// build the rules that [AllOf] and [AtLeast] build, and copy keys and
// children into memory that Rules keeps instead of new memory. Reset
// makes that memory free for the rules of the next tree, so a caller
// that builds a tree of the same size again allocates nothing.
//
// A rule that Rules built is valid until the next Reset of that Rules.
// A [Policy] copies the tree that it receives, so a Policy built from
// the rules stays valid after Reset.
//
// The zero Rules is empty and ready to use.
//
// # Concurrency
//
// Not safe for concurrent use.
//
// # Allocation contract
//
// AllOf and AtLeast allocate nothing when Rules has room for the copy,
// as after a [Rules.Grow] for the tree or after a Reset that freed the
// memory of a tree of the same size. Otherwise they allocate the growth
// of the memory.
type Rules struct {
	// keys contains the keys of every AllOf rule built since the last
	// Reset, each rule's keys contiguous.
	keys []Verifier

	// children contains the children of every AtLeast rule built since
	// the last Reset, each rule's children contiguous.
	children []Rule
}

// Grow makes room in r for keys more keys of AllOf rules and children
// more children of AtLeast rules, so that a caller that knows the size of
// its tree builds it with one allocation for each.
func (r *Rules) Grow(keys, children int) {
	r.keys = slices.Grow(r.keys, keys)
	r.children = slices.Grow(r.children, children)
}

// AllOf returns the rule that the function [AllOf] returns, with keys
// copied into the memory of r.
func (r *Rules) AllOf(name string, keys ...Verifier) Rule {
	start := len(r.keys)
	r.keys = append(r.keys, keys...)

	return Rule{name: name, keys: r.keys[start:len(r.keys):len(r.keys)]}
}

// AtLeast returns the rule that the function [AtLeast] returns, with
// children copied into the memory of r.
func (r *Rules) AtLeast(name string, threshold int, children ...Rule) Rule {
	start := len(r.children)
	r.children = append(r.children, children...)

	return Rule{name: name, children: r.children[start:len(r.children):len(r.children)], threshold: threshold}
}

// Reset empties r and keeps its memory for the rules of the next tree.
// It clears the keys and children that r contains, so that r does not
// keep them alive. The rules that r built before become invalid.
func (r *Rules) Reset() {
	clear(r.keys)
	clear(r.children)
	r.keys, r.children = r.keys[:0], r.children[:0]
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
// Build a Policy with [NewPolicy] or [NewPolicyTree], and build it again
// in its own memory with [Policy.Reset]. The zero Policy refuses every
// check with [ErrPolicy].
//
// # Concurrency
//
// Safe for concurrent use while no goroutine calls Reset on it. A copy
// of a Policy shares its memory, so a Reset changes every copy.
type Policy struct {
	// index maps each key's KeyID to its position in keys.
	index map[KeyID]int

	// keys contains every key of the tree in depth-first order, so the
	// keys of each rule are contiguous, each with its party.
	keys []policyKey

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
//
// # Allocation contract
//
// The allocations of [NewPolicyTree]. It builds the rules of up to 16
// parties on the stack, and allocates them for more.
func NewPolicy(threshold int, parties ...Party) (Policy, error) {
	var stack [stackParties]Rule

	rules := stack[:0]
	for _, party := range parties {
		rules = append(rules, Rule{name: party.Name, keys: party.Keys})
	}

	return NewPolicyTree(Rule{name: "parties", children: rules, threshold: threshold})
}

// NewPolicyTree returns a Policy that requires root to count, as
// [Policy.Reset] builds it in new memory. It copies the tree into the
// Policy.
//
// Returns [ErrPolicy], classified [errs.Invalid], when a threshold is
// not between 1 and the number of its rule's children, when a rule has
// neither keys nor children, when a key is nil, when the tree is deeper
// than 64 rules, and when one key appears twice anywhere in the tree.
// The key check compares KeyIDs and encoded public keys, so one key
// cannot be listed under two identities. The error reports the path of
// the offending rule: the names of the rules from the root to it,
// joined by "/", such as "X-and-Y/X-witnesses". A tree with several
// faults reports the first that a check of its shape finds, and then the
// first repeated key in depth-first order.
//
// # Allocation contract
//
// Allocates the index of the keys, which is two allocations for up to 8
// keys and four for up to 128, the keys and the rules, each at its
// exact size. A tree of more than 64 keys allocates its order by public
// key once more. A tree that it refuses allocates its error.
func NewPolicyTree(root Rule) (Policy, error) {
	var p Policy
	if err := p.Reset(root); err != nil {
		return Policy{}, err
	}

	return p, nil
}

// Reset sets p to the Policy that requires root to count, as
// NewPolicyTree builds it, and reuses the memory of p: its index of
// keys, its keys and its rules. Every copy of p shares that memory, so
// reset a Policy only while no copy of it is in use. A caller that
// replaces the policy of goroutines that check builds the new policy in
// a second Policy and swaps the two.
//
// Returns the errors of [NewPolicyTree]. After an error p checks nothing
// and refuses every check with [ErrPolicy], as the zero Policy does, and
// keeps its memory for the next Reset.
//
// # Allocation contract
//
// Zero-alloc for a tree of at most 64 keys when p has room for its keys
// and rules, as when p held a tree of the same size. Otherwise allocates
// what NewPolicyTree allocates.
func (p *Policy) Reset(root Rule) error {
	if err := p.reset(root); err != nil {
		clear(p.index)
		clear(p.keys)
		p.keys, p.rules = p.keys[:0], p.rules[:0]

		return err
	}

	return nil
}

// reset is Policy.Reset without the emptying of p on an error. It checks
// the shape of the tree and counts its keys and rules first, so that it
// sizes the memory of p once, and then copies the tree in depth-first
// order, where it finds repeated keys.
func (p *Policy) reset(root Rule) error {
	var (
		b     builder
		at    [maxDepth]int
		stack [maxStackKeys]int
	)

	if err := b.check(root, root, at[:0]); err != nil {
		return err
	}

	if p.index == nil {
		p.index = make(map[KeyID]int, b.nkeys)
	} else {
		clear(p.index)
	}

	clear(p.keys)
	b.index = p.index
	b.keys = slices.Grow(p.keys[:0], b.nkeys)
	b.rules = slices.Grow(p.rules[:0], b.nrules)

	_, err := b.add(root, root, at[:0], 0, slices.Grow(stack[:0], b.nkeys))
	p.keys, p.rules = b.keys, b.rules

	return err
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
			r := &p.rules[p.keys[k].party]
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
		for k, entry := range p.keys[r.lo:r.hi] {
			s := &sigs[b.first[r.lo+k]-1]
			if s.Algorithm != entry.key.Algorithm() || !entry.key.Verify(message, s.Value) {
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

// policyKey is one key of a [Policy], in the depth-first order of
// Policy.keys.
type policyKey struct {
	// key verifies the signatures of the key.
	key Verifier

	// party is the position in Policy.rules of the party of the key.
	party int
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

// builder copies a tree of [Rule] values into the memory of a [Policy].
//
// Its methods locate a rule by the positions of the children on the path
// from the root to it, and build the names of that path only for an
// error. They store no name and no pointer to the tree, and the order of
// the keys passes through their results, so that escape analysis keeps
// the tree, the path and the order on the stack of their callers.
type builder struct {
	// index maps each KeyID copied so far to its position in keys.
	index map[KeyID]int

	// keys and rules are the keys and the rules copied so far.
	keys  []policyKey
	rules []rule

	// nkeys and nrules are the number of keys and rules that check
	// counted.
	nkeys, nrules int
}

// check checks the shape of r and of its subtree, and adds their keys and
// rules to the counts of b. at contains the positions of the children on
// the path from root to r, so its length is the depth of r. check finds
// every fault but a repeated key, which add finds.
func (b *builder) check(root, r Rule, at []int) error {
	if len(at) == maxDepth {
		return errRule(root, at, "deeper than %d rules", maxDepth)
	}

	if len(r.keys) == 0 && len(r.children) == 0 {
		return errRule(root, at, "neither keys nor children")
	}

	b.nrules++

	if len(r.keys) > 0 {
		if slices.Contains(r.keys, nil) {
			return errRule(root, at, "a nil key")
		}

		b.nkeys += len(r.keys)

		return nil
	}

	if r.threshold < 1 || r.threshold > len(r.children) {
		return errRule(root, at, "threshold %d of %d rules", r.threshold, len(r.children))
	}

	for i, child := range r.children {
		if err := b.check(root, child, append(at, i)); err != nil {
			return err
		}
	}

	return nil
}

// add appends r and its subtree in depth-first order. at locates r below
// root as check documents, and party is the position of the party that r
// belongs to. A child of the root starts a party of its own. order
// contains the positions of the keys copied so far, sorted by public key,
// and add returns it with the positions of the keys that it copies.
// check has checked the shape of the tree.
func (b *builder) add(root, r Rule, at []int, party int, order []int) ([]int, error) {
	pos := len(b.rules)
	if len(at) == 1 {
		party = pos
	}

	b.rules = append(b.rules, rule{lo: len(b.keys)})

	var err error

	if len(r.keys) > 0 {
		for _, key := range r.keys {
			if order, err = b.addKey(root, at, key, party, order); err != nil {
				return order, err
			}
		}

		b.rules[pos].need = len(r.keys)
	} else {
		for i, child := range r.children {
			if order, err = b.add(root, child, append(at, i), party, order); err != nil {
				return order, err
			}
		}

		b.rules[pos].need = r.threshold
		b.rules[pos].children = len(r.children)
	}

	b.rules[pos].hi = len(b.keys)
	b.rules[pos].next = len(b.rules)

	return order, nil
}

// addKey appends key, a key of the rule that at locates below root, to
// the party at position party, and returns order with the position of
// the key inserted. It returns an error for a KeyID or a public key that
// a key copied before has.
func (b *builder) addKey(root Rule, at []int, key Verifier, party int, order []int) ([]int, error) {
	id := key.KeyID()
	if _, dup := b.index[id]; dup {
		return order, errRule(root, at, "key %s listed twice", id)
	}

	pub := key.PublicKey()

	i, dup := slices.BinarySearchFunc(order, pub, func(k int, pub []byte) int {
		return bytes.Compare(b.keys[k].key.PublicKey(), pub)
	})
	if dup {
		return order, errRule(root, at, "key %s repeats another key's public key", id)
	}

	pos := len(b.keys)
	b.index[id] = pos
	b.keys = append(b.keys, policyKey{key: key, party: party})

	return slices.Insert(order, i, pos), nil
}

// scratch returns n zeroed ints: the first n of stack when they fit, or
// a new slice.
func scratch(stack []int, n int) []int {
	if n <= len(stack) {
		return stack[:n]
	}

	return make([]int, n)
}

// errRule returns [ErrPolicy] for the rule that at locates below root,
// with the reason that format and args describe. The error names the
// path of the rule: the names of the rules from root to it, joined by
// "/". It copies the names into the message, so that no name of the
// tree escapes, and neither does the memory of the tree.
func errRule(root Rule, at []int, format string, args ...any) error {
	var path strings.Builder

	path.WriteString(root.name)

	r := root
	for _, i := range at {
		r = r.children[i]
		path.WriteByte('/')
		path.WriteString(r.name)
	}

	return fmt.Errorf("%w: rule %q: %s", ErrPolicy, path.String(), fmt.Sprintf(format, args...))
}
