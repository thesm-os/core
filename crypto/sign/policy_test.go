// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package sign_test

import (
	"bytes"
	"errors"
	"runtime"
	"slices"
	"sync/atomic"
	"testing"
	"time"
	"weak"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/sign"
	"go.thesmos.sh/core/crypto/sign/ed25519"
	"go.thesmos.sh/core/crypto/sign/mldsa"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/rand"
	"go.thesmos.sh/core/rand/seeded"
)

// The allocation ceilings of the construction of a policy, which the
// docblock of NewPolicyTree states: the index of the keys, the keys and
// the rules.
const (
	// exampleTreeAllocs is the ceiling of NewPolicyTree of the example of
	// C2SP tlog-policy, 6 keys: two for the index, one for the keys and
	// one for the rules.
	exampleTreeAllocs = 4

	// wideTreeAllocs is the ceiling of NewPolicyTree of an AllOf rule of
	// 64 keys: four for the index, one for the keys and one for the
	// rules.
	wideTreeAllocs = 6

	// partiesAllocs is the ceiling of NewPolicy of 5 parties, which
	// builds their rules on the stack.
	partiesAllocs = 4
)

// The goroutines of a concurrent case, and the calls that each makes.
const (
	goroutines = 8
	rounds     = 20
)

var message = []byte("release v1.2.3")

// key is a Verifier test double that counts its Verify calls. A
// signature is valid when it is the key's public key followed by the
// message.
type key struct {
	calls atomic.Int32
	pub   []byte
	alg   crypto.Algorithm
	id    sign.KeyID
}

// newKey returns a key whose public key and KeyID derive from n.
func newKey(n byte) *key {
	return &key{pub: []byte{'k', n}, alg: crypto.AlgEd25519, id: sign.KeyID{n}}
}

// KeyID returns the KeyID of k.
func (k *key) KeyID() sign.KeyID { return k.id }

// PublicKey returns the public key of k.
func (k *key) PublicKey() []byte { return k.pub }

// Algorithm returns the algorithm of k.
func (k *key) Algorithm() crypto.Algorithm { return k.alg }

// Verify counts the call and reports whether sig is the public key of k
// followed by msg.
func (k *key) Verify(msg, sig []byte) bool {
	k.calls.Add(1)

	return len(sig) == len(k.pub)+len(msg) &&
		bytes.Equal(sig[:len(k.pub)], k.pub) &&
		bytes.Equal(sig[len(k.pub):], msg)
}

// signed returns k's valid signature over message.
func (k *key) signed() sign.Signature {
	return sign.Signature{Value: append(bytes.Clone(k.pub), message...), Algorithm: k.alg, KeyID: k.id}
}

// forged returns a signature that names k and does not verify.
func (k *key) forged() sign.Signature {
	return sign.Signature{Value: []byte("forged"), Algorithm: k.alg, KeyID: k.id}
}

// shape is a test-side policy tree, which the reference evaluator
// walks without stopping early. A shape with keys is a key set, and a
// shape without keys is a threshold over its children.
type shape struct {
	keys      []*key
	children  []shape
	threshold int
}

// rule returns s as a sign.Rule.
func (s shape) rule() sign.Rule {
	if len(s.keys) > 0 {
		return allOf("set", s.keys...)
	}

	rules := make([]sign.Rule, len(s.children))
	for i, c := range s.children {
		rules[i] = c.rule()
	}

	return sign.AtLeast("group", s.threshold, rules...)
}

// all returns every key of s in depth-first order.
func (s shape) all() []*key {
	keys := slices.Clone(s.keys)
	for _, c := range s.children {
		keys = append(keys, c.all()...)
	}

	return keys
}

// counts reports whether s counts when the keys in valid, and no
// others, have a counting signature.
func (s shape) counts(valid map[*key]bool) bool {
	if len(s.keys) > 0 {
		for _, k := range s.keys {
			if !valid[k] {
				return false
			}
		}

		return true
	}

	n := 0
	for _, c := range s.children {
		if c.counts(valid) {
			n++
		}
	}

	return n >= s.threshold
}

// satisfied reports whether the root s counts when the keys in valid
// have a counting signature and the parties that contain a key in out
// are excluded. The parties are the children of s, or s itself when it
// is a key set.
func (s shape) satisfied(valid, out map[*key]bool) bool {
	parties, threshold := s.children, s.threshold
	if len(s.keys) > 0 {
		parties, threshold = []shape{s}, 1
	}

	n := 0
	for _, party := range parties {
		excludedKey := slices.ContainsFunc(party.all(), func(k *key) bool { return out[k] })
		if !excludedKey && party.counts(valid) {
			n++
		}
	}

	return n >= threshold
}

