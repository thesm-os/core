// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package sign_test

import (
	"bytes"
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

func TestNewPolicy(t *testing.T) {
	t.Parallel()

	t.Run("accepts a threshold from one to the number of parties", func(t *testing.T) {
		t.Parallel()
		for threshold := 1; threshold <= 3; threshold++ {
			_, err := sign.NewPolicy(threshold, solo(newKey(1), newKey(2), newKey(3))...)
			testkit.NoError(t, err, "a threshold within range must be accepted")
		}
	})

	t.Run("refuses a threshold outside one to the number of parties", func(t *testing.T) {
		t.Parallel()
		for _, threshold := range []int{-1, 0, 4} {
			_, err := sign.NewPolicy(threshold, solo(newKey(1), newKey(2), newKey(3))...)
			testkit.ErrorIs(t, err, sign.ErrPolicy, "a threshold out of range must be refused")
			testkit.Equal(t, errs.Classify(err), errs.Invalid, "ErrPolicy must classify as Invalid")
		}
	})

	t.Run("refuses a policy without parties", func(t *testing.T) {
		t.Parallel()
		_, err := sign.NewPolicy(1)
		testkit.ErrorIs(t, err, sign.ErrPolicy, "a policy without parties must be refused")
	})

	t.Run("refuses a party with no keys", func(t *testing.T) {
		t.Parallel()
		_, err := sign.NewPolicy(1, sign.Party{Name: "empty"})
		testkit.ErrorIs(t, err, sign.ErrPolicy, "a party with no keys must be refused")
	})

	t.Run("refuses a nil key", func(t *testing.T) {
		t.Parallel()
		_, err := sign.NewPolicy(1, sign.Party{Name: "nil", Keys: []sign.Verifier{nil}})
		testkit.ErrorIs(t, err, sign.ErrPolicy, "a nil key must be refused")
	})

	t.Run("refuses one key in two parties", func(t *testing.T) {
		t.Parallel()
		a := newKey(1)
		_, err := sign.NewPolicy(1, solo(a, newKey(2), a)...)
		testkit.ErrorIs(t, err, sign.ErrPolicy, "one key in two parties must be refused")
	})

	t.Run("refuses one key twice in a party", func(t *testing.T) {
		t.Parallel()
		a := newKey(1)
		_, err := sign.NewPolicy(1, sign.Party{Name: "twice", Keys: []sign.Verifier{a, a}})
		testkit.ErrorIs(t, err, sign.ErrPolicy, "one key twice in a party must be refused")
	})

	t.Run("refuses a public key listed under a second KeyID", func(t *testing.T) {
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
		testkit.NoError(t, p.Check(message, []sign.Signature{a.signed()}),
			"replacing a key in the caller's slice must not change the policy")
	})
}

// TestCheck covers Policy.Check. The bound on verifications is the
// property that keeps an attacker who attaches signatures from buying
// verification work: Check verifies at most one signature per key of
// the policy.
func TestCheck(t *testing.T) {
	t.Parallel()

	t.Run("accepts signatures from the threshold of parties", func(t *testing.T) {
		t.Parallel()
		a, b, c := newKey(1), newKey(2), newKey(3)
		p := mustPolicy(t, 2, solo(a, b, c)...)
		testkit.NoError(t, p.Check(message, []sign.Signature{c.signed(), a.signed()}),
			"two of three valid signatures must satisfy a threshold of two")
	})

	t.Run("counts two signatures from one key once", func(t *testing.T) {
		t.Parallel()
		a, b := newKey(1), newKey(2)
		p := mustPolicy(t, 2, solo(a, b)...)
		err := p.Check(message, []sign.Signature{a.signed(), a.signed()})
		testkit.ErrorIs(t, err, sign.ErrThreshold, "one key must count once")
	})

	t.Run("ignores a signature from a key outside the policy", func(t *testing.T) {
		t.Parallel()
		a, outsider := newKey(1), newKey(9)
		p := mustPolicy(t, 1, solo(a)...)
		err := p.Check(message, []sign.Signature{outsider.signed()})
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
		testkit.NoError(t, p.Check(message, []sign.Signature{a.signed(), b.signed(), c.signed()}),
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
	})

	t.Run("verifies nothing for a party missing a signature", func(t *testing.T) {
		t.Parallel()
		a, b := newKey(1), newKey(2)
		p := mustPolicy(t, 1, sign.Party{Name: "pair", Keys: []sign.Verifier{a, b}})
		testkit.ErrorIs(t, p.Check(message, []sign.Signature{a.signed()}), sign.ErrThreshold,
			"a party missing a signature must not count")
		testkit.Equal(t, calls(a, b), int32(0), "a party missing a signature must cost no verification")
	})

	t.Run("does not count a party with an excluded key", func(t *testing.T) {
		t.Parallel()
		a, b, c := newKey(1), newKey(2), newKey(3)
		p := mustPolicy(t, 2, solo(a, b, c)...)
		sigs := []sign.Signature{a.signed(), b.signed()}

		testkit.ErrorIs(t, p.Check(message, sigs, a.id), sign.ErrThreshold,
			"the requester's own signature must not count")
		testkit.NoError(t, p.Check(message, sigs, c.id), "excluding a party that did not sign must not matter")
	})

	t.Run("does not count a hybrid party when one of its keys is excluded", func(t *testing.T) {
		t.Parallel()
		a, b := newKey(1), newKey(2)
		p := mustPolicy(t, 1, sign.Party{Name: "pair", Keys: []sign.Verifier{a, b}})
		testkit.ErrorIs(t, p.Check(message, []sign.Signature{a.signed(), b.signed()}, b.id), sign.ErrThreshold,
			"excluding one key must remove the whole party")
	})

	t.Run("counts an excluded party once when two of its keys are excluded", func(t *testing.T) {
		t.Parallel()
		a, b, c, d := newKey(1), newKey(2), newKey(3), newKey(4)
		p := mustPolicy(t, 2,
			sign.Party{Name: "pair", Keys: []sign.Verifier{a, b}},
			sign.Party{Name: "c", Keys: []sign.Verifier{c}},
			sign.Party{Name: "d", Keys: []sign.Verifier{d}},
		)
		testkit.NoError(t, p.Check(message, []sign.Signature{c.signed(), d.signed()}, a.id, b.id, a.id),
			"excluding one party through several keys must remove it once")
	})

	t.Run("ignores an excluded key outside the policy", func(t *testing.T) {
		t.Parallel()
		a := newKey(1)
		p := mustPolicy(t, 1, solo(a)...)
		testkit.NoError(t, p.Check(message, []sign.Signature{a.signed()}, newKey(9).id),
			"an outside key in exclude must change nothing")
	})

	t.Run("returns ErrThreshold classified Integrity with the reachable count", func(t *testing.T) {
		t.Parallel()
		a, b := newKey(1), newKey(2)
		p := mustPolicy(t, 2, solo(a, b)...)
		err := p.Check(message, []sign.Signature{a.signed()})
		testkit.ErrorIs(t, err, sign.ErrThreshold, "one of two must not satisfy a threshold of two")
		testkit.Equal(t, errs.Classify(err), errs.Integrity, "ErrThreshold must classify as Integrity")
		testkit.True(t, strings.Contains(err.Error(), "at most 1 of 2 required parties"),
			"the error must state the most parties that could count: "+err.Error())
	})

	t.Run("the zero Policy refuses every check", func(t *testing.T) {
		t.Parallel()
		var p sign.Policy
		err := p.Check(message, nil)
		testkit.ErrorIs(t, err, sign.ErrPolicy, "the zero Policy must not accept anything")
		testkit.Equal(t, errs.Classify(err), errs.Invalid, "ErrPolicy must classify as Invalid")
	})

	t.Run("checks a policy of more than 64 keys", func(t *testing.T) {
		t.Parallel()
		keys := make([]*key, 65)
		sigs := make([]sign.Signature, len(keys))
		for i := range keys {
			keys[i] = newKey(byte(i))
			sigs[i] = keys[i].signed()
		}

		p := mustPolicy(t, len(keys), solo(keys...)...)
		testkit.NoError(t, p.Check(message, sigs), "every key signing must satisfy a unanimous policy")
		testkit.ErrorIs(t, p.Check(message, sigs[1:]), sign.ErrThreshold,
			"one missing signature must fail a unanimous policy")
	})
}

// BenchmarkCheck measures Policy.Check for three policies: a threshold
// of three Ed25519 approvers of five, a hybrid party of an Ed25519 and
// an ML-DSA-65 key, and 64 keys of the test double, which isolates the
// bookkeeping from the cost of verification.
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
	signed := make([]sign.Signature, len(doubles))
	for i := range doubles {
		doubles[i] = newKey(byte(i))
		signed[i] = doubles[i].signed()
	}

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
		{"64 test-double keys", mustPolicy(b, len(doubles), solo(doubles...)...), signed},
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
	sigs := make([]sign.Signature, len(keys))
	for i := range keys {
		keys[i] = newKey(byte(i))
		sigs[i] = keys[i].signed()
	}

	unanimous := mustPolicy(t, len(keys), solo(keys...)...)
	allButOne := mustPolicy(t, len(keys)-1, solo(keys...)...)
	excluded := keys[0].id

	tests := []struct {
		fn   func()
		name string
	}{
		{func() { _ = unanimous.Check(message, sigs) }, "Check of 64 keys"},
		{func() { _ = allButOne.Check(message, sigs, excluded) }, "Check of 64 keys with an exclusion"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testkit.Equal(t, testing.AllocsPerRun(20, tt.fn), float64(0), tt.name+" must not allocate")
		})
	}
}
