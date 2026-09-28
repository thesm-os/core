---
adr: 0034
title: The Root's Children Are the Parties of a Policy
status: Accepted
date: 2026-09-28
supersedes: none
superseded-by: none
---

# ADR-0034: The Root's Children Are the Parties of a Policy

## Status

Accepted

## Context

`sign.Policy.Check` takes the KeyIDs to exclude, which keeps a requester
from approving their own request. In the flat policy of RFC-0039, a
party is one key set, and an excluded key removes its party.

RFC-0044 lets a policy nest thresholds. A party can then have
alternative key sets, such as a laptop key and a hardware token. A tree
also has no fixed level of parties. If an excluded key removed only its
own key set, a requester who signed with one key set could approve
their own request with another.

## Decision

We will treat the children of a policy's root as its parties, or the
root itself when it is an `AllOf` rule. An excluded key will remove the
party that contains it, because a party must not count toward its own
request through any of its key sets.

## Alternatives Considered

### Remove the key set alone

An excluded key would remove only the `AllOf` rule that contains it.

Rejected. A party with alternative key sets could then approve its own
request with another key set.

### Mark parties explicitly

A constructor such as `Party(name, rule)` would mark the rule that an
excluded key removes, at any depth.

Rejected for now. The root's children cover the parties of `NewPolicy`
and of trees with two levels, and a marker adds a constructor and a rule
for markers nested in markers. A later decision can add it without
changing the parties of a tree that has no marker.

### Remove every rule on the key's path

An excluded key would remove every rule from its key set up to the
root.

Rejected. The path ends at the root, so any exclusion would fail every
check.

## Consequences

**Positive:**

- `NewPolicy` keeps the behaviour of RFC-0039, because the children of
  its root are its parties.
- A party with alternative key sets cannot approve its own request.
- `ErrThreshold` counts the same parties that an exclusion removes.

**Negative:**

- Exclusion fails closed in trees with more than two levels. When the
  root requires two groups of parties, an excluded key removes its whole
  group, and the root cannot count for that requester.

**Neutral:**

- A witness quorum does not exclude keys, so the decision does not
  change how a tlog-policy tree counts.

## References

- RFC-0044, nested signature policies.
- RFC-0039, signature policies and verifier resolution.
- `crypto/sign/policy.go`: `Policy.Check`, `NewPolicyTree` and
  `builder.add`.