func TestPolicy(t *testing.T) {
	t.Parallel()

	t.Run("AllOf", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a rule that a later write to keys leaves unchanged", func(t *testing.T) {
			t.Parallel()
			a := newKey(1)
			keys := []sign.Verifier{a}
			r := sign.AllOf("a", keys...)
			keys[0] = newKey(2)
			assert.NoError(t, mustTree(t, r).Check(message, signedBy(a)),
				"replacing a key in the slice of the caller must not change the rule")
		})
	})

	t.Run("AtLeast", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a rule that a later write to children leaves unchanged", func(t *testing.T) {
			t.Parallel()
			a := newKey(1)
			children := []sign.Rule{allOf("a", a)}
			r := sign.AtLeast("r", 1, children...)
			children[0] = allOf("b", newKey(2))
			assert.NoError(t, mustTree(t, r).Check(message, signedBy(a)),
				"replacing a child in the slice of the caller must not change the rule")
		})

		t.Run("returns a rule outside its own subtree when the caller reuses the slice", func(t *testing.T) {
			t.Parallel()
			a := newKey(1)
			children := []sign.Rule{allOf("a", a)}
			r := sign.AtLeast("r", 1, children...)
			children[0] = r
			assert.NoError(t, mustTree(t, r).Check(message, signedBy(a)),
				"storing a rule in the slice it was built from must not make it its own child")
		})
	})

	t.Run("Rules", func(t *testing.T) {
		t.Parallel()

		t.Run("AllOf", func(t *testing.T) {
			t.Parallel()

			t.Run("returns a rule that counts when every key signs", func(t *testing.T) {
				t.Parallel()
				var rules sign.Rules
				a, b := newKey(1), newKey(2)
				assert.NoError(t, mustTree(t, rules.AllOf("w", a, b)).Check(message, signedBy(a, b)),
					"both keys must satisfy the rule")
			})

			t.Run("returns a rule that does not count when one key is missing", func(t *testing.T) {
				t.Parallel()
				var rules sign.Rules
				a, b := newKey(1), newKey(2)
				assert.ErrorIs(t, mustTree(t, rules.AllOf("w", a, b)).Check(message, signedBy(a)), sign.ErrThreshold,
					"one key must not satisfy the rule")
			})

			t.Run("returns a rule that a later write to keys leaves unchanged", func(t *testing.T) {
				t.Parallel()
				var rules sign.Rules
				a := newKey(1)
				keys := []sign.Verifier{a}
				r := rules.AllOf("a", keys...)
				keys[0] = newKey(2)
				assert.NoError(t, mustTree(t, r).Check(message, signedBy(a)),
					"replacing a key in the slice of the caller must not change the rule")
			})

			t.Run("keeps the keys of the rules built before", func(t *testing.T) {
				t.Parallel()
				var rules sign.Rules
				a, b := newKey(1), newKey(2)
				first := rules.AllOf("a", a)
				second := rules.AllOf("b", b)
				assert.NoError(t, mustTree(t, sign.AtLeast("both", 2, first, second)).Check(message, signedBy(a, b)),
					"each rule must keep its own key")
			})

			t.Run("returns a rule without keys for no keys", func(t *testing.T) {
				t.Parallel()
				var rules sign.Rules
				_, err := sign.NewPolicyTree(rules.AllOf("empty"))
				assert.ErrorIs(t, err, sign.ErrPolicy, "a rule without keys must be refused")
			})
		})

		t.Run("AtLeast", func(t *testing.T) {
			t.Parallel()

			t.Run("returns a rule that counts when the threshold of children count", func(t *testing.T) {
				t.Parallel()
				var rules sign.Rules
				a, b, c := newKey(1), newKey(2), newKey(3)
				p := mustTree(t, rules.AtLeast("two", 2, rules.AllOf("a", a), rules.AllOf("b", b), rules.AllOf("c", c)))
				assert.NoError(t, p.Check(message, signedBy(a, c)), "two children must satisfy the rule")
			})

			t.Run("returns a rule that does not count below the threshold of children", func(t *testing.T) {
				t.Parallel()
				var rules sign.Rules
				a, b, c := newKey(1), newKey(2), newKey(3)
				p := mustTree(t, rules.AtLeast("two", 2, rules.AllOf("a", a), rules.AllOf("b", b), rules.AllOf("c", c)))
				assert.ErrorIs(t, p.Check(message, signedBy(b)), sign.ErrThreshold,
					"one child must not satisfy the rule")
			})

			t.Run("returns a rule that a later write to children leaves unchanged", func(t *testing.T) {
				t.Parallel()
				var rules sign.Rules
				a := newKey(1)
				children := []sign.Rule{rules.AllOf("a", a)}
				r := rules.AtLeast("r", 1, children...)
				children[0] = rules.AllOf("b", newKey(2))
				assert.NoError(t, mustTree(t, r).Check(message, signedBy(a)),
					"replacing a child in the slice of the caller must not change the rule")
			})

			t.Run("keeps the children of the rules built before", func(t *testing.T) {
				t.Parallel()
				var rules sign.Rules
				a, b := newKey(1), newKey(2)
				first := rules.AtLeast("a", 1, rules.AllOf("a", a))
				second := rules.AtLeast("b", 1, rules.AllOf("b", b))
				assert.NoError(t, mustTree(t, rules.AtLeast("both", 2, first, second)).Check(message, signedBy(a, b)),
					"each rule must keep its own children")
			})
		})

		t.Run("Grow", func(t *testing.T) {
			t.Parallel()

			t.Run("leaves the rules built before unchanged", func(t *testing.T) {
				t.Parallel()
				var rules sign.Rules
				a := newKey(1)
				r := rules.AtLeast("r", 1, rules.AllOf("a", a))
				rules.Grow(100, 100)
				assert.NoError(t, mustTree(t, r).Check(message, signedBy(a)),
					"Grow must not change a rule built before")
			})
		})

		t.Run("Reset", func(t *testing.T) {
			t.Parallel()

			t.Run("leaves a Policy built from the rules valid", func(t *testing.T) {
				t.Parallel()
				var rules sign.Rules
				a, b := newKey(1), newKey(2)
				p := mustTree(t, rules.AtLeast("a", 1, rules.AllOf("a", a)))
				rules.Reset()
				mustTree(t, rules.AtLeast("b", 1, rules.AllOf("b", b)))
				assert.NoError(t, p.Check(message, signedBy(a)), "a Policy must not change when its rules are reused")
			})

			t.Run("leaves a Policy built from the rules without the keys of the next tree", func(t *testing.T) {
				t.Parallel()
				var rules sign.Rules
				a, b := newKey(1), newKey(2)
				p := mustTree(t, rules.AtLeast("a", 1, rules.AllOf("a", a)))
				rules.Reset()
				mustTree(t, rules.AtLeast("b", 1, rules.AllOf("b", b)))
				assert.ErrorIs(t, p.Check(message, signedBy(b)), sign.ErrThreshold,
					"a key of the next tree must not count in the Policy")
			})

			t.Run("builds the next tree in the memory of the last", func(t *testing.T) {
				t.Parallel()
				var rules sign.Rules
				a, b := newKey(1), newKey(2)
				first := rules.AllOf("a", a)
				rules.Reset()
				second := rules.AllOf("b", b)
				expect.NoError(t, mustTree(t, first).Check(message, signedBy(b)),
					"a rule from before Reset must read the keys of the rule built after it")
				expect.NoError(t, mustTree(t, second).Check(message, signedBy(b)), "the new rule must count")
			})

			t.Run("drops the keys of the rules that it built", func(t *testing.T) {
				t.Parallel()
				var rules sign.Rules
				dropped := func() weak.Pointer[key] {
					k := newKey(1)
					rules.AllOf("a", k)

					return weak.Make(k)
				}()
				rules.Reset()
				assertCollected(t, dropped, "Reset must not keep a key of the last tree alive")
				runtime.KeepAlive(&rules)
			})

			t.Run("drops the children of the rules that it built", func(t *testing.T) {
				t.Parallel()
				var rules sign.Rules
				dropped := func() weak.Pointer[key] {
					k := newKey(1)
					rules.AtLeast("r", 1, sign.AllOf("a", k))

					return weak.Make(k)
				}()
				rules.Reset()
				assertCollected(t, dropped, "Reset must not keep a child of the last tree alive")
				runtime.KeepAlive(&rules)
			})
		})
	})

	t.Run("NewPolicy", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a Policy for 17 parties, more than fit on the stack", func(t *testing.T) {
			t.Parallel()
			keys := make([]*key, 17)
			for i := range keys {
				keys[i] = newKey(byte(i))
			}
			p := mustPolicy(t, len(keys), solo(keys...)...)
			expect.NoError(t, p.Check(message, signedBy(keys...)), "every party signing must satisfy the policy")
			expect.ErrorIs(t, p.Check(message, signedBy(keys[1:]...)), sign.ErrThreshold,
				"one missing party must fail the policy")
		})

		t.Run("returns a Policy for a threshold from one to the number of parties", func(t *testing.T) {
			t.Parallel()
			prop.NoError(t, func(threshold int) error {
				_, err := sign.NewPolicy(threshold, solo(newKey(1), newKey(2), newKey(3))...)

				return err
			}, "a threshold within range must be accepted", prop.Using(prop.Integer(1, 3)))
		})

		t.Run("returns ErrPolicy for a threshold outside one to the number of parties", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(threshold int) error {
				_, err := sign.NewPolicy(threshold, solo(newKey(1), newKey(2), newKey(3))...)

				return err
			}, sign.ErrPolicy, "a threshold out of range must be refused",
				prop.Using(prop.Integer(-8, 11).Filter(func(n int) bool { return n < 1 || n > 3 })),
				prop.Example(-1), prop.Example(0), prop.Example(4))
		})

		t.Run("returns an error of class Invalid for a threshold out of range", func(t *testing.T) {
			t.Parallel()
			_, err := sign.NewPolicy(0, solo(newKey(1))...)
			assert.Equal(t, errs.Classify(err), errs.Invalid, "ErrPolicy must classify as Invalid")
		})

		refused := []struct {
			name    string
			parties []sign.Party
		}{
			{name: "returns ErrPolicy for no parties"},
			{name: "returns ErrPolicy for a party with no keys", parties: []sign.Party{{Name: "empty"}}},
			{name: "returns ErrPolicy for a nil key", parties: []sign.Party{{Name: "nil", Keys: []sign.Verifier{nil}}}},
		}
		for _, tt := range refused {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, err := sign.NewPolicy(1, tt.parties...)
				assert.ErrorIs(t, err, sign.ErrPolicy, "NewPolicy must refuse the parties")
			})
		}

		t.Run("returns ErrPolicy for one key in two parties", func(t *testing.T) {
			t.Parallel()
			a := newKey(1)
			_, err := sign.NewPolicy(1, solo(a, newKey(2), a)...)
			assert.ErrorIs(t, err, sign.ErrPolicy, "one key in two parties must be refused")
		})

		t.Run("returns ErrPolicy for one key twice in a party", func(t *testing.T) {
			t.Parallel()
			a := newKey(1)
			_, err := sign.NewPolicy(1, sign.Party{Name: "twice", Keys: []sign.Verifier{a, a}})
			assert.ErrorIs(t, err, sign.ErrPolicy, "one key twice in a party must be refused")
		})

		t.Run("returns ErrPolicy for a public key under a second KeyID", func(t *testing.T) {
			t.Parallel()
			a := newKey(1)
			alias := &key{pub: a.pub, alg: a.alg, id: sign.KeyID{0xFF}}
			_, err := sign.NewPolicy(1, solo(a, alias)...)
			assert.ErrorIs(t, err, sign.ErrPolicy, "one public key under two KeyIDs must be refused")
		})

		t.Run("returns a Policy that a later write to the parties leaves unchanged", func(t *testing.T) {
			t.Parallel()
			a := newKey(1)
			parties := solo(a)
			p := mustPolicy(t, 1, parties...)
			parties[0].Keys[0] = newKey(2)
			assert.NoError(t, p.Check(message, signedBy(a)),
				"replacing a key in the slice of the caller must not change the policy")
		})
	})

	t.Run("NewPolicyTree", func(t *testing.T) {
		t.Parallel()

		a := newKey(1)
		alias := &key{pub: a.pub, alg: a.alg, id: sign.KeyID{0xFF}}

		// Each give is a rule whose rule at path is invalid for reason.
		tests := []struct {
			name   string
			give   sign.Rule
			path   string
			reason string
		}{
			{
				name:   "a threshold of 0",
				give:   sign.AtLeast("bad", 0, allOf("a", a)),
				path:   "bad",
				reason: "threshold 0 of 1 rules",
			},
			{
				name:   "a threshold above the number of children",
				give:   sign.AtLeast("bad", 2, allOf("a", a)),
				path:   "bad",
				reason: "threshold 2 of 1 rules",
			},
			{
				name:   "an AllOf rule without keys",
				give:   sign.AllOf("bad"),
				path:   "bad",
				reason: "neither keys nor children",
			},
			{
				name:   "an AtLeast rule without children",
				give:   sign.AtLeast("bad", 1),
				path:   "bad",
				reason: "neither keys nor children",
			},
			{
				name:   "the zero Rule",
				give:   sign.Rule{},
				path:   "",
				reason: "neither keys nor children",
			},
			{
				name:   "a nil key",
				give:   sign.AllOf("bad", nil),
				path:   "bad",
				reason: "a nil key",
			},
			{
				name:   "one KeyID in two branches",
				give:   sign.AtLeast("bad", 1, allOf("x", a), allOf("y", a)),
				path:   "bad/y",
				reason: "key " + a.id.String() + " listed twice",
			},
			{
				name:   "one public key under two KeyIDs in two branches",
				give:   sign.AtLeast("bad", 1, allOf("x", a), allOf("y", alias)),
				path:   "bad/y",
				reason: "key " + alias.id.String() + " repeats another key's public key",
			},
		}
		for _, tt := range tests {
			t.Run("returns ErrPolicy for "+tt.name+" at the root", func(t *testing.T) {
				t.Parallel()
				_, err := sign.NewPolicyTree(tt.give)
				assert.ErrorIs(t, err, sign.ErrPolicy, "an invalid root must be refused")
				assert.Contains(t, err.Error(), `rule "`+tt.path+`": `+tt.reason,
					"the error must name the rule and the reason")
			})

			t.Run("returns ErrPolicy for "+tt.name+" three levels below the root", func(t *testing.T) {
				t.Parallel()
				nested := sign.AtLeast("l1", 1, sign.AtLeast("l2", 1, sign.AtLeast("l3", 1, tt.give)))
				_, err := sign.NewPolicyTree(nested)
				assert.ErrorIs(t, err, sign.ErrPolicy, "an invalid rule below the root must be refused")
				assert.Contains(t, err.Error(), `rule "l1/l2/l3/`+tt.path+`": `+tt.reason,
					"the error must name the path of the rule and the reason")
			})
		}

		t.Run("returns an error of class Invalid for an invalid tree", func(t *testing.T) {
			t.Parallel()
			_, err := sign.NewPolicyTree(sign.Rule{})
			assert.Equal(t, errs.Classify(err), errs.Invalid, "ErrPolicy must classify as Invalid")
		})

		t.Run("returns a Policy for a chain of 64 rules", func(t *testing.T) {
			t.Parallel()
			k := newKey(1)
			assert.NoError(t, mustTree(t, chain(64, k)).Check(message, signedBy(k)),
				"a tree 64 rules deep must be accepted and checked")
		})

		t.Run("returns ErrPolicy for a chain of 65 rules", func(t *testing.T) {
			t.Parallel()
			_, err := sign.NewPolicyTree(chain(65, newKey(1)))
			assert.ErrorIs(t, err, sign.ErrPolicy, "a tree 65 rules deep must be refused")
			assert.Contains(t, err.Error(), "deeper than 64 rules", "the error must state the bound")
		})
	})

	t.Run("Reset", func(t *testing.T) {
		t.Parallel()

		t.Run("sets p to the policy of each tree in turn", func(t *testing.T) {
			t.Parallel()
			a, b, c := newKey(1), newKey(2), newKey(3)
			first := sign.AtLeast("two", 2, allOf("a", a), allOf("b", b), allOf("c", c))
			var p sign.Policy
			assert.NoError(t, p.Reset(first), "Reset must accept the first tree")
			expect.NoError(t, p.Check(message, signedBy(a, b)), "two of three must satisfy the first tree")

			assert.NoError(t, p.Reset(allOf("c", c)), "Reset must accept the second tree")
			expect.NoError(t, p.Check(message, signedBy(c)), "c must satisfy the second tree")
			expect.ErrorIs(t, p.Check(message, signedBy(a, b)), sign.ErrThreshold,
				"the keys of the first tree must not count in the second")

			assert.NoError(t, p.Reset(first), "Reset must accept the first tree again")
			expect.NoError(t, p.Check(message, signedBy(b, c)), "two of three must satisfy the first tree again")
		})

		t.Run("returns the errors of NewPolicyTree", func(t *testing.T) {
			t.Parallel()
			a := newKey(1)
			var p sign.Policy
			err := p.Reset(sign.AtLeast("bad", 1, allOf("x", a), allOf("y", a)))
			assert.ErrorIs(t, err, sign.ErrPolicy, "Reset must refuse a key listed twice")
			assert.Contains(t, err.Error(), `rule "bad/y": key `+a.id.String()+" listed twice",
				"the error must name the rule and the reason")
		})

		refused := []struct {
			name string
			give func(a *key) sign.Rule
		}{
			{
				name: "checks nothing after a tree that fails the check of its shape",
				give: func(*key) sign.Rule { return sign.AtLeast("bad", 0) },
			},
			{
				name: "checks nothing after a tree that repeats a key",
				give: func(a *key) sign.Rule { return sign.AtLeast("bad", 1, allOf("x", a), allOf("y", a)) },
			},
		}
		for _, tt := range refused {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				a := newKey(1)
				var p sign.Policy
				assert.NoError(t, p.Reset(allOf("a", a)), "Reset must accept the first tree")
				assert.HasError(t, p.Reset(tt.give(a)), "Reset must refuse the second tree")
				assert.ErrorIs(t, p.Check(message, signedBy(a)), sign.ErrPolicy,
					"a Policy whose Reset failed must check nothing")
			})
		}

		t.Run("checks again after a tree that it refused", func(t *testing.T) {
			t.Parallel()
			a := newKey(1)
			var p sign.Policy
			assert.HasError(t, p.Reset(sign.AtLeast("bad", 0)), "Reset must refuse the tree")
			assert.NoError(t, p.Reset(allOf("a", a)), "Reset must accept a tree after an error")
			assert.NoError(t, p.Check(message, signedBy(a)), "the Policy must check again")
		})

		t.Run("drops the keys of the tree that it replaced", func(t *testing.T) {
			t.Parallel()
			var p sign.Policy
			dropped := func() weak.Pointer[key] {
				k := newKey(2)
				assert.NoError(t, p.Reset(allOf("two", newKey(1), k)), "Reset must accept the first tree")

				return weak.Make(k)
			}()
			assert.NoError(t, p.Reset(allOf("one", newKey(3))), "Reset must accept the second tree")
			assertCollected(t, dropped, "Reset must not keep a key of the replaced tree alive")
			runtime.KeepAlive(&p)
		})

		t.Run("drops the keys of a tree that it refused", func(t *testing.T) {
			t.Parallel()
			var p sign.Policy
			dropped := func() weak.Pointer[key] {
				k := newKey(1)
				assert.HasError(t, p.Reset(sign.AtLeast("bad", 1, allOf("x", k), allOf("y", k))),
					"Reset must refuse a repeated key")

				return weak.Make(k)
			}()
			assertCollected(t, dropped, "Reset must not keep a key of a refused tree alive")
			runtime.KeepAlive(&p)
		})
	})

	// Check verifies at most one signature per key of the policy, which
	// keeps an attacker who attaches signatures from buying verification
	// work.
	t.Run("Check", func(t *testing.T) {
		t.Parallel()

		t.Run("returns nil for signatures from the threshold of parties", func(t *testing.T) {
			t.Parallel()
			a, b, c := newKey(1), newKey(2), newKey(3)
			assert.NoError(t, mustPolicy(t, 2, solo(a, b, c)...).Check(message, signedBy(c, a)),
				"two of three valid signatures must satisfy a threshold of two")
		})

		t.Run("returns the result of one call to goroutines that check at once", func(t *testing.T) {
			t.Parallel()
			a, b, c := newKey(1), newKey(2), newKey(3)
			p := mustPolicy(t, 2, solo(a, b, c)...)
			satisfying, short := signedBy(c, a), signedBy(b)
			outcomes := history.Concurrently(goroutines, 10*time.Second, func(int) (any, error) {
				results := make([]bool, 0, 2*rounds)
				for range rounds {
					results = append(results, p.Check(message, satisfying) == nil,
						errors.Is(p.Check(message, short), sign.ErrThreshold))
				}

				return results, nil
			})
			for _, o := range outcomes {
				assert.True(t, o.Finished, "every goroutine must finish")
				results, _ := o.Output.([]bool)
				assert.Equal(t, results, slices.Repeat([]bool{true}, 2*rounds),
					"every Check must return the result of one call")
			}
		})

		t.Run("counts two signatures from one key once", func(t *testing.T) {
			t.Parallel()
			a, b := newKey(1), newKey(2)
			assert.ErrorIs(t, mustPolicy(t, 2, solo(a, b)...).Check(message, signedBy(a, a)), sign.ErrThreshold,
				"one key must count once")
		})

		t.Run("ignores a signature from a key outside the policy", func(t *testing.T) {
			t.Parallel()
			a, outsider := newKey(1), newKey(9)
			err := mustPolicy(t, 1, solo(a)...).Check(message, signedBy(outsider))
			assert.ErrorIs(t, err, sign.ErrThreshold, "an outside key must not count")
			assert.Equal(t, calls(a, outsider), int32(0), "an outside signature must not be verified")
		})

		t.Run("counts a party with two keys only when both sign", func(t *testing.T) {
			t.Parallel()
			classical, err := ed25519.Generate(seeded.New(rand.Seed(1)))
			assert.NoError(t, err, "ed25519.Generate must succeed")
			postQuantum, err := mldsa.Generate(mldsa.MLDSA65, seeded.New(rand.Seed(2)), "")
			assert.NoError(t, err, "mldsa.Generate must succeed")
			p := mustPolicy(t, 1, sign.Party{Name: "hybrid", Keys: []sign.Verifier{classical, postQuantum}})
			sigs := []sign.Signature{signature(t, classical), signature(t, postQuantum)}
			tampered := []sign.Signature{sigs[0], sigs[1]}
			tampered[1].Value = bytes.Clone(tampered[1].Value)
			tampered[1].Value[0] ^= 1

			expect.NoError(t, p.Check(message, sigs), "both valid signatures must satisfy the hybrid")
			expect.ErrorIs(t, p.Check(message, sigs[:1]), sign.ErrThreshold,
				"the classical signature alone must not satisfy the hybrid")
			expect.ErrorIs(t, p.Check(message, sigs[1:]), sign.ErrThreshold,
				"the post-quantum signature alone must not satisfy the hybrid")
			expect.ErrorIs(t, p.Check(message, tampered), sign.ErrThreshold,
				"an invalid post-quantum signature must not satisfy the hybrid")
		})

		t.Run("does not count a signature whose algorithm differs from its key's", func(t *testing.T) {
			t.Parallel()
			a := newKey(1)
			sig := a.signed()
			sig.Algorithm = crypto.AlgMLDSA44
			err := mustPolicy(t, 1, solo(a)...).Check(message, []sign.Signature{sig})
			assert.ErrorIs(t, err, sign.ErrThreshold, "a signature claiming another algorithm must not count")
			assert.Equal(t, calls(a), int32(0), "a mismatched algorithm must not be verified")
		})

		t.Run("considers only the first signature for a key", func(t *testing.T) {
			t.Parallel()
			a := newKey(1)
			err := mustPolicy(t, 1, solo(a)...).Check(message, []sign.Signature{a.forged(), a.signed()})
			assert.ErrorIs(t, err, sign.ErrThreshold, "a valid signature after an invalid one must not count")
			assert.Equal(t, calls(a), int32(1), "only the first signature must be verified")
		})

		t.Run("runs one verification per key for a thousand signatures", func(t *testing.T) {
			t.Parallel()
			a := newKey(1)
			sigs := make([]sign.Signature, 1000)
			for i := range sigs {
				sigs[i] = a.forged()
			}
			err := mustPolicy(t, 1, solo(a)...).Check(message, sigs)
			assert.ErrorIs(t, err, sign.ErrThreshold, "forged signatures must not count")
			assert.Equal(t, calls(a), int32(1), "a thousand signatures for one key must cost one verification")
		})

		t.Run("stops once the threshold is met", func(t *testing.T) {
			t.Parallel()
			a, b, c := newKey(1), newKey(2), newKey(3)
			assert.NoError(t, mustPolicy(t, 1, solo(a, b, c)...).Check(message, signedBy(a, b, c)),
				"one valid signature must satisfy a threshold of one")
			assert.Equal(t, calls(a, b, c), int32(1), "Check must stop after the first counting party")
		})

		t.Run("stops once the threshold can no longer be met", func(t *testing.T) {
			t.Parallel()
			a, b, c := newKey(1), newKey(2), newKey(3)
			p := mustPolicy(t, 3, solo(a, b, c)...)
			err := p.Check(message, []sign.Signature{a.forged(), b.signed(), c.signed()})
			assert.ErrorIs(t, err, sign.ErrThreshold, "a forged signature must fail a unanimous policy")
			expect.Equal(t, calls(a, b, c), int32(1), "Check must stop once three parties are out of reach")
			expect.Contains(t, err.Error(), "at most 2 of 3 required parties",
				"the error must count the parties that Check did not reach")
		})

		t.Run("verifies nothing for a party missing a signature", func(t *testing.T) {
			t.Parallel()
			a, b := newKey(1), newKey(2)
			err := mustPolicy(t, 1, sign.Party{Name: "pair", Keys: []sign.Verifier{a, b}}).Check(message, signedBy(a))
			assert.ErrorIs(t, err, sign.ErrThreshold, "a party missing a signature must not count")
			assert.Equal(t, calls(a, b), int32(0), "a party missing a signature must cost no verification")
		})

		t.Run("verifies nothing when the parties that signed cannot meet the threshold", func(t *testing.T) {
			t.Parallel()
			a, b, c := newKey(1), newKey(2), newKey(3)
			err := mustPolicy(t, 3, solo(a, b, c)...).Check(message, signedBy(a, b))
			assert.ErrorIs(t, err, sign.ErrThreshold, "two of three parties must fail a unanimous policy")
			expect.Equal(t, calls(a, b, c), int32(0), "a threshold out of reach must cost no verification")
			expect.Contains(t, err.Error(), "at most 2 of 3 required parties",
				"the error must count the parties that signed")
		})

		t.Run("does not count a party with an excluded key", func(t *testing.T) {
			t.Parallel()
			a, b, c := newKey(1), newKey(2), newKey(3)
			err := mustPolicy(t, 2, solo(a, b, c)...).Check(message, signedBy(a, b), a.id)
			assert.ErrorIs(t, err, sign.ErrThreshold, "the own signature of the requester must not count")
			assert.Contains(t, err.Error(), "at most 1 of 2 required parties",
				"the error must not count the excluded party")
		})

		t.Run("counts every party when the excluded party did not sign", func(t *testing.T) {
			t.Parallel()
			a, b, c := newKey(1), newKey(2), newKey(3)
			assert.NoError(t, mustPolicy(t, 2, solo(a, b, c)...).Check(message, signedBy(a, b), c.id),
				"excluding a party that did not sign must not matter")
		})

		t.Run("does not count a hybrid party when one of its keys is excluded", func(t *testing.T) {
			t.Parallel()
			a, b := newKey(1), newKey(2)
			p := mustPolicy(t, 1, sign.Party{Name: "pair", Keys: []sign.Verifier{a, b}})
			assert.ErrorIs(t, p.Check(message, signedBy(a, b), b.id), sign.ErrThreshold,
				"excluding one key must remove the whole party")
		})

		t.Run("removes a party once when two of its keys are excluded", func(t *testing.T) {
			t.Parallel()
			a, b, c, d := newKey(1), newKey(2), newKey(3), newKey(4)
			p := mustPolicy(t, 2,
				sign.Party{Name: "pair", Keys: []sign.Verifier{a, b}},
				sign.Party{Name: "c", Keys: []sign.Verifier{c}},
				sign.Party{Name: "d", Keys: []sign.Verifier{d}},
			)
			assert.NoError(t, p.Check(message, signedBy(c, d), a.id, b.id, a.id),
				"excluding one party through several keys must remove it once")
		})

		t.Run("ignores an excluded key outside the policy", func(t *testing.T) {
			t.Parallel()
			a := newKey(1)
			assert.NoError(t, mustPolicy(t, 1, solo(a)...).Check(message, signedBy(a), newKey(9).id),
				"an outside key in exclude must change nothing")
		})

		t.Run("returns ErrThreshold with the most parties that could count", func(t *testing.T) {
			t.Parallel()
			a, b := newKey(1), newKey(2)
			err := mustPolicy(t, 2, solo(a, b)...).Check(message, signedBy(a))
			assert.ErrorIs(t, err, sign.ErrThreshold, "one of two must not satisfy a threshold of two")
			assert.Contains(t, err.Error(), "at most 1 of 2 required parties",
				"the error must state the most parties that could count")
		})

		t.Run("returns an error of class Integrity below the threshold", func(t *testing.T) {
			t.Parallel()
			a, b := newKey(1), newKey(2)
			err := mustPolicy(t, 2, solo(a, b)...).Check(message, signedBy(a))
			assert.Equal(t, errs.Classify(err), errs.Integrity, "ErrThreshold must classify as Integrity")
		})

		t.Run("returns ErrPolicy for the zero Policy", func(t *testing.T) {
			t.Parallel()
			var p sign.Policy
			assert.ErrorIs(t, p.Check(message, nil), sign.ErrPolicy, "the zero Policy must not accept anything")
		})

		t.Run("returns nil for a unanimous policy of 65 keys that all sign", func(t *testing.T) {
			t.Parallel()
			keys := numberedKeys(65)
			assert.NoError(t, mustPolicy(t, len(keys), solo(keys...)...).Check(message, signedBy(keys...)),
				"every key signing must satisfy a unanimous policy")
		})

		t.Run("returns ErrThreshold for a unanimous policy of 65 keys with one missing", func(t *testing.T) {
			t.Parallel()
			keys := numberedKeys(65)
			assert.ErrorIs(t, mustPolicy(t, len(keys), solo(keys...)...).Check(message, signedBy(keys[1:]...)),
				sign.ErrThreshold, "one missing signature must fail a unanimous policy")
		})

		t.Run("returns nil for an AllOf root whose keys all sign", func(t *testing.T) {
			t.Parallel()
			a, b := newKey(1), newKey(2)
			assert.NoError(t, mustTree(t, allOf("w", a, b)).Check(message, signedBy(a, b)),
				"a key set as the root must count when every key signs")
		})

		t.Run("returns ErrThreshold for an AllOf root with an excluded key", func(t *testing.T) {
			t.Parallel()
			a, b := newKey(1), newKey(2)
			err := mustTree(t, allOf("w", a, b)).Check(message, signedBy(a, b), b.id)
			assert.ErrorIs(t, err, sign.ErrThreshold, "an excluded key must remove the root that is its party")
			expect.Equal(t, calls(a, b), int32(0), "an excluded root must cost no verification")
			expect.Contains(t, err.Error(), "at most 0 of 1 required parties", "the root must be the only party")
		})

		t.Run("returns ErrThreshold for an AllOf root with a forged signature", func(t *testing.T) {
			t.Parallel()
			a, b := newKey(1), newKey(2)
			err := mustTree(t, allOf("w", a, b)).Check(message, []sign.Signature{a.signed(), b.forged()})
			assert.ErrorIs(t, err, sign.ErrThreshold, "a key set with a forged signature must not count")
			assert.Equal(t, calls(a, b), int32(2), "both signatures of the root must be verified")
		})

		t.Run("returns nil for a nested threshold that its children exactly meet", func(t *testing.T) {
			t.Parallel()
			a, b, c := newKey(1), newKey(2), newKey(3)
			inner := sign.AtLeast("inner", 2, allOf("a", a), allOf("b", b), allOf("c", c))
			assert.NoError(t, mustTree(t, sign.AtLeast("root", 1, inner)).Check(message, signedBy(a, b)),
				"two of three below the root must satisfy a threshold of two")
		})

		// witnesses is the example quorum of C2SP tlog-policy: two of three
		// X witnesses and one of three Y witnesses.
		x1, x2, x3, y1, y2, y3 := newKey(1), newKey(2), newKey(3), newKey(4), newKey(5), newKey(6)
		witnesses := mustTree(t, sign.AtLeast("X-and-Y", 2,
			sign.AtLeast("X-witnesses", 2, allOf("X1", x1), allOf("X2", x2), allOf("X3", x3)),
			sign.AtLeast("Y-witnesses", 1, allOf("Y1", y1), allOf("Y2", y2), allOf("Y3", y3)),
		))

		t.Run("returns nil for two X witnesses and one Y witness", func(t *testing.T) {
			t.Parallel()
			assert.NoError(t, witnesses.Check(message, signedBy(x1, x3, y2)),
				"two of three X witnesses and one Y witness must satisfy the quorum")
		})

		t.Run("returns ErrThreshold for one X witness and three Y witnesses", func(t *testing.T) {
			t.Parallel()
			err := witnesses.Check(message, signedBy(y1, y2, y3, x2))
			assert.ErrorIs(t, err, sign.ErrThreshold, "one X witness must not satisfy the X group")
			assert.Contains(t, err.Error(), "at most 1 of 2 required parties",
				"the error must count the groups at the root")
		})

		t.Run("stops once the root of a nested tree counts", func(t *testing.T) {
			t.Parallel()
			root, keys := newQuorum()
			all := make([]*key, 0, 16)
			for o := range keys {
				all = append(all, orgKeys(keys, o)...)
			}
			assert.NoError(t, mustTree(t, root).Check(message, signedBy(all...)),
				"every witness must satisfy the quorum")
			assert.Equal(t, calls(all...), int32(6), "Check must verify the first witness of three organisations alone")
		})

		t.Run("stops once the root of a nested tree can no longer count", func(t *testing.T) {
			t.Parallel()
			root, keys := newQuorum()
			var sigs []sign.Signature
			for o := range 2 {
				for _, k := range orgKeys(keys, o) {
					sigs = append(sigs, k.forged())
				}
			}
			sigs = append(sigs, signedBy(orgKeys(keys, 2)...)...)

			err := mustTree(t, root).Check(message, sigs)
			assert.ErrorIs(t, err, sign.ErrThreshold, "one honest organisation must not satisfy 3 of 4")
			expect.Equal(t, calls(orgKeys(keys, 0)...), int32(2),
				"Check must verify one key of each witness of the first")
			expect.Equal(t, calls(append(orgKeys(keys, 1), orgKeys(keys, 2)...)...), int32(0),
				"Check must stop once the other organisations cannot make three")
			expect.Contains(t, err.Error(), "at most 2 of 3 required parties",
				"the error must count the organisations that Check did not rule out")
		})

		t.Run("verifies nothing when the organisations that signed cannot meet the threshold", func(t *testing.T) {
			t.Parallel()
			root, keys := newQuorum()
			signers := append(orgKeys(keys, 0), orgKeys(keys, 1)...)
			err := mustTree(t, root).Check(message, signedBy(signers...))
			assert.ErrorIs(t, err, sign.ErrThreshold, "two organisations must not satisfy 3 of 4")
			assert.Equal(t, calls(signers...), int32(0), "a root out of reach must cost no verification")
		})

		laptop, token, bob, carol := newKey(11), newKey(12), newKey(13), newKey(14)
		alice := sign.AtLeast("alice", 1, allOf("alice laptop", laptop), allOf("alice token", token))
		approvers := mustTree(t, sign.AtLeast("approvers", 2, alice, allOf("bob", bob), allOf("carol", carol)))

		t.Run("returns nil for one key set of a party with alternatives", func(t *testing.T) {
			t.Parallel()
			assert.NoError(t, approvers.Check(message, signedBy(token, bob)),
				"the token of Alice and Bob must satisfy two of three approvers")
		})

		t.Run("does not count a party whose other key set has an excluded key", func(t *testing.T) {
			t.Parallel()
			assert.ErrorIs(t, approvers.Check(message, signedBy(token, bob), laptop.id), sign.ErrThreshold,
				"Alice must not approve her own request with her token")
		})

		t.Run("counts a party with alternatives when another party is excluded", func(t *testing.T) {
			t.Parallel()
			assert.NoError(t, approvers.Check(message, signedBy(token, bob), carol.id),
				"excluding Carol must not remove Alice")
		})

		t.Run("returns what a reference evaluator returns for generated trees", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Check must return what the reference evaluator returns", func(c *prop.Case) {
				next := byte(0)
				s := drawShape(c, &next, 3)
				keys := s.all()
				byID := make(map[sign.KeyID]*key, len(keys))
				for _, k := range keys {
					byID[k.id] = k
				}

				var sigs []sign.Signature
				for _, k := range keys {
					choice := c.Draw(prop.Integer(0, 3), "signatures of a key")
					if choice == 1 {
						sigs = append(sigs, k.signed())
					}
					if choice >= 2 {
						sigs = append(sigs, k.forged())
					}
					if choice == 3 {
						sigs = append(sigs, k.signed())
					}
				}
				sigs = c.Draw(prop.Permutation(sigs...), "order of the signatures")

				valid := map[*key]bool{}
				seen := map[*key]bool{}
				for _, sig := range sigs {
					k := byID[sig.KeyID]
					if !seen[k] {
						seen[k] = true
						valid[k] = bytes.Equal(sig.Value, k.signed().Value)
					}
				}

				out := map[*key]bool{}
				exclude := make([]sign.KeyID, 0, 2)
				for range c.Draw(prop.Integer(0, 2), "exclusions") {
					k := keys[c.Draw(prop.Integer(0, len(keys)-1), "excluded key")]
					out[k] = true
					exclude = append(exclude, k.id)
				}

				err := mustTree(c, s.rule()).Check(message, sigs, exclude...)
				assert.Equal(c, err == nil, s.satisfied(valid, out), "Check must return what the reference returns")
				for _, k := range keys {
					assert.InRange(c, k.calls.Load(), 0, 1, "Check must verify at most one signature per key")
				}
			})
		})
	})
}

