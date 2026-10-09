---
rfc: 0044
title: Nested Signature Policies
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-09-28
updated: 2026-09-28
discussion: none
supersedes: none
superseded-by: none
produces-adr: ADR-0034
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# RFC-0044: Nested Signature Policies

## Summary

We propose to let a `sign.Policy` nest thresholds, so that a policy is a
tree of rules:

- `AllOf(name, keys...)` is a key set that counts when every one of its
  keys has a valid signature. It is one signer, classical or hybrid.
- `AtLeast(name, k, children...)` counts when at least k of its children
  count. It is a quorum of parties, or the alternatives of one party
  with k = 1.

`NewPolicyTree(root)` validates a tree and returns a `Policy`.
`Policy.Check` keeps the contract of RFC-0039 across the whole tree:

- It verifies at most one signature per key, whatever the number of
  signatures in its input.
- It stops as soon as the result is decided.
- It does not allocate on success for a policy of up to 64 keys and 128
  rules.

Check also skips verification when the signatures it received cannot
satisfy the root, even if every one of them is valid. The children of
the root are the parties, and an excluded key removes the party that
contains it. `NewPolicy` keeps its API and its behaviour, and builds the
one-level tree.

The tree is the structure that C2SP tlog-policy defines for witness
quorums. RFC-0039 recorded nested policies as future work.

## Motivation

### A threshold of parties cannot express alternatives

`Policy` counts parties against one threshold, and a `Party` counts only
when every one of its keys signs (`crypto/sign/policy.go:28-39`). A
witness quorum needs three rules at once:

- A quorum across independent parties, such as 3 of 4.
- Alternatives within a party. An organisation that runs two witnesses
  counts once when either of them cosigns.
- Every key of a hybrid witness. A witness with an ML-DSA-87 key and an
  ECDSA P-384 key counts only when both keys sign.

`Policy` expresses the first and the third, but not the second
together with them.

A caller can list each witness as a party of its own, but the policy
then loses its fault bound. A split view needs at least 2q − n
compromised parties, because any two sets of q parties out of n have at
least 2q − n parties in common and an honest party does not cosign two
conflicting checkpoints. This bound assumes that parties fail
independently. The witnesses of one organisation fail together. A
policy that counts them as separate parties tolerates fewer compromised
organisations than its numbers state.

### C2SP tlog-policy nests groups

In C2SP tlog-policy, a group counts when k of its n members count, and a
member is a witness or another group, so a witness quorum is a tree. The
specification's example requires two of three witnesses of one
organisation and one of three witnesses of another:

```text
group X-witnesses 2 X1 X2 X3
group Y-witnesses any Y1 Y2 Y3
group X-and-Y all X-witnesses Y-witnesses
quorum X-and-Y
```

A parser that turns such a policy into a `sign.Policy` must refuse every
group that is a member of another group.

### Why core

A caller that composes policies itself must repeat the two rules that
make a threshold policy safe:

- A key counts once, however often it appears. RFC-0039 cites three TUF
  implementations that counted a key twice.
- The verification work is bounded by the keys of the policy, whatever
  the number of signatures an attacker attaches.

Both rules are properties of the whole tree, so they belong where the
tree is evaluated.

## Detailed design

### Components

| Component | Responsibility |
|---|---|
| `Rule` | One node of a policy tree: an `AllOf` key set or an `AtLeast` threshold. Opaque and immutable |
| `AllOf` | Builds a key set that counts when every one of its keys has a valid signature |
| `AtLeast` | Builds a threshold that counts when at least k of its children count |
| `NewPolicyTree` | Validates a tree and flattens it into a `Policy` |
| `Policy.Check` | Evaluates the tree for a message and its signatures. Its signature does not change |
| `NewPolicy` | Builds `AtLeast` over one `AllOf` per party. Its API does not change |

### API

