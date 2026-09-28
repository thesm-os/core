// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package sign_test

import (
	"bytes"
	"fmt"
	mrand "math/rand/v2"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/sign"
	"go.thesmos.sh/core/crypto/sign/ed25519"
	"go.thesmos.sh/core/crypto/sign/mldsa"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/rand"
	"go.thesmos.sh/core/rand/seeded"
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

func (k *key) KeyID() sign.KeyID           { return k.id }
func (k *key) PublicKey() []byte           { return k.pub }
func (k *key) Algorithm() crypto.Algorithm { return k.alg }

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

// calls returns the Verify calls made on keys.
func calls(keys ...*key) int32 {
	var n int32
	for _, k := range keys {
		n += k.calls.Load()
	}

	return n
}

func mustPolicy(tb testing.TB, threshold int, parties ...sign.Party) sign.Policy {
	tb.Helper()

	p, err := sign.NewPolicy(threshold, parties...)
	testkit.NoError(tb, err, "NewPolicy must accept the policy")

	return p
}

func mustTree(tb testing.TB, root sign.Rule) sign.Policy {
	tb.Helper()

	p, err := sign.NewPolicyTree(root)
	testkit.NoError(tb, err, "NewPolicyTree must accept the tree")

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

func TestAllOf(t *testing.T) {
	t.Parallel()

	t.Run("copies keys", func(t *testing.T) {
		t.Parallel()
		a := newKey(1)
		keys := []sign.Verifier{a}
		r := sign.AllOf("a", keys...)

		keys[0] = newKey(2)
		testkit.NoError(t, mustTree(t, r).Check(message, signedBy(a)),
			"replacing a key in the caller's slice must not change the rule")
	})
}

func TestAtLeast(t *testing.T) {
	t.Parallel()

	t.Run("copies children", func(t *testing.T) {
		t.Parallel()
		a := newKey(1)
		children := []sign.Rule{allOf("a", a)}
		r := sign.AtLeast("r", 1, children...)

		children[0] = allOf("b", newKey(2))
		testkit.NoError(t, mustTree(t, r).Check(message, signedBy(a)),
			"replacing a child in the caller's slice must not change the rule")
	})

	t.Run("keeps a rule out of its own subtree when the caller reuses the slice", func(t *testing.T) {
		t.Parallel()
		a := newKey(1)
		children := []sign.Rule{allOf("a", a)}
		r := sign.AtLeast("r", 1, children...)

		children[0] = r
		testkit.NoError(t, mustTree(t, r).Check(message, signedBy(a)),
			"storing a rule in the slice it was built from must not make it its own child")
	})
}

func TestNewPolicy(t *testing.T) {
	t.Parallel()

	t.Run("returns a Policy for a threshold from one to the number of parties", func(t *testing.T) {
		t.Parallel()
		for threshold := 1; threshold <= 3; threshold++ {
			_, err := sign.NewPolicy(threshold, solo(newKey(1), newKey(2), newKey(3))...)
			testkit.NoError(t, err, "a threshold within range must be accepted")
		}
	})

	t.Run("returns ErrPolicy for a threshold outside one to the number of parties", func(t *testing.T) {
		t.Parallel()
		for _, threshold := range []int{-1, 0, 4} {
			_, err := sign.NewPolicy(threshold, solo(newKey(1), newKey(2), newKey(3))...)
			testkit.ErrorIs(t, err, sign.ErrPolicy, "a threshold out of range must be refused")
			testkit.Equal(t, errs.Classify(err), errs.Invalid, "ErrPolicy must classify as Invalid")
		}
	})

	t.Run("returns ErrPolicy for no parties", func(t *testing.T) {
		t.Parallel()
		_, err := sign.NewPolicy(1)
		testkit.ErrorIs(t, err, sign.ErrPolicy, "a policy without parties must be refused")
	})

	t.Run("returns ErrPolicy for a party with no keys", func(t *testing.T) {
		t.Parallel()
		_, err := sign.NewPolicy(1, sign.Party{Name: "empty"})
		testkit.ErrorIs(t, err, sign.ErrPolicy, "a party with no keys must be refused")
	})

	t.Run("returns ErrPolicy for a nil key", func(t *testing.T) {
		t.Parallel()
		_, err := sign.NewPolicy(1, sign.Party{Name: "nil", Keys: []sign.Verifier{nil}})
		testkit.ErrorIs(t, err, sign.ErrPolicy, "a nil key must be refused")
	})

	t.Run("returns ErrPolicy for one key in two parties", func(t *testing.T) {
		t.Parallel()
		a := newKey(1)
		_, err := sign.NewPolicy(1, solo(a, newKey(2), a)...)
		testkit.ErrorIs(t, err, sign.ErrPolicy, "one key in two parties must be refused")
	})

	t.Run("returns ErrPolicy for one key twice in a party", func(t *testing.T) {
		t.Parallel()
		a := newKey(1)
		_, err := sign.NewPolicy(1, sign.Party{Name: "twice", Keys: []sign.Verifier{a, a}})
		testkit.ErrorIs(t, err, sign.ErrPolicy, "one key twice in a party must be refused")
	})

	t.Run("returns ErrPolicy for a public key under a second KeyID", func(t *testing.T) {
		t.Parallel()
		a := newKey(1)
		alias := &key{pub: a.pub, alg: a.alg, id: sign.KeyID{0xFF}}
		_, err := sign.NewPolicy(1, solo(a, alias)...)
		testkit.ErrorIs(t, err, sign.ErrPolicy, "one public key under two KeyIDs must be refused")
	})

	t.Run("copies the parties' key slices", func(t *testing.T) {
		t.Parallel()
		a := newKey(1)
		parties := solo(a)
		p := mustPolicy(t, 1, parties...)

		parties[0].Keys[0] = newKey(2)
		testkit.NoError(t, p.Check(message, signedBy(a)),
			"replacing a key in the caller's slice must not change the policy")
	})
}

func TestNewPolicyTree(t *testing.T) {
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
			testkit.ErrorIs(t, err, sign.ErrPolicy, "an invalid root must be refused")
			testkit.Equal(t, errs.Classify(err), errs.Invalid, "ErrPolicy must classify as Invalid")
			testkit.True(t, strings.Contains(err.Error(), fmt.Sprintf("rule %q: %s", tt.path, tt.reason)),
				"the error must name the rule and the reason: "+err.Error())
		})

		t.Run("returns ErrPolicy for "+tt.name+" three levels below the root", func(t *testing.T) {
			t.Parallel()
			nested := sign.AtLeast("l1", 1, sign.AtLeast("l2", 1, sign.AtLeast("l3", 1, tt.give)))
			_, err := sign.NewPolicyTree(nested)
			testkit.ErrorIs(t, err, sign.ErrPolicy, "an invalid rule below the root must be refused")
			testkit.True(t, strings.Contains(err.Error(), fmt.Sprintf("rule %q: %s", "l1/l2/l3/"+tt.path, tt.reason)),
				"the error must name the path of the rule and the reason: "+err.Error())
		})
	}

	// chain returns a key set of k under n-1 AtLeast rules: a tree n
	// rules deep.
	chain := func(n int, k *key) sign.Rule {
		r := allOf("k", k)
		for range n - 1 {
			r = sign.AtLeast("c", 1, r)
		}

		return r
	}

	t.Run("returns a Policy for a chain of 64 rules", func(t *testing.T) {
		t.Parallel()
		k := newKey(1)
		testkit.NoError(t, mustTree(t, chain(64, k)).Check(message, signedBy(k)),
			"a tree 64 rules deep must be accepted and checked")
	})

	t.Run("returns ErrPolicy for a chain of 65 rules", func(t *testing.T) {
		t.Parallel()
		_, err := sign.NewPolicyTree(chain(65, newKey(1)))
		testkit.ErrorIs(t, err, sign.ErrPolicy, "a tree 65 rules deep must be refused")
		testkit.True(t, strings.Contains(err.Error(), "deeper than 64 rules"),
			"the error must state the bound: "+err.Error())
	})
}