// TestPolicyAllocs checks the allocation contracts of Check, of the
// construction of a policy in new memory, and of Reset and Rules in the
// memory of the last construction. MaxAllocs counts the allocations of
// the whole process, so the test does not run in parallel.
//
//nolint:paralleltest // see above
func TestPolicyAllocs(t *testing.T) {
	keys := numberedKeys(64)
	sigs := signedBy(keys...)
	unanimous := mustPolicy(t, len(keys), solo(keys...)...)
	allButOne := mustPolicy(t, len(keys)-1, solo(keys...)...)
	example, rebuild, satisfying := exampleTree()
	wide := allOf("all", keys...)
	parties := solo(keys[:5]...)

	t.Run("Check", func(t *testing.T) {
		t.Run("allocates nothing for 64 keys", func(t *testing.T) {
			var err error
			expect.MaxAllocs(t, func() { err = unanimous.Check(message, sigs) }, 0, "Check must not allocate")
			assert.NoError(t, err, "the test must measure a check that succeeds")
		})

		t.Run("allocates nothing for 64 keys and an exclusion", func(t *testing.T) {
			var err error
			expect.MaxAllocs(t, func() { err = allButOne.Check(message, sigs, keys[0].id) }, 0,
				"Check with an exclusion must not allocate")
			assert.NoError(t, err, "the test must measure a check that succeeds")
		})
	})

	t.Run("NewPolicyTree", func(t *testing.T) {
		t.Run("allocates the index, the keys and the rules of the example of tlog-policy", func(t *testing.T) {
			var err error
			expect.MaxAllocs(t, func() { _, err = sign.NewPolicyTree(example) }, exampleTreeAllocs,
				"NewPolicyTree must allocate its memory once")
			assert.NoError(t, err, "the test must measure a tree that NewPolicyTree accepts")
		})

		t.Run("allocates the index, the keys and the rules of 64 keys", func(t *testing.T) {
			var err error
			expect.MaxAllocs(t, func() { _, err = sign.NewPolicyTree(wide) }, wideTreeAllocs,
				"NewPolicyTree must allocate its memory once")
			assert.NoError(t, err, "the test must measure a tree that NewPolicyTree accepts")
		})
	})

	t.Run("NewPolicy", func(t *testing.T) {
		var err error
		expect.MaxAllocs(t, func() { _, err = sign.NewPolicy(3, parties...) }, partiesAllocs,
			"NewPolicy must build the rules of 5 parties on the stack")
		assert.NoError(t, err, "the test must measure parties that NewPolicy accepts")
	})

	t.Run("Reset", func(t *testing.T) {
		for _, tt := range []struct {
			name string
			root sign.Rule
		}{
			{name: "allocates nothing in the memory of the example of tlog-policy", root: example},
			{name: "allocates nothing in the memory of 64 keys", root: wide},
		} {
			t.Run(tt.name, func(t *testing.T) {
				var p sign.Policy
				assert.NoError(t, p.Reset(tt.root), "Reset must accept the tree")
				var err error
				expect.MaxAllocs(t, func() { err = p.Reset(tt.root) }, 0, "Reset into its own memory must not allocate")
				assert.NoError(t, err, "the test must measure a Reset that succeeds")
			})
		}
	})

	t.Run("Rules", func(t *testing.T) {
		t.Run("rebuilds a tree in their own memory without allocating", func(t *testing.T) {
			var rules sign.Rules
			var r sign.Rule
			rebuild(&rules)
			expect.MaxAllocs(t, func() { r = rebuild(&rules) }, 0, "Rules must rebuild a tree in their own memory")
			assert.NoError(t, mustTree(t, r).Check(message, signedBy(satisfying...)),
				"the test must measure a tree that counts")
		})

		// The example has 6 keys and 8 children.
		t.Run("builds a tree within the room of Grow without allocating", func(t *testing.T) {
			var r sign.Rule
			expect.MaxAllocsWithSetup(t, func() *sign.Rules {
				rules := &sign.Rules{}
				rules.Grow(6, 8)

				return rules
			}, func(rules *sign.Rules) { r = rebuild(rules) }, 0, "a tree within the room of Grow must not allocate")
			assert.NoError(t, mustTree(t, r).Check(message, signedBy(satisfying...)),
				"the test must measure a tree that counts")
		})
	})
}