```go
// Rule is one node of a signature policy: a set of keys that must all
// sign, or a threshold over child rules. AllOf and AtLeast build a
// Rule without validating it, and NewPolicyTree validates the whole
// tree. The zero Rule is invalid in a policy.
type Rule struct{ /* unexported fields */ }

// AllOf returns a rule that counts when every one of keys has a valid
// signature: one signer, or one hybrid signer with a classical and a
// post-quantum key. name identifies the rule in errors. AllOf copies
// keys.
func AllOf(name string, keys ...Verifier) Rule

// AtLeast returns a rule that counts when at least threshold of
// children count: a quorum of parties, or the alternatives of one
// party with threshold 1. name identifies the rule in errors. AtLeast
// copies children.
func AtLeast(name string, threshold int, children ...Rule) Rule

// NewPolicyTree returns a Policy that requires root to count.
//
// Returns ErrPolicy, classified errs.Invalid, when a threshold is not
// between 1 and the number of its rule's children, when a rule has
// neither keys nor children, when a key is nil, when the tree is deeper
// than 64 rules, and when one key appears twice anywhere in the tree.
// The key check compares KeyIDs and encoded public keys, as NewPolicy
// does. The error reports the path of names from the root to the
// offending rule, such as "X-and-Y/X-witnesses".
func NewPolicyTree(root Rule) (Policy, error)
```

C2SP tlog-policy's example is one expression:

```go
policy, err := sign.NewPolicyTree(sign.AtLeast("X-and-Y", 2,
    sign.AtLeast("X-witnesses", 2,
        sign.AllOf("X1", x1), sign.AllOf("X2", x2), sign.AllOf("X3", x3)),
    sign.AtLeast("Y-witnesses", 1,
        sign.AllOf("Y1", y1), sign.AllOf("Y2", y2), sign.AllOf("Y3", y3)),
))
```

A quorum of three of four organisations, each with two hybrid
witnesses, has 16 keys and 13 rules:

```go
// org returns an organisation that counts when either of its witnesses
// cosigns. A witness is an ML-DSA-87 key and an ECDSA P-384 key.
org := func(name string, w1, w2 [2]sign.Verifier) sign.Rule {
    return sign.AtLeast(name, 1,
        sign.AllOf(name+"/1", w1[:]...),
        sign.AllOf(name+"/2", w2[:]...),
    )
}

policy, err := sign.NewPolicyTree(sign.AtLeast("witnesses", 3,
    org("a", a1, a2), org("b", b1, b2), org("c", c1, c2), org("d", d1, d2),
))
```

### Counting

- Under the rules of RFC-0039, Check considers the first signature in
  its input for each key of the policy. That signature counts when its
  algorithm equals its key's algorithm and it verifies over the message.
- An `AllOf` rule counts when every one of its keys has a counting
  signature.
- An `AtLeast` rule counts when at least its threshold of children
  count.
- The policy is satisfied when its root counts.

The result depends only on which keys have a counting signature. The
order of the children changes the number of verifications, and never
the result.

### Parties and exclusion

The parties of a policy are the children of its root. When the root is
an `AllOf` rule, the root is the only party. `Check`'s `exclude`
argument removes parties:

- An excluded key removes the party that contains it. The party does
  not count, whatever signatures it has.
- A key in `exclude` that is not in the policy does not change the
  result.
- Two or more excluded keys of one party remove the party once.
- `ErrThreshold` reports parties: "at most N of K required parties".
  K is the root's threshold, or 1 for an `AllOf` root. N is the number
  of parties that counted or that Check did not rule out.

For `NewPolicy`, the root's children are its parties, so the rule is
the behaviour of RFC-0039. In the tlog-policy example, the parties are
`X-witnesses` and `Y-witnesses`, the two organisations.

The rule removes a party with alternative key sets as a whole. Alice may
approve with a laptop key or with a hardware token:

```go
alice := sign.AtLeast("alice", 1,
    sign.AllOf("alice laptop", aliceLaptop),
    sign.AllOf("alice token", aliceToken),
)
approvers, err := sign.NewPolicyTree(sign.AtLeast("approvers", 2, alice, bob, carol))
```

When Alice signs a request with her laptop key, the caller checks the
request's approvals with that key in `exclude`. The rule removes `alice`
as a whole, so her token's signature and Bob's signature do not approve
her own request.

### Evaluation

