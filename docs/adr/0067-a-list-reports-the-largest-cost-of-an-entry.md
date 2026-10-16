---
adr: 0067
title: A List Reports the Largest Cost of an Entry
status: Accepted
date: 2026-10-16
supersedes: none
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0067: A List Reports the Largest Cost of an Entry

## Status

Accepted

## Context

`List.Cost` returned the exact bytes of an entry in one `List`. The height of
a node derives from the seed of the List, so lists of other seeds reported
other costs for one entry, and an engine whose replicas bound a batch with
`Cost` admitted the batch on some replicas and refused it on others. The
bound over every seed was 108 node bytes, which only the documentation of
`Config` stated.

## Decision

We will add `skiplist.MaxCost(keyLen, valueLen int) (dataBytes, nodeBytes int)`,
the cost of an entry in a node of the largest height, because every replica
then computes the same bound for a batch.

- `dataBytes` is keyLen plus valueLen, as `Cost` computes it, and
  `nodeBytes` is 108.
- `Cost` of an entry returns the same `dataBytes` and at most the
  `nodeBytes` of `MaxCost`.
- A length below 0 counts as 0, and one above 2^32 - 1 counts as 2^32 - 1.

## Alternatives Considered

### A constant of 108 bytes

Rejected. A caller would add the bytes of the data arena itself.

### The bound in the engine

Rejected. A change of the node layout would make the copy wrong without a
change of an API.

## Consequences

**Positive:**

- Every replica computes one bound for a batch, whatever the seed of its
  List.
- The layout of a node is private to `skiplist`.

**Negative:**

- A reservation of `MaxCost` takes 108 node bytes for each entry, where a
  node takes 34.3 bytes on average.

**Neutral:**

- `MaxCost` computes its result from its arguments alone, without an
  allocation.

## References

- RFC-0069, the largest cost of a skip list entry.
- Issue 63, <https://github.com/thesm-os/core/issues/63>.