// BenchmarkPolicy reports the cost of Check for five policies, of the
// construction of a policy in new memory, and of Reset and Rules in the
// memory of the last construction, and fails when one of them allocates
// more than TestPolicyAllocs allows. The policies of Check are:
//
//   - a threshold of three Ed25519 approvers of five;
//   - a hybrid party of an Ed25519 and an ML-DSA-65 key;
//   - 64 test-double keys, which isolates the bookkeeping from the cost
//     of verification;
//   - 3 of 4 organisations, each with two witnesses of two test-double
//     keys, which Check satisfies with the first witness of three;
//   - 64 test-double keys in 128 rules, the largest policy whose
//     bookkeeping fits on the stack.
func BenchmarkPolicy(b *testing.B) {
	approvers := make([]sign.Party, 5)
	approvals := make([]sign.Signature, 0, 5)
	for i := range approvers {
		s, err := ed25519.Generate(seeded.New(rand.Seed(int64(i + 1))))
		assert.NoError(b, err, "ed25519.Generate must succeed")
		approvers[i] = sign.Party{Name: s.KeyID().String(), Keys: []sign.Verifier{s}}
		approvals = append(approvals, signature(b, s))
	}

	classical, err := ed25519.Generate(seeded.New(rand.Seed(10)))
	assert.NoError(b, err, "ed25519.Generate must succeed")
	postQuantum, err := mldsa.Generate(mldsa.MLDSA65, seeded.New(rand.Seed(11)), "")
	assert.NoError(b, err, "mldsa.Generate must succeed")

	doubles := numberedKeys(64)
	quorum, quorumKeys := newQuorum()
	witnessKeys := make([]*key, 0, 16)
	for o := range quorumKeys {
		witnessKeys = append(witnessKeys, orgKeys(quorumKeys, o)...)
	}

	// Each of 63 keys is a key set below a threshold of one, and the last
	// key is a key set of its own: 128 rules in all.
	deep := make([]sign.Rule, len(doubles))
	for i, k := range doubles[:len(doubles)-1] {
		deep[i] = sign.AtLeast("wrap", 1, allOf("k", k))
	}
	deep[len(deep)-1] = allOf("k", doubles[len(doubles)-1])

	b.Run("Check", func(b *testing.B) {
		for _, tt := range []struct {
			name   string
			policy sign.Policy
			sigs   []sign.Signature
		}{
			{name: "of 3 of 5 Ed25519", policy: mustPolicy(b, 3, approvers...), sigs: approvals},
			{
				name:   "of a hybrid Ed25519 and ML-DSA-65",
				policy: mustPolicy(b, 1, sign.Party{Name: "hybrid", Keys: []sign.Verifier{classical, postQuantum}}),
				sigs:   []sign.Signature{signature(b, classical), signature(b, postQuantum)},
			},
			{
				name:   "of 64 test-double keys",
				policy: mustPolicy(b, len(doubles), solo(doubles...)...),
				sigs:   signedBy(doubles...),
			},
			{
				name:   "of 3 of 4 organisations of test-double witnesses",
				policy: mustTree(b, quorum),
				sigs:   signedBy(witnessKeys...),
			},
			{
				name:   "of 64 test-double keys in 128 rules",
				policy: mustTree(b, sign.AtLeast("root", len(deep), deep...)),
				sigs:   signedBy(doubles...),
			},
		} {
			b.Run(tt.name, func(b *testing.B) {
				var err error

				c := bench.Start(b).MaxAllocs(0)
				defer c.End()

				for c.Loop() {
					err = tt.policy.Check(message, tt.sigs)
				}

				assert.NoError(b, err, "the benchmark must measure a check that succeeds")
			})
		}
	})

	example, rebuild, satisfying := exampleTree()
	wide := allOf("all", doubles...)
	parties := solo(doubles[:5]...)

	b.Run("NewPolicyTree", func(b *testing.B) {
		for _, tt := range []struct {
			name   string
			root   sign.Rule
			allocs uint64
		}{
			{name: "of the example of tlog-policy", root: example, allocs: exampleTreeAllocs},
			{name: "of 64 keys", root: wide, allocs: wideTreeAllocs},
		} {
			b.Run(tt.name, func(b *testing.B) {
				var err error

				c := bench.Start(b).MaxAllocs(tt.allocs)
				defer c.End()

				for c.Loop() {
					_, err = sign.NewPolicyTree(tt.root)
				}

				assert.NoError(b, err, "the benchmark must measure a tree that NewPolicyTree accepts")
			})
		}
	})

	b.Run("NewPolicy", func(b *testing.B) {
		b.Run("of 5 parties", func(b *testing.B) {
			var err error

			c := bench.Start(b).MaxAllocs(partiesAllocs)
			defer c.End()

			for c.Loop() {
				_, err = sign.NewPolicy(3, parties...)
			}

			assert.NoError(b, err, "the benchmark must measure parties that NewPolicy accepts")
		})
	})

	b.Run("Reset", func(b *testing.B) {
		for _, tt := range []struct {
			name string
			root sign.Rule
		}{
			{name: "to the example of tlog-policy", root: example},
			{name: "to 64 keys", root: wide},
		} {
			b.Run(tt.name, func(b *testing.B) {
				var p sign.Policy
				err := p.Reset(tt.root)
				assert.NoError(b, err, "Reset must accept the tree")

				c := bench.Start(b).MaxAllocs(0)
				defer c.End()

				for c.Loop() {
					err = p.Reset(tt.root)
				}

				assert.NoError(b, err, "the benchmark must measure a Reset that succeeds")
			})
		}
	})

	b.Run("Rules", func(b *testing.B) {
		b.Run("of the example of tlog-policy", func(b *testing.B) {
			var rules sign.Rules
			r := rebuild(&rules)

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				r = rebuild(&rules)
			}

			assert.NoError(b, mustTree(b, r).Check(message, signedBy(satisfying...)),
				"the benchmark must measure a tree that counts")
		})
	})
}