`NewPolicyTree` flattens the tree into arrays. As in today's `Policy`,
the keys of each party are contiguous. `Check` makes three passes:

1. It records the position of the first signature for each key, and it
   marks the keys of every excluded party.
2. From the key sets up, it counts for each rule the children, or the
   keys, that could count if every recorded signature were valid. This
   pass does not verify signatures. When the root could not count,
   Check returns `ErrThreshold` without verifying a signature.
3. It evaluates the root depth first. An `AtLeast` rule skips the
   children that pass 2 ruled out. It stops as soon as its count meets
   its threshold, and as soon as its remaining children can no longer
   meet it. An `AllOf` rule stops at the first key whose signature does
   not count.

Every key is in one key set, and Check evaluates each key set at most
once, so it verifies at most one signature per key.

Today's `Check` rules out one party at a time, in the order of the
parties, so pass 2 also removes verifications from flat policies.
Measured against `crypto/sign` at 80b33ed:

- A 3-of-3 policy whose third party did not sign runs 2 verifications
  before it returns `ErrThreshold`.
- A 2-of-2 policy whose first party did not sign runs none.

With pass 2, both return `ErrThreshold` without a verification.

### Bounds

| Quantity | Bound |
|---|---|
| Verifications per `Check` | At most one per key of the policy, whatever the number of signatures |
| Verifications when the recorded signatures cannot satisfy the root | 0 |
| Depth of a tree | 64 rules |
| Bookkeeping of `Check` | One position per key and one count per rule |

We measured the verification of one valid signature over a 64-byte
message, with Go 1.27.1 on an AMD Ryzen 9 9950X3D and four Ps. Each
range covers three runs.

| Algorithm | Verification | Allocations |
|---|---|---|
| Ed25519 | 27.3-28.4 µs | 0 |
| ML-DSA-44 | 68.0-69.6 µs | 0 |
| ML-DSA-65 | 109.0-110.2 µs | 0 |
| ML-DSA-87 | 177.9-180.3 µs | 0 |
| ECDSA P-384 | 322.4-328.2 µs | 17, 808 bytes |

The bookkeeping is small next to these figures. `BenchmarkCheck` runs a
policy of 64 test-double keys, whose verification is a byte comparison,
in 1.24-1.26 µs. For the quorum of four organisations:

- A check that verifies all 16 keys takes 4.0 to 4.1 ms.
- A check in which the first witness of three organisations cosigns
  stops after 6 verifications, in 1.50 to 1.53 ms.
- Without the bound of one verification per key, an attacker who
  attaches 1,000 signatures for one ECDSA P-384 key would add 0.32 to
  0.33 s to every check.

### Copies, cycles and depth

No caller can make a rule its own descendant, because `AllOf` and
`AtLeast` copy their arguments and `Rule` has no exported fields. A rule
can contain only rules that were built before it. A constructor that
kept its variadic slice would share the caller's backing array. After
`r := sign.AtLeast("r", 1, rules...)`, the caller could store r in
`rules[0]`, and r would become its own child.

The depth bound limits the recursion of `NewPolicyTree` and of `Check`.
C2SP tlog-policy recommends that an implementation support at least 32
groups. A chain of 32 groups over one witness is 33 rules deep, so a
bound below 33 would refuse a policy that the specification asks
implementations to accept.

`NewPolicyTree` copies the tree into arrays of the `Policy`. A `Policy`
is immutable after construction and safe for concurrent use, as today.

### NewPolicy

`NewPolicy(threshold, parties...)` keeps its API, its documented errors
and its results. It returns `NewPolicyTree` of an `AtLeast` rule with
the threshold and one `AllOf(p.Name, p.Keys...)` rule per party. Its
documented errors are a threshold out of range, a party without keys, a
nil key and a key listed twice. Every check returns nil exactly when it
returns nil today, and pass 2 can only remove verifications.

### Failure handling