// TestCheck covers Policy.Check. The bound on verifications is the
// property that keeps an attacker who attaches signatures from buying
// verification work: Check verifies at most one signature per key of
// the policy.
func TestCheck(t *testing.T) {
	t.Parallel()

	t.Run("returns nil for signatures from the threshold of parties", func(t *testing.T) {
		t.Parallel()
		a, b, c := newKey(1), newKey(2), newKey(3)
		p := mustPolicy(t, 2, solo(a, b, c)...)
		testkit.NoError(t, p.Check(message, signedBy(c, a)),
			"two of three valid signatures must satisfy a threshold of two")
	})

	t.Run("counts two signatures from one key once", func(t *testing.T) {
		t.Parallel()
		a, b := newKey(1), newKey(2)
		p := mustPolicy(t, 2, solo(a, b)...)
		err := p.Check(message, signedBy(a, a))
		testkit.ErrorIs(t, err, sign.ErrThreshold, "one key must count once")
	})

	t.Run("ignores a signature from a key outside the policy", func(t *testing.T) {
		t.Parallel()
		a, outsider := newKey(1), newKey(9)
		p := mustPolicy(t, 1, solo(a)...)
		err := p.Check(message, signedBy(outsider))
		testkit.ErrorIs(t, err, sign.ErrThreshold, "an outside key must not count")
		testkit.Equal(t, calls(a, outsider), int32(0), "an outside signature must not be verified")
	})

	t.Run("counts a party with two keys only when both sign", func(t *testing.T) {
		t.Parallel()
		classical, err := ed25519.Generate(seeded.New(rand.Seed(1)))
		testkit.NoError(t, err, "ed25519.Generate must succeed")
		postQuantum, err := mldsa.Generate(mldsa.MLDSA65, seeded.New(rand.Seed(2)), "")
		testkit.NoError(t, err, "mldsa.Generate must succeed")
		p := mustPolicy(t, 1, sign.Party{Name: "hybrid", Keys: []sign.Verifier{classical, postQuantum}})

		sigs := make([]sign.Signature, 0, 2)
		for _, s := range []sign.Signer{classical, postQuantum} {
			value, err := s.Sign(message)
			testkit.NoError(t, err, "Sign must succeed")
			sigs = append(sigs, sign.Signature{Value: value, Algorithm: s.Algorithm(), KeyID: s.KeyID()})
		}

		testkit.NoError(t, p.Check(message, sigs), "both valid signatures must satisfy the hybrid")
		testkit.ErrorIs(t, p.Check(message, sigs[:1]), sign.ErrThreshold,
			"the classical signature alone must not satisfy the hybrid")
		testkit.ErrorIs(t, p.Check(message, sigs[1:]), sign.ErrThreshold,
			"the post-quantum signature alone must not satisfy the hybrid")

		tampered := []sign.Signature{sigs[0], sigs[1]}
		tampered[1].Value = bytes.Clone(tampered[1].Value)
		tampered[1].Value[0] ^= 1
		testkit.ErrorIs(t, p.Check(message, tampered), sign.ErrThreshold,
			"an invalid post-quantum signature must not satisfy the hybrid")
	})

	t.Run("does not count a signature whose algorithm differs from its key's", func(t *testing.T) {
		t.Parallel()
		a := newKey(1)
		p := mustPolicy(t, 1, solo(a)...)
		sig := a.signed()
		sig.Algorithm = crypto.AlgMLDSA44
		testkit.ErrorIs(t, p.Check(message, []sign.Signature{sig}), sign.ErrThreshold,
			"a signature claiming another algorithm must not count")
		testkit.Equal(t, calls(a), int32(0), "a mismatched algorithm must not be verified")
	})

	t.Run("considers only the first signature for a key", func(t *testing.T) {
		t.Parallel()
		a := newKey(1)
		p := mustPolicy(t, 1, solo(a)...)
		err := p.Check(message, []sign.Signature{a.forged(), a.signed()})
		testkit.ErrorIs(t, err, sign.ErrThreshold, "a valid signature after an invalid one must not count")
		testkit.Equal(t, calls(a), int32(1), "only the first signature must be verified")
	})

	t.Run("runs one verification per key for a thousand signatures", func(t *testing.T) {
		t.Parallel()
		a := newKey(1)
		p := mustPolicy(t, 1, solo(a)...)
		sigs := make([]sign.Signature, 1000)
		for i := range sigs {
			sigs[i] = a.forged()
		}

		testkit.ErrorIs(t, p.Check(message, sigs), sign.ErrThreshold, "forged signatures must not count")
		testkit.Equal(t, calls(a), int32(1), "a thousand signatures for one key must cost one verification")
	})

	t.Run("stops once the threshold is met", func(t *testing.T) {
		t.Parallel()
		a, b, c := newKey(1), newKey(2), newKey(3)
		p := mustPolicy(t, 1, solo(a, b, c)...)
		testkit.NoError(t, p.Check(message, signedBy(a, b, c)),
			"one valid signature must satisfy a threshold of one")
		testkit.Equal(t, calls(a, b, c), int32(1), "Check must stop after the first counting party")
	})

	t.Run("stops once the threshold can no longer be met", func(t *testing.T) {
		t.Parallel()
		a, b, c := newKey(1), newKey(2), newKey(3)
		p := mustPolicy(t, 3, solo(a, b, c)...)
		err := p.Check(message, []sign.Signature{a.forged(), b.signed(), c.signed()})
		testkit.ErrorIs(t, err, sign.ErrThreshold, "a forged signature must fail a unanimous policy")
		testkit.Equal(t, calls(a, b, c), int32(1), "Check must stop once three parties are out of reach")
		testkit.True(t, strings.Contains(err.Error(), "at most 2 of 3 required parties"),
			"the error must count the parties Check did not reach: "+err.Error())
	})

	t.Run("verifies nothing for a party missing a signature", func(t *testing.T) {
		t.Parallel()
		a, b := newKey(1), newKey(2)
		p := mustPolicy(t, 1, sign.Party{Name: "pair", Keys: []sign.Verifier{a, b}})
		testkit.ErrorIs(t, p.Check(message, signedBy(a)), sign.ErrThreshold,
			"a party missing a signature must not count")
		testkit.Equal(t, calls(a, b), int32(0), "a party missing a signature must cost no verification")
	})

	t.Run("verifies nothing when the parties that signed cannot meet the threshold", func(t *testing.T) {
		t.Parallel()
		a, b, c := newKey(1), newKey(2), newKey(3)
		p := mustPolicy(t, 3, solo(a, b, c)...)
		err := p.Check(message, signedBy(a, b))
		testkit.ErrorIs(t, err, sign.ErrThreshold, "two of three parties must fail a unanimous policy")
		testkit.Equal(t, calls(a, b, c), int32(0), "a threshold out of reach must cost no verification")
		testkit.True(t, strings.Contains(err.Error(), "at most 2 of 3 required parties"),
			"the error must count the parties that signed: "+err.Error())
	})

	t.Run("does not count a party with an excluded key", func(t *testing.T) {
		t.Parallel()
		a, b, c := newKey(1), newKey(2), newKey(3)
		p := mustPolicy(t, 2, solo(a, b, c)...)
		sigs := signedBy(a, b)

		err := p.Check(message, sigs, a.id)
		testkit.ErrorIs(t, err, sign.ErrThreshold, "the requester's own signature must not count")
		testkit.True(t, strings.Contains(err.Error(), "at most 1 of 2 required parties"),
			"the error must not count the excluded party: "+err.Error())
		testkit.NoError(t, p.Check(message, sigs, c.id), "excluding a party that did not sign must not matter")
	})

	t.Run("does not count a hybrid party when one of its keys is excluded", func(t *testing.T) {
		t.Parallel()
		a, b := newKey(1), newKey(2)
		p := mustPolicy(t, 1, sign.Party{Name: "pair", Keys: []sign.Verifier{a, b}})
		testkit.ErrorIs(t, p.Check(message, signedBy(a, b), b.id), sign.ErrThreshold,
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
		testkit.NoError(t, p.Check(message, signedBy(c, d), a.id, b.id, a.id),
			"excluding one party through several keys must remove it once")
	})

	t.Run("ignores an excluded key outside the policy", func(t *testing.T) {
		t.Parallel()
		a := newKey(1)
		p := mustPolicy(t, 1, solo(a)...)
		testkit.NoError(t, p.Check(message, signedBy(a), newKey(9).id),
			"an outside key in exclude must change nothing")
	})

	t.Run("returns ErrThreshold classified Integrity with the reachable count", func(t *testing.T) {
		t.Parallel()
		a, b := newKey(1), newKey(2)
		p := mustPolicy(t, 2, solo(a, b)...)
		err := p.Check(message, signedBy(a))
		testkit.ErrorIs(t, err, sign.ErrThreshold, "one of two must not satisfy a threshold of two")
		testkit.Equal(t, errs.Classify(err), errs.Integrity, "ErrThreshold must classify as Integrity")
		testkit.True(t, strings.Contains(err.Error(), "at most 1 of 2 required parties"),
			"the error must state the most parties that could count: "+err.Error())
	})

	t.Run("returns ErrPolicy for the zero Policy", func(t *testing.T) {
		t.Parallel()
		var p sign.Policy
		err := p.Check(message, nil)
		testkit.ErrorIs(t, err, sign.ErrPolicy, "the zero Policy must not accept anything")
		testkit.Equal(t, errs.Classify(err), errs.Invalid, "ErrPolicy must classify as Invalid")
	})

	t.Run("returns nil for a unanimous policy of 65 keys that all sign", func(t *testing.T) {
		t.Parallel()
		keys := make([]*key, 65)
		for i := range keys {
			keys[i] = newKey(byte(i))
		}

		p := mustPolicy(t, len(keys), solo(keys...)...)
		testkit.NoError(t, p.Check(message, signedBy(keys...)), "every key signing must satisfy a unanimous policy")
	})

	t.Run("returns ErrThreshold for a unanimous policy of 65 keys with one missing", func(t *testing.T) {
		t.Parallel()
		keys := make([]*key, 65)
		for i := range keys {
			keys[i] = newKey(byte(i))
		}

		p := mustPolicy(t, len(keys), solo(keys...)...)
		testkit.ErrorIs(t, p.Check(message, signedBy(keys[1:]...)), sign.ErrThreshold,
			"one missing signature must fail a unanimous policy")
	})

	t.Run("returns nil for an AllOf root whose keys all sign", func(t *testing.T) {
		t.Parallel()
		a, b := newKey(1), newKey(2)
		testkit.NoError(t, mustTree(t, allOf("w", a, b)).Check(message, signedBy(a, b)),
			"a key set as the root must count when every key signs")
	})

	t.Run("returns ErrThreshold for an AllOf root with an excluded key", func(t *testing.T) {
		t.Parallel()
		a, b := newKey(1), newKey(2)
		err := mustTree(t, allOf("w", a, b)).Check(message, signedBy(a, b), b.id)
		testkit.ErrorIs(t, err, sign.ErrThreshold, "an excluded key must remove the root that is its party")
		testkit.Equal(t, calls(a, b), int32(0), "an excluded root must cost no verification")
		testkit.True(t, strings.Contains(err.Error(), "at most 0 of 1 required parties"),
			"the root must be the only party: "+err.Error())
	})

	t.Run("returns ErrThreshold for an AllOf root with a forged signature", func(t *testing.T) {
		t.Parallel()
		a, b := newKey(1), newKey(2)
		err := mustTree(t, allOf("w", a, b)).Check(message, []sign.Signature{a.signed(), b.forged()})
		testkit.ErrorIs(t, err, sign.ErrThreshold, "a key set with a forged signature must not count")
		testkit.Equal(t, calls(a, b), int32(2), "both signatures of the root must be verified")
	})

	t.Run("returns nil for a nested threshold that its children exactly meet", func(t *testing.T) {
		t.Parallel()
		a, b, c := newKey(1), newKey(2), newKey(3)
		inner := sign.AtLeast("inner", 2, allOf("a", a), allOf("b", b), allOf("c", c))
		testkit.NoError(t, mustTree(t, sign.AtLeast("root", 1, inner)).Check(message, signedBy(a, b)),
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
		testkit.NoError(t, witnesses.Check(message, signedBy(x1, x3, y2)),
			"two of three X witnesses and one Y witness must satisfy the quorum")
	})

	t.Run("returns ErrThreshold for one X witness and three Y witnesses", func(t *testing.T) {
		t.Parallel()
		err := witnesses.Check(message, signedBy(y1, y2, y3, x2))
		testkit.ErrorIs(t, err, sign.ErrThreshold, "one X witness must not satisfy the X group")
		testkit.True(t, strings.Contains(err.Error(), "at most 1 of 2 required parties"),
			"the error must count the groups at the root: "+err.Error())
	})

	t.Run("stops once the root of a nested tree counts", func(t *testing.T) {
		t.Parallel()
		root, keys := newQuorum()
		all := make([]*key, 0, 16)
		for o := range keys {
			all = append(all, orgKeys(keys, o)...)
		}

		testkit.NoError(t, mustTree(t, root).Check(message, signedBy(all...)), "every witness must satisfy the quorum")
		testkit.Equal(t, calls(all...), int32(6), "Check must verify the first witness of three organisations alone")
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
		testkit.ErrorIs(t, err, sign.ErrThreshold, "one honest organisation must not satisfy 3 of 4")
		testkit.Equal(t, calls(orgKeys(keys, 0)...), int32(2), "Check must verify one key of each witness of the first")
		testkit.Equal(t, calls(append(orgKeys(keys, 1), orgKeys(keys, 2)...)...), int32(0),
			"Check must stop once the other organisations cannot make three")
		testkit.True(t, strings.Contains(err.Error(), "at most 2 of 3 required parties"),
			"the error must count the organisations Check did not rule out: "+err.Error())
	})

	t.Run("verifies nothing when the organisations that signed cannot meet the threshold", func(t *testing.T) {
		t.Parallel()
		root, keys := newQuorum()
		signers := append(orgKeys(keys, 0), orgKeys(keys, 1)...)
		err := mustTree(t, root).Check(message, signedBy(signers...))
		testkit.ErrorIs(t, err, sign.ErrThreshold, "two organisations must not satisfy 3 of 4")
		testkit.Equal(t, calls(signers...), int32(0), "a root out of reach must cost no verification")
	})

	laptop, token, bob, carol := newKey(11), newKey(12), newKey(13), newKey(14)
	alice := sign.AtLeast("alice", 1, allOf("alice laptop", laptop), allOf("alice token", token))
	approvers := mustTree(t, sign.AtLeast("approvers", 2, alice, allOf("bob", bob), allOf("carol", carol)))

	t.Run("returns nil for one key set of a party with alternatives", func(t *testing.T) {
		t.Parallel()
		testkit.NoError(t, approvers.Check(message, signedBy(token, bob)),
			"Alice's token and Bob must satisfy two of three approvers")
	})

	t.Run("does not count a party whose other key set has an excluded key", func(t *testing.T) {
		t.Parallel()
		err := approvers.Check(message, signedBy(token, bob), laptop.id)
		testkit.ErrorIs(t, err, sign.ErrThreshold, "Alice must not approve her own request with her token")
	})

	t.Run("counts a party with alternatives when another party is excluded", func(t *testing.T) {
		t.Parallel()
		testkit.NoError(t, approvers.Check(message, signedBy(token, bob), carol.id),
			"excluding Carol must not remove Alice")
	})

	t.Run("returns what a reference evaluator returns for random trees", func(t *testing.T) {
		t.Parallel()
		r := testkit.SeededRand(t)

		for range 500 {
			next := byte(0)
			s := randomShape(r, &next, 3)
			keys := s.all()
			byID := make(map[sign.KeyID]*key, len(keys))
			for _, k := range keys {
				byID[k.id] = k
			}

			var sigs []sign.Signature
			for _, k := range keys {
				choice := r.IntN(4)
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

			r.Shuffle(len(sigs), func(i, j int) { sigs[i], sigs[j] = sigs[j], sigs[i] })

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
			for range r.IntN(3) {
				k := keys[r.IntN(len(keys))]
				out[k] = true
				exclude = append(exclude, k.id)
			}

			err := mustTree(t, s.rule()).Check(message, sigs, exclude...)
			testkit.Equal(t, err == nil, s.satisfied(valid, out), "Check must return what the reference returns")

			for _, k := range keys {
				testkit.True(t, k.calls.Load() <= 1, "Check must verify at most one signature per key")
			}
		}
	})
}

// shape is a test-side policy tree, which the reference evaluator
// walks without stopping early. A shape with keys is a key set, and a
// shape without keys is a threshold over its children.
type shape struct {
	keys      []*key
	children  []shape
	threshold int
}

// randomShape returns a random tree of at most depth threshold levels
// above key sets of one or two keys. next numbers the keys, so every
// key of the tree is distinct.
func randomShape(r *mrand.Rand, next *byte, depth int) shape {
	if depth == 0 || r.IntN(3) == 0 {
		keys := make([]*key, 1+r.IntN(2))
		for i := range keys {
			keys[i] = newKey(*next)
			*next++
		}

		return shape{keys: keys}
	}

	children := make([]shape, 1+r.IntN(3))
	for i := range children {
		children[i] = randomShape(r, next, depth-1)
	}

	return shape{children: children, threshold: 1 + r.IntN(len(children))}
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

// BenchmarkCheck measures Policy.Check for five policies:
//
//   - a threshold of three Ed25519 approvers of five;
//   - a hybrid party of an Ed25519 and an ML-DSA-65 key;
//   - 64 test-double keys, which isolates the bookkeeping from the cost
//     of verification;
//   - 3 of 4 organisations, each with two witnesses of two test-double
//     keys, which Check satisfies with the first witness of three;
//   - 64 test-double keys in 128 rules, the largest policy whose
//     bookkeeping fits on the stack.
func BenchmarkCheck(b *testing.B) {
	approvers := make([]sign.Party, 5)
	approvals := make([]sign.Signature, 0, 5)
	for i := range approvers {
		s, err := ed25519.Generate(seeded.New(rand.Seed(int64(i + 1))))
		testkit.NoError(b, err, "ed25519.Generate must succeed")
		approvers[i] = sign.Party{Name: s.KeyID().String(), Keys: []sign.Verifier{s}}
		approvals = append(approvals, signature(b, s))
	}

	classical, err := ed25519.Generate(seeded.New(rand.Seed(10)))
	testkit.NoError(b, err, "ed25519.Generate must succeed")
	postQuantum, err := mldsa.Generate(mldsa.MLDSA65, seeded.New(rand.Seed(11)), "")
	testkit.NoError(b, err, "mldsa.Generate must succeed")

	doubles := make([]*key, 64)
	for i := range doubles {
		doubles[i] = newKey(byte(i))
	}

	quorum, quorumKeys := newQuorum()
	witnessKeys := make([]*key, 0, 16)
	for o := range quorumKeys {
		witnessKeys = append(witnessKeys, orgKeys(quorumKeys, o)...)
	}

	// Each of 63 keys is a key set below a threshold of one, and the
	// last key is a key set of its own: 128 rules in all.
	deep := make([]sign.Rule, len(doubles))
	for i, k := range doubles[:len(doubles)-1] {
		deep[i] = sign.AtLeast("wrap", 1, allOf("k", k))
	}
	deep[len(deep)-1] = allOf("k", doubles[len(doubles)-1])

	cases := []struct {
		name   string
		policy sign.Policy
		sigs   []sign.Signature
	}{
		{"3 of 5 Ed25519", mustPolicy(b, 3, approvers...), approvals},
		{
			"hybrid Ed25519 and ML-DSA-65",
			mustPolicy(b, 1, sign.Party{Name: "hybrid", Keys: []sign.Verifier{classical, postQuantum}}),
			[]sign.Signature{signature(b, classical), signature(b, postQuantum)},
		},
		{"64 test-double keys", mustPolicy(b, len(doubles), solo(doubles...)...), signedBy(doubles...)},
		{"3 of 4 organisations of test-double witnesses", mustTree(b, quorum), signedBy(witnessKeys...)},
		{
			"64 test-double keys in 128 rules",
			mustTree(b, sign.AtLeast("root", len(deep), deep...)),
			signedBy(doubles...),
		},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				_ = tc.policy.Check(message, tc.sigs)
			}
		})
	}
}

// signature returns s's signature over message.
func signature(tb testing.TB, s sign.Signer) sign.Signature {
	tb.Helper()

	value, err := s.Sign(message)
	testkit.NoError(tb, err, "Sign must succeed")

	return sign.Signature{Algorithm: s.Algorithm(), Value: value, KeyID: s.KeyID()}
}

// TestZeroAlloc enforces the allocation contract of Policy.Check for a
// policy of 64 keys. testing.AllocsPerRun reads a process-global
// malloc counter, so this test does not call t.Parallel.
//
//nolint:paralleltest // see comment above
func TestZeroAlloc(t *testing.T) {
	keys := make([]*key, 64)
	for i := range keys {
		keys[i] = newKey(byte(i))
	}

	sigs := signedBy(keys...)
	unanimous := mustPolicy(t, len(keys), solo(keys...)...)
	allButOne := mustPolicy(t, len(keys)-1, solo(keys...)...)
	excluded := keys[0].id

	tests := []struct {
		fn   func()
		name string
	}{
		{func() { _ = unanimous.Check(message, sigs) }, "allocates nothing for 64 keys"},
		{func() { _ = allButOne.Check(message, sigs, excluded) }, "allocates nothing for 64 keys and an exclusion"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testkit.Equal(t, testing.AllocsPerRun(20, tt.fn), float64(0), tt.name+" must not allocate")
		})
	}
}