// solo returns one single-key party per key.
func solo(keys ...*key) []sign.Party {
	parties := make([]sign.Party, len(keys))
	for i, k := range keys {
		parties[i] = sign.Party{Name: string(k.pub), Keys: []sign.Verifier{k}}
	}

	return parties
}

// allOf returns the AllOf rule over keys.
func allOf(name string, keys ...*key) sign.Rule {
	verifiers := make([]sign.Verifier, len(keys))
	for i, k := range keys {
		verifiers[i] = k
	}

	return sign.AllOf(name, verifiers...)
}

// signedBy returns the valid signature of each key, in order.
func signedBy(keys ...*key) []sign.Signature {
	sigs := make([]sign.Signature, len(keys))
	for i, k := range keys {
		sigs[i] = k.signed()
	}

	return sigs
}

// numberedKeys returns n keys numbered from 0.
func numberedKeys(n int) []*key {
	keys := make([]*key, n)
	for i := range keys {
		keys[i] = newKey(byte(i))
	}

	return keys
}

// calls returns the Verify calls made on keys.
func calls(keys ...*key) int32 {
	var n int32
	for _, k := range keys {
		n += k.calls.Load()
	}

	return n
}

// chain returns a key set of k under n-1 AtLeast rules: a tree n rules
// deep.
func chain(n int, k *key) sign.Rule {
	r := allOf("k", k)
	for range n - 1 {
		r = sign.AtLeast("c", 1, r)
	}

	return r
}