| Condition | Function | Error | Class |
|---|---|---|---|
| A threshold outside 1 to the number of its rule's children | `NewPolicyTree` | `ErrPolicy` | `Invalid` |
| A rule with neither keys nor children, including the zero `Rule` | `NewPolicyTree` | `ErrPolicy` | `Invalid` |
| A nil key | `NewPolicyTree` | `ErrPolicy` | `Invalid` |
| One KeyID twice anywhere in the tree | `NewPolicyTree` | `ErrPolicy` | `Invalid` |
| One public key under two KeyIDs | `NewPolicyTree` | `ErrPolicy` | `Invalid` |
| A tree deeper than 64 rules | `NewPolicyTree` | `ErrPolicy` | `Invalid` |
| The root does not count | `Check` | `ErrThreshold` | `Integrity` |
| The zero `Policy` | `Check` | `ErrPolicy` | `Invalid` |

### Allocation contract

| Operation | Allocations |
|---|---|
| `Check` that returns nil, for up to 64 keys and 128 rules | 0, apart from what each `Verifier` allocates |
| `Check` that returns nil, for a larger policy | Its bookkeeping |
| `Check` that returns an error | The returned error |
| `AllOf`, `AtLeast` | The copy of their arguments |
| `NewPolicyTree` | The arrays and the two key maps of the `Policy` |

Both example policies fit the stack bound: the quorum of four
organisations has 13 rules, and a tlog-policy within the specification's
recommended limits of 32 witnesses and 32 groups has at most 64.

### Tests

| # | Guarantee |
|---|---|
| 1 | Every case of the flat `TestNewPolicy` and `TestCheck` passes unchanged, with `NewPolicy` built on the tree |
| 2 | A property test builds random trees of up to 64 keys, random signatures that are valid, forged or missing, and random exclusions. `Check` returns what a reference evaluator returns, which evaluates every rule without stopping early, and it verifies at most one signature per key |
| 3 | `NewPolicyTree` refuses each condition of the failure table at the root and three levels below it, and its error reports the rule's path |
| 4 | `NewPolicyTree` accepts a chain of 64 rules and refuses a chain of 65 |
| 5 | A change to the caller's slices after `AllOf` or `AtLeast` changes no rule |
| 6 | The tlog-policy example counts two X witnesses and one Y witness, and does not count three Y witnesses and one X witness |
| 7 | With Alice's laptop key excluded, her token's signature and Bob's do not satisfy the approvers |
| 8 | `Check` does not verify a signature when the recorded signatures cannot satisfy the root, counted through a `Verifier` that records its calls |
| 9 | `Check` stops once the root counts and once it can no longer count, counted the same way |
| 10 | `BenchmarkCheck` reports 0 allocations for its three flat cases, for the quorum of four organisations over test-double keys, and for 64 test-double keys in 128 rules |
| 11 | The suite covers every statement of `crypto/sign`, and gremlins kills every mutant |

We measured `BenchmarkCheck` at 80b33ed and with the tree, in three
alternating rounds:

| Case | 80b33ed | Tree |
|---|---|---|
| 3 of 5 Ed25519 approvers | 78.1-78.8 µs | 78.0-78.4 µs |
| An Ed25519 and ML-DSA-65 hybrid | 130.9-131.3 µs | 131.8-132.4 µs |
| 64 test-double keys | 1.06-1.10 µs | 1.24-1.26 µs |
| 3 of 4 organisations of test-double witnesses | none | 0.20-0.22 µs |
| 64 test-double keys in 128 rules | none | 1.64-1.77 µs |

The case of 64 test-double keys measures the bookkeeping alone, and
pass 2 adds about 0.17 µs to it. No case allocates.

## Alternatives considered

### A. Compose policies in the caller

The caller evaluates each party with a `Policy` of its own and counts
the parties.

**Why not:** the caller must then refuse a key that two parties share.
It must also bound the verifications across the parties. `Policy`
already does both within one policy. The rules that make a threshold
policy safe would then exist in two places.

### B. Alternatives inside `Party`

A `Party` would have two or more key sets. It would count when one of
them signs in full.

**Why not:** it fixes the tree at three levels: a threshold of parties,
the alternatives of a party, and a key set. It cannot express a group of
groups, such as the tlog-policy example. It also changes the meaning of
an existing type.

### C. A party that contains a `Policy`

RFC-0039 describes this future work as a party that is itself a policy.

