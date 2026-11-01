---
rfc: 0083
title: Calls Beyond a Quorum
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-11-01
updated: 2026-11-01
discussion: https://github.com/thesm-os/core/issues/110
supersedes: RFC-0037 in part
superseded-by: none
produces-adr: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# RFC-0083: Calls Beyond a Quorum

## Summary

We propose a rule for the start of each call of `task.Quorum`. A worker
starts a call only while fewer than `limit` calls are in flight, and while
the calls in flight and the calls that succeeded are fewer than the larger
of k and `limit`. With `limit` equal to k, `Quorum` makes at most k calls
and one more for each failure. With `limit` above k, `Quorum` runs at most
`limit` − k calls beyond the quorum at once. The signature of `Quorum` does
not change. The rule replaces the start of the calls that RFC-0037 leaves
to the workers of `Quorum`.

## Motivation

Each worker of `Quorum` claims the next item as soon as its call returns.
A worker whose call succeeded starts a call while another call is in
flight. The quorum needs that call only when the call in flight
fails:

- The reproduction of issue 110 calls `Quorum` with `limit` and k both 2
  over three items. Item 0 succeeds at once, and item 1 waits for a
  release. Before the release, `Quorum` has called item 2, decided the
  quorum with items 0 and 2, and cancelled item 1. The reproduction fails
  20 of 20 runs at v0.37.0.
- Some services charge for each call. An RFC 3161 time-stamp authority can
  charge for each token, and it can process a request that the decision
  cancels. With `limit` and k both 2 over three authorities, `Quorum` sends
  a request to the third authority as soon as the first token arrives.

A caller that must not make a call that its quorum does not need writes
its own fan-out. It counts the calls in flight and the calls that
succeeded, and it starts the next call only when a call fails. Package
`task` exists to replace such code.

## Detailed design

### The rule

A worker starts a call only when both of these conditions are true:

- Fewer than `limit` calls are in flight.
- The calls in flight and the calls that succeeded are fewer than the
  larger of k and `limit`.

| Setting | Calls of `Quorum` |
|---|---|
| `limit` at or above the number of items | Every call starts at once |
| `limit` above k and below the number of items | At most `limit` − k calls beyond the quorum at once |
| `limit` equal to k | At most k calls and one more for each failure |
| `limit` below k | At most `limit` calls at once, and no call beyond k |

Each call in flight and each call that succeeded takes a place. A call
that succeeds keeps its place, because it counts toward k. A call that
fails frees its place for the next item. A call that ends by
`runtime.Goexit` counts as a failure with `ErrExited`. It frees its place
too.

### Invariants

1. At most `limit` calls are in flight.
2. The calls in flight and the calls that succeeded are at most the larger
   of k and `limit`. They are also at most the number of items.
3. `Quorum` makes at most the larger of k and `limit` calls plus one call
   for each failure. It makes at most one call for each item.
4. The decision of RFC-0037 is unchanged. `Quorum` returns nil once k calls
   succeed. It returns `ErrNoQuorum` once fewer than k calls can still
   succeed. It returns the cause of ctx when ctx ends first.
5. The rule does not stop a quorum before its decision. While no call is in
   flight and items remain, the places in use are the successes alone.
   They are fewer than k, so a worker starts the next item. Once every
   item has returned, k calls succeeded or fewer than k can succeed.

### Implementation

Each place is a value in a channel. The capacity of the channel is
`min(max(k, limit), len(items))`.

- A worker sends a value into the channel before it claims an item. It
  waits while the channel is full. It stops when the context of the calls
  ends.
- A worker whose call failed receives one value from the channel.
- A worker whose call succeeded keeps its value in the channel.
- The deferred function of a worker that `runtime.Goexit` ends receives
  one value from the channel and starts a new worker.

`Quorum` starts as many workers as the lower of `limit` and the number of
items. Each worker runs one call at a time. A worker selects on the
context only when the channel is full. A call of `Quorum` in which no
worker waits does not allocate the Done channel of its context.
The channel adds one allocation to each call of `Quorum`. The allocations
of a call do not grow with the number of items.

### API

The signature of `Quorum` does not change. Its docblock gains the rule:

```go
// Quorum calls fn for the elements of items and returns nil as soon as k
// calls have returned nil. At most limit calls run at once. Once k calls
// have succeeded, Quorum cancels the context of the calls still running
// and does not call fn for the elements left.
//
// # Calls beyond the quorum
//
// Each call in flight and each call that succeeded takes a place. Quorum
// has as many places as the larger of k and limit. It starts a call only
// in a free place. A call that succeeds keeps its place. A call that
// fails frees its place for the next element.
//
// With limit equal to k, Quorum makes at most k calls and one more for
// each failure. With limit above k, Quorum runs at most limit − k calls
// beyond the quorum at once. With limit at or above the number of items,
// Quorum starts every call at once.
func Quorum[E any](ctx context.Context, limit, k int, items []E, fn func(ctx context.Context, i int, item E) error) error
```

### Failure handling

| Condition | Result |
|---|---|
| A call fails before the decision | Its place frees, and the next item starts |
| A call ends by `runtime.Goexit` | `ErrExited` as its failure. Its place frees, and a new worker replaces the worker that ended |
| A call succeeds before the decision | It keeps its place |
| The decision or the end of ctx | Every worker stops. A worker that waits for a place stops too |
| `limit` below one, or k outside one to the number of items | `ErrLimit` or `ErrQuorumSize` |

### Tests

| # | Guarantee |
|---|---|
| 1 | With `limit` and k both 2 over three items, `Quorum` does not call item 2 while item 0 has succeeded and item 1 is in flight. The case runs in a `testing/synctest` bubble |
| 2 | `Quorum` starts the next item in place of a failed call |
| 3 | With `limit` above k, `Quorum` runs at most `limit` − k calls beyond the quorum |
| 4 | With `limit` below k, `Quorum` runs at most `limit` calls at once and does not start a call beyond k |
| 5 | A property over generated limits, quorums and results checks the invariants 1 to 4 |
| 6 | A call that ends by `runtime.Goexit` frees its place for the next item |
| 7 | The allocation test and the benchmark check the count of the allocation contract |
| 8 | The specs pass under the race detector and cover every statement. dokimi-mutate-go detects every mutant |

### Migration

A caller with `limit` below the number of items can get fewer calls. A
caller that wants calls beyond the quorum passes a `limit` above k. A
caller with `limit` at or above the number of items gets the calls of
v0.37.0.

## Alternatives considered

### A. A second function with the rule

Issue 110 proposes a second function with the rule, so `Quorum` would keep
its behaviour for the callers that rely on it.

**Why not:** a caller of v0.37.0 cannot choose the call that a success
starts, and RFC-0037 does not argue for that call. Under the rule, a caller
gets calls beyond the quorum through a `limit` above k. A second function
would keep the unneeded calls in `Quorum`. Each caller would then have to
choose between two functions with one purpose.

### B. The rule of issue 110 alone

A worker would start a call only while the calls in flight and the calls
that succeeded are fewer than `limit`.

**Why not:** with `limit` below k, a quorum would not start a call after
`limit` successes. It could then never count k successes. The rule takes
the larger of k and `limit` as the number of places. A quorum then has a
place for each of its k successes, and at most `limit` calls are in
flight.

### C. A count of spare calls as a parameter

`Quorum` would take `limit` for the calls in flight and a separate count
of the calls beyond the quorum.

**Why not:** the signature would change for every caller. A `limit` above
k gives the same count, and `limit` keeps its meaning as the bound of the
calls in flight.

### D. A spare call after a delay

`Quorum` would start a call beyond the quorum when a call in flight runs
longer than a delay.

**Why not:** the delay is a policy of the caller, and `Quorum` does not
take a clock or a delay. A caller passes a `limit` above k for spare calls
at once. The verdict changes when a caller needs a spare call that waits
for a delay.

## Drawbacks

- A caller with `limit` below the number of items can get fewer calls.
  `Quorum` does not start a spare call after a success while a slow call
  is in flight, so such a quorum can take longer.
- The change of behaviour is breaking. The changelog marks it with
  **Breaking:**.
- The channel of places adds one allocation to each call of `Quorum`. A
  call in which a worker waits for a place also allocates the Done channel
  of its context.

## Open questions

None.

## Unresolved / future work

None.

## References

- Issue 110, <https://github.com/thesm-os/core/issues/110>.
- RFC-0030, structured concurrency.
- RFC-0037, periodic and quorum tasks.