// mustPolicy returns the Policy that NewPolicy builds. It fails tb when
// NewPolicy refuses the parties.
func mustPolicy(tb assert.TB, threshold int, parties ...sign.Party) sign.Policy {
	tb.Helper()

	p, err := sign.NewPolicy(threshold, parties...)
	assert.NoError(tb, err, "NewPolicy must accept the policy")

	return p
}

// mustTree returns the Policy that NewPolicyTree builds. It fails tb when
// NewPolicyTree refuses the tree.
func mustTree(tb assert.TB, root sign.Rule) sign.Policy {
	tb.Helper()

	p, err := sign.NewPolicyTree(root)
	assert.NoError(tb, err, "NewPolicyTree must accept the tree")

	return p
}

// newQuorum returns the rule of 3 of 4 organisations, each of which
// counts when either of its two witnesses cosigns, and the keys of the
// witnesses. A witness is a key set of two keys, as a hybrid witness
// is: keys[o][w] are the keys of witness w of organisation o.
func newQuorum() (root sign.Rule, keys [4][2][2]*key) {
	orgs := make([]sign.Rule, len(keys))
	n := byte(0)
	for o := range keys {
		for w := range keys[o] {
			for i := range keys[o][w] {
				keys[o][w][i] = newKey(n)
				n++
			}
		}

		orgs[o] = sign.AtLeast("org", 1, allOf("w1", keys[o][0][:]...), allOf("w2", keys[o][1][:]...))
	}

	return sign.AtLeast("witnesses", 3, orgs...), keys
}

