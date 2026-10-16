---
adr: 0063
title: Waiting Calls of a Limiter Take Units by Priority
status: Accepted
date: 2026-10-16
supersedes: none
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0063: Waiting Calls of a Limiter Take Units by Priority

## Status

Accepted

## Context

`resilience.Limiter.WaitN` reserved the units of a call when the call
started, so a call waited behind every call that started before it. A
flush that started after the chunks of four compactions returned after 4
seconds on a limiter of 1 MiB per second, although the writers of its
volume waited for it.

## Decision

We will grant the units of waiting calls in the order of their priorities
when the bucket covers them, because an urgent call then takes its units
before the calls of lower priority that started before it.

- `Priority` has `PriorityNormal` and `PriorityHigh`.
  `WaitNPriority(ctx, n, p)` waits at p, and `WaitN` waits at
  `PriorityNormal`. A priority that is not valid returns `ErrPriority`,
  classified `errs.Invalid`.
- The Limiter keeps one queue of waiting calls per priority, in the order of
  their starts. The next call that it grants is the oldest high call. It is
  the oldest normal call when no high call waits, and after nine grants in a
  row to high calls while a normal call waited.
- A high call that starts while a normal call is next takes its place,
  unless the normal call has the tenth grant.
- A call that the bucket covers while no call waits takes its units at
  once. The call that the Limiter grants next waits on a timer, and every
  other waiting call waits on a channel.
- A grant moves `due` by the exact cost of its units. A cancelled call took
  no units, so it gives none back.
- `AllowN` takes units only while no call waits.

This decision supersedes the reservation at the start of a call that
RFC-0050 describes.

## Alternatives Considered

### Strict priority without a share

Rejected. A steady stream of high calls starves the normal calls.

### Refills once per period

Rejected. A call waits for the next refill when the bucket would cover it
sooner.

### A random share

Rejected. A test of the order of the grants would depend on the random
source.

## Consequences

**Positive:**

- A high call waits for at most the units that the bucket lacks and every
  tenth grant to a normal call.
- A cancelled call no longer delays the calls behind it.

**Negative:**

- A normal call can take its units after a high call that started after it.
- A call that waits allocates its place in a queue and its channel beside
  its timer.

**Neutral:**

- The bucket keeps the exact arithmetic of ADR-0055.

## References

- RFC-0067, priorities for the waiting calls of a limiter.
- RFC-0050, the rate limiter.
- ADR-0055, the Limiter counts units exactly.
- Issue 55, <https://github.com/thesm-os/core/issues/55>.
