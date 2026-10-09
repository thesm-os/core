---
adr: 0024
title: A Spec With a Terminal State Rejects States That Cannot Finish
status: Accepted
date: 2026-09-27
supersedes: none
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0024: A Spec With a Terminal State Rejects States That Cannot Finish

## Status

Accepted

## Context

`fsm.Builder.Build` validates a lifecycle once, when the lifecycle is
declared. It rejects three kinds of state:

- A state without an outgoing edge that is not terminal.
- A terminal state with an outgoing edge.
- A state that has no path from the initial state.

These checks make an unreachable state, a state that cannot be left and
an edge that can never be taken a construction error in every
lifecycle, whoever declares it.

A cycle of non-terminal states without an exit passed every check. A
machine that enters such a cycle never finishes, although its Spec
declares terminal states.

Apache Helix states the requirement for its replica state models: "There
must be a path to DROPPED for every state in the model". The controller
can then drop a replica in any role. This replica model passed `Build`:

- `offline` leads to `follower` and to `dropped`.
- `follower` leads to `leader`, and `leader` leads to `follower`.
- `dropped` is terminal.

Every state but `dropped` has an outgoing edge. `offline` has a path to
every other state. `follower` and `leader` lead only to each other, so a
replica that becomes a follower can never be dropped.

A lifecycle can come from a user's configuration. `Build` is then the
only check it passes before machines run on it.

## Decision

We will make `Build` reject every state that has an outgoing edge but no
path to a terminal state, with guards ignored, whenever the Spec
declares a terminal state, because a Spec with a terminal state
describes a lifecycle that ends.

## Alternatives Considered

### The declaring system checks the rule itself

A system that loads lifecycles from configuration searches for such
states before it calls `Build`.

Rejected. Every system with terminal states then writes the same
search, and a lifecycle declared without the search still builds.

### An option on the builder

`RequireTerminal()`, or a similar option, turns the check on.

Rejected. Declaring a terminal state already states that the Spec's
machines finish. The option would be a second way to state the same
property.

### Reject every cycle

`Build` rejects any cycle of non-terminal states.

Rejected. Retry loops are cycles by design. A failed job returns to
pending while attempts remain, and it leaves the cycle when it succeeds,
fails for good or is cancelled. Only a cycle without an exit is a
defect.

## Consequences

**Positive:**

- Every state of a Spec that declares a terminal state has a path to a
  terminal state, with guards ignored.
- `Build` runs the check once. A backward search from the terminal
  states over the table of allowed transitions tests each pair of
  states at most once, so it costs O(n²) for n states, as the search for
  unreachable states does.
- A Spec without terminal states builds as before. The circuit of
  `resilience.Breaker` declares none, because a circuit runs for the
  life of its target.

**Negative:**

- A Spec that built before can fail to build. Its author adds an exit to
  each cycle without one, or removes the Spec's terminal states.
- The check ignores guards, as the search for unreachable states does.
  A cycle whose only exit has a guard that is always false still builds.
- `Build` reports every state of such a cycle, so a cycle of ten states
  produces ten errors.

**Neutral:**

- A state without an outgoing edge is reported once, as a state that
  cannot be left. The new check skips it.

## References

- RFC-0031, finite state machines, section Validation.
- ADR-0018, core ships mechanisms, not lifecycles.
- Apache Helix 2.0.1, tutorial on state models,
  <https://helix.apache.org/2.0.1-docs/tutorial_state.html>.
- `fsm/builder.go`: `Builder.Build`, `checkShape` and `mark`.
