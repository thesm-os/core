---
adr: 0081
title: A Fenced Writer May Refuse Epoch Zero
status: Accepted
date: 2026-11-07
supersedes: RFC-0026
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0081: A Fenced Writer May Refuse Epoch Zero

## Status

Accepted

Supersedes the law of `epoch.Zero` that `coretest/epochtest` asserts in
[RFC-0026](../rfc/0026-ordering-and-fencing.md), for a writer that declares
that it refuses Zero. The laws of `epoch.Admissible` and `epoch.Watermark`
and the rest of RFC-0026 remain in force.

## Context

A write at `epoch.Zero` is unfenced under the fence laws. It is admitted
whatever the watermark, and it does not move the watermark.
`epochtest.AssertFencedWriter` asserts that law of every writer that it
checks. A zero value where an epoch was meant is then admitted silently,
and the fence laws record that cost among their drawbacks.

A store that grants each scope to one writer at a time cannot accept an
unfenced write once a host has granted the scope an epoch, because two
writers would then share the scope. Such a store refuses a handle at Zero
with `epoch.ErrFenced` in a scope with an authority. A log whose host
grants the epoch of each range of its slots works this way, and checks
every write of its contract with `AssertFencedWriter`. The kit fails such a
store at its law of Zero, and passes it at the four other laws.

## Decision

We will let a writer declare in `FencedSystem.RefusesZero` that a scope
with an authority refuses a handle at `epoch.Zero`, because a writer that
grants each scope to one writer at a time is stricter than the law of Zero
and still has to pass the other fence laws.

- With `RefusesZero`, `AssertFencedWriter` asserts that Open at Zero, or a
  write through its handle, returns an error that matches
  `epoch.ErrFenced` and classifies as `errs.Conflict`. The refusal leaves
  the scope unmutated, and the current authority is still admitted. These
  assertions replace the law of Zero.
- The zero value of `RefusesZero` keeps the law of Zero, so every writer
  that applies the kit today is checked as before.
- The reference writer of `epoch` is checked in both forms.

## Alternatives Considered

### Fence laws of its own for such a store

The store leaves the kit out and checks its fence laws with tests of its
own.

Rejected. The kit asserts that no write of a handle escapes validation,
over every write that the handle exposes. A store that drops the kit for a
stricter rule loses that assertion.

### A writer that refuses Zero for every writer of the kit

The kit asserts the refusal of Zero for every writer.

Rejected. The law of Zero is the documented meaning of `epoch.Zero` in
`epoch.Admissible` and `epoch.Watermark`, and the reference writer of
`epoch` admits unfenced writes and passes the kit today.

## Consequences

**Positive:**

- A writer that grants each scope to one writer at a time passes the kit
  for every one of its writes.
- The cost that the fence laws list for Zero disappears for such a writer:
  a forgotten epoch fails with `epoch.ErrFenced` instead of admitting.

**Negative:**

- The kit asserts two laws of Zero, and the writer chooses one through a
  field of the system that it passes.
- `AssertFencedWriter` takes a `*testing.T`, so no test of core proves that
  the refusal fails a writer that admits Zero. The reference writer proves
  that the law passes.

**Neutral:**

- `epoch.Admissible` and `epoch.Watermark` still admit Zero. A writer that
  refuses Zero checks it before it admits a write.

## References

- Issue 122, <https://github.com/thesm-os/core/issues/122>.
- RFC-0026, ordering and fencing: the fence laws and the suite of
  `coretest/epochtest`.