**Why not:** each `Policy` validates only its own keys, so a key in two
nested policies passes both checks. Each nested `Check` also has a bound
of its own. A check for shared keys and one bound across the levels
need one index over every key, which is the flattened tree of this RFC.

### D. Policies as interface values, as in torchwood

torchwood v0.9.0 composes values of a `Policy` interface with
`ThresholdPolicy(n, policies...)`, and parses tlog-policy groups into
them. `note.Open` first verifies the signature of every key that the
policy knows, once per key. Each child's `Check` then looks for the
verified signatures it needs.

**Why not:**

- `note.Open` verifies the signature of each known key whether the
  result needs it or not, as in alternative H.
- torchwood refuses a verifier found in two groups only when `note.Open`
  looks the verifier up.
- `ParsePolicy` refuses a duplicate witness name, and does not compare
  public keys.

A closed tree lets `NewPolicyTree` refuse a duplicate key once, by KeyID
and by public key, when it builds the policy.

### E. Exclusion of the key set alone

An excluded key would remove only the `AllOf` rule that contains it.

**Why not:** it lets a party with alternative key sets approve its own
request with another key set, as in Alice's example. The flat `Policy`
of RFC-0039 cannot express alternatives, so it does not have this gap.

### F. An explicit party marker

A constructor such as `Party(name, rule)` would mark the rule that an
excluded key removes, at any depth.

**Why not now:** the root's children cover the parties of `NewPolicy`
and of trees with two levels. A marker adds a constructor, and a rule
for markers nested in markers, before any policy needs one. A later RFC
can add it. The root's children are then still the parties of a tree
without a marker.

### G. A policy that does not require a signature

sigsum-go represents tlog-policy's `quorum none` as a group with a
threshold of 0, which always counts.

**Why not:** a policy that accepts an empty set of signatures is a
decision that the caller states in its own code. `NewPolicy` refuses a
threshold below 1, and `AtLeast` keeps that rule. A caller that parses
`quorum none` skips the check.

### H. Verify every signature, then evaluate the tree

sigsum-go verifies the cosignature of every listed witness, and then
evaluates the tree without stopping early.

**Why not:** Check would then run one verification per key with a
signature, whether the result needs it or not. For the quorum of four
organisations, that takes 4.0 to 4.1 ms, against 1.50 to 1.53 ms when
the first witness of three organisations cosigns.

## Drawbacks

- Exclusion fails closed in trees with more than two levels. When the
  root requires two groups of parties, an excluded key removes its whole
  group, and the root cannot count for that requester.
- `AllOf(name, keys...)` and `Party{Name, Keys}` describe the same key
  set. `NewPolicy` keeps `Party` for compatibility.
- Pass 2 adds integer work to every `Check`, including a check that
  would stop after its first verification.
- The recursion of `Check` adds up to 64 frames to the goroutine's
  stack.
- `ErrThreshold` reports the count of parties, not the rule that failed
  below them. Only the errors of `NewPolicyTree` report the path of a
  rule.

## Open questions

None.

## Unresolved / future work

- A marker for the rule that an excluded key removes, below the root's
  children.
- A parser of C2SP tlog-policy text into a `Policy`. It needs the
  signed-note verifier keys of the policy's witness lines, and core does
  not implement signed notes.

## References

- C2SP tlog-policy, <https://c2sp.org/tlog-policy>: groups of groups,
  the rule that a name is a member of at most one group, duplicate keys,
  and the recommended limits of 32 witnesses and 32 groups.
- sigsum-go, `pkg/policy/policy.go` and `pkg/policy/builder.go` at
  c924c03, <https://git.glasklar.is/sigsum/core/sigsum-go>: the reference
  implementation of the policy format.
- torchwood v0.9.0, `policy.go`, <https://github.com/FiloSottile/torchwood>.
- golang.org/x/mod v0.41.0, `sumdb/note/note.go`, `Open`.
- RFC-0039, signature policies and verifier resolution, and the TUF
  vulnerabilities it cites.
- ADR-0034, the root's children are the parties of a policy.
- `crypto/sign/policy.go`, `Party`, `Policy`, `NewPolicy` and `Check`.
