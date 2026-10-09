---
rfc: 0055
title: Party Markers in Signature Policies
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-10-07
updated: 2026-10-07
discussion: none
supersedes: none
superseded-by: none
produces-adr: ADR-0046
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# RFC-0055: Party Markers in Signature Policies

## Summary

We propose a marker that makes one rule of a `sign.Policy` a party at
any depth of its tree. `Rule.AsParty` returns a copy of a rule with the
marker. A key that `Policy.Check` excludes then removes the marked rule
that contains it. Today it removes the child of the root that contains
it.

- A tree without a marker keeps the parties of ADR-0034: the children
  of its root, or the root itself when the root is an `AllOf` rule.
  `NewPolicy` builds such a tree.
- In a tree with a marker, the marked rules are the parties.
  `NewPolicyTree` refuses a marked rule inside another marked rule. It
  also refuses a key set outside every marked rule. Each key then has
  exactly one party.
- The code of `Policy.Check` does not change. The text of
  `ErrThreshold` counts "required rules" in place of "required
  parties". The children of the root of a marked tree are not its
  parties.

## Motivation

### An exclusion removes a whole group

`Policy.Check` takes the KeyIDs to exclude. The exclusion keeps a
requester from approving their own request. Under ADR-0034, the
children of the root are the parties. An excluded key removes the party
that contains it. In `crypto/sign/policy.go`, `builder.add` assigns each
key to the child of the root above it. `Check` then marks every key of
that child as excluded.

Two-person integrity across organisational units needs a threshold of
thresholds. One such policy requires approvals from 2 of 5
administrators and from 1 of 3 security officers, with the requester
excluded. In the following tree, `ada` to `hal` are `AllOf` rules of one
key each:

```go
approval, err := sign.NewPolicyTree(sign.AtLeast("approval", 2,
    sign.AtLeast("administrators", 2, ada, bob, carol, dan, eve),
    sign.AtLeast("security officers", 1, fay, gus, hal),
))
```

The children of the root are the two groups. The exclusion of an
administrator's key removes the whole group of administrators. The root
requires both groups. We ran this policy with eight Ed25519 keys
against `crypto/sign` on 2026-10-07, with the key of one administrator
excluded:

- With the signatures of two other administrators and one officer,
  `Check` returns `ErrThreshold` with the text "at most 1 of 2 required
  parties".
- With the signatures of all eight keys, `Check` returns the same
  error.

The policy refuses every request of an administrator. ADR-0034 lists
this outcome among its negative consequences.

### The structure of a tree does not identify a person

An exclusion must remove the requester and keep the requester's group.
A rule does not record whether it is a group or a person:

- `AtLeast("security officers", 1, fay, gus, hal)` is a group of three
  people.
- `AtLeast("bob", 1, bobLaptop, bobToken)` is one person with two key
  sets.

Both rules are a threshold of one over key sets, so the policy has to
state which rule is a person.

### Prior art

Review systems that require approvals from more than one group exclude
the requester as one person inside each group:

- In GitLab, each approval rule of a merge request requires a number of
  approvals from its eligible approvers. One approval reduces the count
  of every rule that the approver belongs to. By default, the author of
  a merge request is not an eligible approver of it. The other approvers
  of each rule remain eligible.
- In Gerrit, a submit requirement can count only the votes of users
  other than the uploader of the latest patch set, through the argument
  `user=non_uploader` of its `label` operator. The argument replaces the
  deprecated label setting `ignoreSelfApproval`.

Both systems remove the requester alone, and every other approver still
counts.

## Detailed design

### Components

| Component | Responsibility |
|---|---|
| `Rule.AsParty` | Returns a copy of a rule with the party marker. It does not validate the rule, as `AllOf` and `AtLeast` do not |
| `NewPolicyTree`, `Policy.Reset` | Refuse a marked rule inside a marked rule, and a key set outside every marked rule of a tree with a marker. Assign each key to its party |
| `Policy.Check` | No code change. An excluded key removes its party at any depth |
| `ErrThreshold` | Its text counts the rules that the root requires |
| `NewPolicy`, `AllOf`, `AtLeast`, `Rules` | No change |

### API

