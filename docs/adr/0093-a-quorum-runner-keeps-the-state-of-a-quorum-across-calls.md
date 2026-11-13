---
adr: 0093
title: A Quorum Runner Keeps the State of a Quorum Across Calls
status: Accepted
date: 2026-11-13
supersedes: none
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0093: A Quorum Runner Keeps the State of a Quorum Across Calls

## Status

Accepted

## Context

`task.Quorum` allocates 5 objects per call: the derived context, its cancel
function, the state of the call, the channel of the places and the closure
that the workers share. A signer that signs each message with k of n keys
calls Quorum once per message. When each call of the function allocates
nothing, such as an Ed25519 signature that appends into a buffer of the
caller, these 5 objects are the whole cost of a message.

Only the derived context and its cancel function belong to one call. The
state, the channel and the closure can serve every call of a caller that
runs one quorum at a time.

## Decision

We will add `task.QuorumRunner`, a type whose zero value runs one quorum at a
time with the rules of `Quorum`, because a caller that keeps it allocates
only the context of each call.

```go
type QuorumRunner[E any] struct {
	// unexported fields
}

func (q *QuorumRunner[E]) Run(
	ctx context.Context,
	limit, k int,
	items []E,
	fn func(ctx context.Context, i int, item E) error,
) error
```

- `QuorumRunner` keeps the count of the results, the channel of the places,
  the list of failures and the closure of its workers across calls.
- `Run` makes a new channel of places only when the places of a call differ
  from the capacity of the channel that it keeps.
- After the workers of a call return, `Run` empties the channel of places.
  It drops the context, the items, the function, the result and the
  failures of the call, so a kept `QuorumRunner` does not keep them alive.
- A mutex lets one call of `Run` at a time use the state. Concurrent calls
  run one after the other.
- `Quorum` runs on a new `QuorumRunner` without the mutex and without the
  reset, because it discards the runner after its call.

## Alternatives Considered

### A pool of states inside Quorum

`Quorum` would take its state from a pool and put the state back after the
call.

Rejected. The state has the type of the elements, and Go has no package
variable of a generic type. One pool would keep the states of every element
type as `any` behind type assertions. A pool of the state without the
elements leaves a closure over the items and the function in each call,
which makes 3 objects. A collection also empties a `sync.Pool`.

### An error for a concurrent call of Run

A call of `Run` while another call runs would return a sentinel error.

Rejected. A caller that shares a `QuorumRunner` by mistake would fail its
calls at run time. The mutex does not delay a caller that runs one quorum
at a time, because such a caller never waits for it.

### No lock

`Run` would leave the order of its calls to the caller, as a `bytes.Buffer`
does.

Rejected. Two concurrent calls would share the count, the channel and the
failures of one state. A call could then return the result of the other.

## Consequences

**Positive:**

- A call of `Run` over a quorum of two of five calls allocates 2 objects and
  96 bytes. A call of `Quorum` allocates 5 objects and 440 bytes. Both
  counts hold at 1, 2 and 4 cores.
- `Quorum` keeps its rules and its 5 objects. It takes 967.2 ns against
  991.2 ns before on one core, and the same time on four cores.

**Negative:**

- A call of `Run` that waits for another call does not end at the end of
  its context.
- A kept `QuorumRunner` keeps its channel of places and the capacity of its
  list of failures between calls.
- A call of `Quorum` allocates 440 bytes against 432 bytes before, because
  the state of a call contains the context, the items and the function.

Four alternating rounds of three runs per build measured these times on an
AMD Ryzen 9 9950X3D with Go 1.27.4.

**Neutral:**

- The first call of `Run` allocates the closure of the workers and the
  channel of places.

## References

- Issue 157, <https://github.com/thesm-os/core/issues/157>.
- RFC-0083, calls beyond a quorum: the places of `Quorum`.