// orgKeys returns the four keys of organisation o of a quorum.
func orgKeys(keys [4][2][2]*key, o int) []*key {
	return []*key{keys[o][0][0], keys[o][0][1], keys[o][1][0], keys[o][1][1]}
}

// exampleTree returns the example quorum of C2SP tlog-policy, two of
// three X witnesses and one of three Y witnesses, built with AllOf and
// AtLeast. It also returns a function that builds the same tree with the
// Rules that it receives after their Reset, and the keys of X1, X2 and
// Y1, which satisfy the quorum.
func exampleTree() (sign.Rule, func(*sign.Rules) sign.Rule, []*key) {
	x1, x2, x3, y1, y2, y3 := newKey(1), newKey(2), newKey(3), newKey(4), newKey(5), newKey(6)
	example := sign.AtLeast("X-and-Y", 2,
		sign.AtLeast("X-witnesses", 2, allOf("X1", x1), allOf("X2", x2), allOf("X3", x3)),
		sign.AtLeast("Y-witnesses", 1, allOf("Y1", y1), allOf("Y2", y2), allOf("Y3", y3)),
	)
	rebuild := func(rules *sign.Rules) sign.Rule {
		rules.Reset()

		return rules.AtLeast("X-and-Y", 2,
			rules.AtLeast("X-witnesses", 2, rules.AllOf("X1", x1), rules.AllOf("X2", x2), rules.AllOf("X3", x3)),
			rules.AtLeast("Y-witnesses", 1, rules.AllOf("Y1", y1), rules.AllOf("Y2", y2), rules.AllOf("Y3", y3)),
		)
	}

	return example, rebuild, []*key{x1, x2, y1}
}