```go
// AsParty returns a copy of r that is one party of a policy: a key that
// Policy.Check excludes removes the whole rule, whatever signatures its
// other keys have. A tree that marks one rule must mark every party:
// NewPolicyTree refuses a marked rule inside another marked rule, and a
// key set outside every marked rule. AsParty leaves r unchanged, and it
// does not validate the copy.
//
// # Allocation contract
//
// Zero-alloc. The copy shares the keys and the children of r.
func (r Rule) AsParty() Rule
```

The errors of `NewPolicyTree` gain the last two conditions of this
list, each reported with the path of the offending rule:

```go
// Returns ErrPolicy, classified errs.Invalid, when a threshold is not
// between 1 and the number of its rule's children, when a rule has
// neither keys nor children, when a key is nil, when the tree is deeper
// than 64 rules, when one key appears twice anywhere in the tree, when
// a marked rule is inside another marked rule, and when a tree with a
// marked rule has a key set outside every marked rule.
```

The approval policy marks each person:

```go
approval, err := sign.NewPolicyTree(sign.AtLeast("approval", 2,
    sign.AtLeast("administrators", 2,
        sign.AllOf("ada", ada).AsParty(),
        sign.AtLeast("bob", 1,
            sign.AllOf("bob laptop", bobLaptop),
            sign.AllOf("bob token", bobToken),
        ).AsParty(),
        sign.AllOf("carol", carol).AsParty(),
        sign.AllOf("dan", dan).AsParty(),
        sign.AllOf("eve", eve).AsParty(),
    ),
    sign.AtLeast("security officers", 1,
        sign.AllOf("fay", fay).AsParty(),
        sign.AllOf("gus", gus).AsParty(),
        sign.AllOf("hal", hal).AsParty(),
    ),
))
```

`Check` then counts as follows:

- Ada requests, and the caller excludes her key. The exclusion removes
  `ada` alone. The administrators count when 2 of Bob, Carol, Dan and
  Eve sign.
- Bob requests with his laptop key. The exclusion removes `bob` with
  both of his key sets, so the signature of his token does not approve
  his own request.
- Without an exclusion, `Check` returns the result and makes the
  verifications of the same tree without markers.

### Parties

The parties of a policy are:

- In a tree without a marker, the children of the root, or the root
  itself when the root is an `AllOf` rule, as ADR-0034 decides.
- In a tree with a marker, the marked rules.

`Check` removes the party of an excluded key as it does today:

- The party does not count, whatever signatures its keys have.
- Every rule outside the party counts as it would if the party had no
  signatures.
- More than one excluded key of a party removes the party once.
- An excluded key outside the policy does not change the result.

A marker on the root makes the whole policy one party, as it is for an
`AllOf` root today. An exclusion of any of its keys then fails the
check.

### A marked rule inside a marked rule

`NewPolicyTree` refuses a marked rule inside another marked rule, with
`ErrPolicy` and the path of the inner rule. A tree with such a rule has
two readings:

- If the outer rule were the party, the inner markers would have no
  effect.
- If the inner rule were the party, a key inside an inner marker would
  remove its inner party alone. A key outside every inner marker would
  remove the outer rule, the parties inside it included.

The second reading expresses a policy that one level of markers cannot.
GitLab and Gerrit exclude the requester alone, and we did not find an
approval scheme that needs the second reading. A tree with two readings
is a hazard in a security policy. `NewPolicyTree` refuses it, and the
author marks one level instead. A policy that must remove the outer rule
for some of its keys and an inner party for others would reverse this
verdict.

### A key set outside every marked rule

In a tree with a marker, `NewPolicyTree` refuses a key set outside every
marked rule. The error reports the path of the first such key set in
depth-first order, also when the key set precedes the first marker.

The tree states no party for such a key set, and each implicit choice
fails in one direction:

- The child of the root that contains the key set, as in a tree without
  a marker. An administrator whose marker the author forgot would remove
  every administrator, which is the failure that this RFC removes.
  `NewPolicyTree` would not report an error.
- The key set alone. A person with alternative key sets whose marker the
  author forgot could approve their own request with the other key set.
  ADR-0034 rejected this rule.

So a tree that marks one party must mark every party. A policy that
needs unmarked key sets beside marked parties would reverse this
verdict. A witness quorum is checked without exclusions, so a marker
would not change it. We do not know of another such policy.

