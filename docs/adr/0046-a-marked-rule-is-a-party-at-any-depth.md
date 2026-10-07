---
adr: 0046
title: A Marked Rule Is a Party at Any Depth
status: Accepted
date: 2026-10-07
supersedes: none
superseded-by: none
---

# ADR-0046: A Marked Rule Is a Party at Any Depth

## Status

Accepted

## Context

ADR-0034 makes the children of a policy's root its parties, and an
excluded key removes the party that contains it. In a tree whose root
requires groups of people, the parties are the groups. A policy of 2 of
5 administrators and 1 of 3 security officers then refuses every
request of an administrator whose key the check excludes, because the
exclusion removes the whole group of administrators. The structure of a
tree does not tell a group from a person, so the policy has to state
which rules are its parties. RFC-0055 gives the design.

## Decision

We will let `Rule.AsParty` mark a rule as one party at any depth. In a
tree with a marked rule, the marked rules are the parties, and an
excluded key removes the marked rule that contains it. `NewPolicyTree`
refuses a marked rule inside another marked rule, and a key set outside
every marked rule, so each key of such a tree has exactly one party. A
tree without a marked rule keeps the parties of ADR-0034.

## Alternatives Considered

### The inner rule of nested markers as the party

A key inside an inner marker would remove its inner party. A key outside
every inner marker would remove the outer rule.

Rejected. A reader of the tree would have to work out which marker
applies to each key. No approval scheme that RFC-0055 examined needs the
policy that this reading adds.

### An unmarked key set of a marked tree as its own party

Rejected. A person with alternative key sets whose marker the author
forgot could approve their own request with the other key set. ADR-0034
rules this out.

### An unmarked key set of a marked tree in the party of its root child

Rejected. A forgotten marker on one administrator would remove every
administrator. `NewPolicyTree` would not report an error.

### Parties read from the structure of the tree

An `AtLeast` rule with a threshold of 1 over key sets would be the
alternatives of one person.

Rejected. A group with a threshold of 1, such as 1 of 3 security
officers, has the same structure.

### Exclusion of rules by name

Rejected. The caller knows the key that signed the request, not the
rule of the requester, and a `Policy` does not store the names of its
rules.

## Consequences

**Positive:**

- A policy of groups of people excludes the requester alone, and the
  other members of the requester's group still count.
- `NewPolicy` and every tree without a marker keep their parties.
- `NewPolicyTree` checks that each key of a marked tree has one party
  when it builds the policy.

**Negative:**

- A tree with a marker needs a marker on every party.
- `NewPolicyTree` cannot check that the author marked the right rule.
- `Rule` grows from 72 to 80 bytes.

**Neutral:**

- `ErrThreshold` counts the rules that the root requires, and its text
  says "required rules" in every tree.
- A witness quorum is checked without exclusions, so its trees need no
  marker.

## References

- RFC-0055, party markers in signature policies.
- ADR-0034, the root's children are the parties of a policy.
- `crypto/sign/policy.go`: `Rule.AsParty`, `NewPolicyTree`,
  `checkParties` and `builder.add`.