// drawShape draws a tree of at most depth threshold levels above key sets
// of one or two keys. next numbers the keys, so every key of the tree is
// distinct.
func drawShape(c *prop.Case, next *byte, depth int) shape {
	if depth == 0 || c.Draw(prop.Integer(0, 2), "a key set") == 0 {
		keys := make([]*key, c.Draw(prop.Integer(1, 2), "keys of the set"))
		for i := range keys {
			keys[i] = newKey(*next)
			*next++
		}

		return shape{keys: keys}
	}

	children := make([]shape, c.Draw(prop.Integer(1, 3), "children"))
	for i := range children {
		children[i] = drawShape(c, next, depth-1)
	}

	return shape{children: children, threshold: c.Draw(prop.Integer(1, len(children)), "threshold")}
}

// assertCollected fails tb unless the garbage collector frees the key
// behind dropped within a second.
func assertCollected(tb testing.TB, dropped weak.Pointer[key], msg string) {
	tb.Helper()

	assert.EventuallyTrue(tb, time.Second, func() bool {
		runtime.GC()

		return dropped.Value() == nil
	}, msg)
}

// signature returns s's signature over message. It fails tb when Sign
// fails.
func signature(tb assert.TB, s sign.Signer) sign.Signature {
	tb.Helper()

	value, err := s.Sign(message)
	assert.NoError(tb, err, "Sign must succeed")

	return sign.Signature{Algorithm: s.Algorithm(), Value: value, KeyID: s.KeyID()}
}