### The text of ErrThreshold

`Check` returns `ErrThreshold` with the most children of the root that
could count and the number of children that the root requires. For an
`AllOf` root, it counts the root itself. In a tree without a marker,
those children are the parties. In the approval policy, they are the two
groups, and the parties are the eight people.

The text becomes "at most N of K required rules" for every tree. The
word "rules" is true of the children of every root, so every tree gets
one text. A text chosen by the kind of tree would need a field of
`Policy` for the message alone. Inside core, only the tests of
`crypto/sign` match the text, and `note` and `tlog/checkpoint` check the
error with `errors.Is`. A caller that parses the text would reverse this
verdict.

### Invariants

1. A tree without a marker has the parties of ADR-0034, and `Check`
   returns what it returns today, apart from the text of `ErrThreshold`.
2. In a tree with a marker, each key belongs to exactly one party: the
   marked rule that contains its key set.
3. Without an exclusion, a marker changes neither the result of `Check`
   nor its verifications.
4. An excluded key removes exactly its party.
5. `Check` verifies at most one signature per key, and an exclusion only
   removes verifications.

### Evaluation

`NewPolicyTree` stores the position of each key's party in
`Policy.rules`. `Check` marks the keys of an excluded party before its
passes 2 and 3, which RFC-0044 describes. The marker changes only the
position that `NewPolicyTree` stores: the marked rule above the key set,
in place of the child of the root. The code of `Check`, its bookkeeping
and its bounds do not change.

`NewPolicyTree` finds both new faults before it sizes the memory of the
`Policy`, so a refused tree allocates only its error, as today.

### Bounds

| Quantity | Bound |
|---|---|
| Verifications per `Check` | At most one per key of the policy, unchanged |
| Writes of an exclusion | One per key of the party of each excluded key, unchanged |
| Depth of a tree | 64 rules, unchanged. A marker adds no rule |
| Size of a `Rule` | 80 bytes on amd64, from 72 |

We measured the size of `Rule` with `unsafe.Sizeof`, and of a struct of
its fields with the marker added.

### Failure handling

| Condition | Function | Error | Class |
|---|---|---|---|
| A marked rule inside another marked rule | `NewPolicyTree`, `Policy.Reset` | `ErrPolicy`, "a party inside another party", with the path of the inner rule | `Invalid` |
| A key set outside every marked rule of a tree with a marker | `NewPolicyTree`, `Policy.Reset` | `ErrPolicy`, "a key set outside every party", with the path of the key set | `Invalid` |
| The root does not count | `Check` | `ErrThreshold`, "at most N of K required rules" | `Integrity` |

The other rows of the failure table of RFC-0044 do not change.

### Allocation contract

| Operation | Allocations |
|---|---|
| `Rule.AsParty` | 0 |
| `NewPolicyTree` of a tree with markers | The allocations of the same tree without markers |
| `Policy.Reset` and the methods of `Rules`, in the memory of a build of the same size | 0 |
| `Check` that returns nil, for up to 64 keys and 128 rules, with or without an exclusion | 0, apart from what each `Verifier` allocates |

### Compatibility

- `NewPolicy` and every tree without a marker keep their parties and
  their results.
- The text of `ErrThreshold` changes for every policy. `errors.Is` and
  `errs.Classify` return what they return today.
- `Rule` has no exported fields, so its new field does not change any
  caller.

### Tests

| # | Guarantee |
|---|---|
| 1 | Every case of `TestPolicy` passes, with "required rules" in place of "required parties" |
| 2 | In the approval policy, with Ada's key excluded, the signatures of Bob, Carol and Fay satisfy the policy, and the signatures of Ada, Bob and Fay do not |
| 3 | In the approval policy, with Bob's laptop key excluded, the signatures of Bob's token, Carol and Fay do not satisfy the policy |
| 4 | `NewPolicyTree` refuses a marked rule inside a marked rule, at the root and three levels below it, and its error reports the path of the inner rule |
| 5 | `NewPolicyTree` refuses a key set outside every marked rule, before and after the first marker in depth-first order, and its error reports the path of the key set |
| 6 | With a marker on the root, an exclusion of one of its keys fails the check |
| 7 | `AsParty` leaves the rule that it copies unmarked, so a tree of that rule has the parties of ADR-0034 |
| 8 | The property test draws trees with and without markers, signatures and exclusions. `Check` returns what a reference evaluator returns, which removes the marked rule of an excluded key, and it verifies at most one signature per key |
| 9 | The property test checks a marked tree without an exclusion against the same tree without markers, for the same result and the same verifications |
| 10 | `TestPolicyAllocs` and `BenchmarkPolicy` measure 0 allocations for a `Check` of the approval policy with an exclusion, and for a rebuild of the policy in the memory of `Rules` |
| 11 | The suite covers every statement of `crypto/sign`, and mutation testing detects every mutant |

