---
adr: 0018
title: Core Ships Mechanisms, Not Lifecycles
status: Accepted
date: 2026-09-24
supersedes: none
superseded-by: none
---

# ADR-0018: Core Ships Mechanisms, Not Lifecycles

## Status

Accepted

## Context

A lifecycle is a set of named states, the transitions between them,
and the timings and side effects of each transition. A lease is
acquired, renewed and revoked. A job is pending, running, succeeded,
failed or cancelled. Each is built from a few general mechanisms, and
core ships those mechanisms:

- `epoch.Admissible`, `epoch.Watermark` and `epoch.ErrFenced` apply the
  fence rules that a lease enforces on the writes of a deposed holder.
- `fsm.Spec` declares a transition table and validates it at
  construction, and `fsm.Machine` runs guards and actions over it.
- `task` bounds the goroutines that the work of a transition starts.

The ordering and fencing design weighed a lease lifecycle with
acquisition, renewal, revocation callbacks and poisoning of a revoked
handle. The state machine design weighed ready-made machines for a job
lifecycle and a lease lifecycle. Both designs chose the mechanism
alone.

Every state machine in core today describes one of core's own
algorithms. `resilience.Breaker` runs each circuit as an
`fsm.Machine`.

## Decision

We will keep lifecycles such as leases, jobs and workflows out of core
and ship only the mechanisms they are built on, because a lifecycle's
states, transitions and timings are the policy of the system that runs
it.

## Alternatives Considered

### A lease lifecycle in core

Acquisition, renewal, revocation callbacks, and poisoning a handle
after revocation.

Rejected. Issuing a lease is leader election, which core does not
ship, and the holder's discipline after revocation differs between
systems. Core ships the fence rules a lease enforces, and
`coretest/epochtest` checks that a fenced writer applies them.

### A job lifecycle in core

A ready-made `fsm.Spec` for pending, running, succeeded, failed and
cancelled jobs.

Rejected. A job lifecycle belongs to the scheduler that runs jobs, and
core has no scheduler. The fsm design declares that lifecycle in a
single builder chain, so a scheduler loses little by declaring its own.

## Consequences

**Positive:**

- Core's API contains no domain states. A proposal to add one is
  answered by this decision.
- Each system declares its lifecycle over a validated `fsm.Spec`, so
  unreachable states, states that cannot be left and edges that can
  never be taken are construction errors in every lifecycle, whoever
  writes it.

**Negative:**

- Systems that need the same lifecycle each declare it, and their
  declarations can drift apart.
- Core has no conformance suite for a lifecycle. `coretest/epochtest`
  checks a lease's fence rules, and the lease's own tests check
  everything else.

**Neutral:**

- A proposal to ship a lifecycle in core supersedes this ADR rather
  than amending it.

## References

- RFC-0026, ordering and fencing, alternative E.
- RFC-0031, finite state machines, alternative I.