## Alternatives considered

### A. A policy per group in the caller

The caller checks the administrators and the officers with one policy
each, with the key of the requester excluded, and requires both.

**Why not:** the caller must then refuse a key that both policies list,
and bound the verifications across them, as alternative A of RFC-0044
explains. `Policy` already does both within one tree.

### B. Removal of the key set alone

An excluded key would remove only the `AllOf` rule that contains it.

**Why not:** a person with alternative key sets could approve their own
request with the other key set. ADR-0034 rejected this rule.

### C. Parties read from the structure of the tree

An `AtLeast` rule with a threshold of 1 over key sets would be the
alternatives of one person.

**Why not:** a group with a threshold of 1 has the same structure. The
rule would remove all three security officers for the request of one of
them.

### D. Exclusion of rules by name

`Check` would take the names of the rules to exclude.

**Why not:** the caller knows the key that signed the request, not the
rule of the requester. The change would break the signature of `Check`,
and a `Policy` does not store the names of its rules.

### E. A constructor `Party(name, rule)`

ADR-0034 and RFC-0044 name this constructor.

**Why not:** `Party` is the struct type that `NewPolicy` takes. A Go
package declares its types and its functions in one namespace, so the
constructor needs another name. The rule also has a name of its own for
the paths of errors, and a second name adds nothing to it.

### F. The inner rule of nested markers as the party

**Why not:** the outer marker would then apply only to the keys outside
every inner marker. A reader of the tree would have to work out which
marker applies to each key, and we did not find an approval scheme that
needs the policy that this reading adds.

### G. An unmarked key set of a marked tree as its own party

**Why not:** a person with alternative key sets whose marker the author
forgot could approve their own request with the other key set.

### H. An unmarked key set of a marked tree in the party of its root child

**Why not:** the exclusion of an administrator whose marker the author
forgot would remove every administrator. `NewPolicyTree` would not
report an error.

### I. A text of ErrThreshold per kind of tree

The text would count "parties" in a tree without a marker and "rules"
in a tree with one.

**Why not:** `Policy` would need a field for the message alone, and one
count would have two texts.

## Drawbacks

- A tree with a marker needs a marker on every party. The approval
  policy has eight.
- The marker states the party, and `NewPolicyTree` cannot check that the
  author marked the right rule. Markers on Bob's two key sets, in place
  of one on the rule over both, let Bob approve his own request with his
  other key set.
- The text of `ErrThreshold` changes for every policy, the policies of
  `NewPolicy` included. A caller that matched the old text no longer
  matches.
- `Rule` grows from 72 to 80 bytes. The memory of `Rules` and the stack
  array of 16 rules in `NewPolicy` grow with it.
- ADR-0034 defines the parties of a tree without a marker, and the
  markers define the parties of a tree with one.

## Open questions

None.

## Unresolved / future work

None.

## References

- ADR-0034, the root's children are the parties of a policy.
- RFC-0044, nested signature policies.
- RFC-0039, signature policies and verifier resolution.
- `crypto/sign/policy.go`: `Rule`, `Policy`, `NewPolicyTree`,
  `builder.add` and `Policy.Check`.
- GitLab, "Merge request approval rules",
  <https://docs.gitlab.com/user/project/merge_requests/approvals/rules/>,
  and "Merge request approval settings",
  <https://docs.gitlab.com/user/project/merge_requests/approvals/settings/>.
- Gerrit Code Review, "Review Labels", `ignoreSelfApproval`,
  <https://gerrit-review.googlesource.com/Documentation/config-labels.html>.
