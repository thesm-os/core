---
rfc: 0001
title: Clock Seam
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-05-06
updated: 2026-05-06
discussion: none
supersedes: none
superseded-by: none
produces-adr: none
---

# RFC-0001: Clock Seam

## Summary

A unified time seam — `clock.Clock` — that consumers read time and
schedule timers through instead of calling the standard library
directly. The interface returns Hybrid Logical Clock instants for
callers that need causal ordering, plus a `Time()` method for the
common case where only a `time.Time` is needed. Two implementations
ship: `clock/hlc` (production HLC over `time.Now`) and `clock/fake`
(virtual time for tests).

## Motivation

Library code that calls `time.Now`, `time.NewTimer`, or `time.Sleep`
directly is non-deterministic by construction: tests cannot wind
virtual time forward, simulations cannot replay a trace, and code
that depends on relative timing cannot be exercised at sub-second
precision in a unit test. Routing every time read and timer
construction through an injectable interface is the standard
remedy.

Distributed code additionally needs causal ordering across nodes.
Wall time alone is insufficient: clock skew, NTP corrections, and
same-nanosecond ties leave events un-orderable. A Hybrid Logical
Clock — wall time fused with a Lamport counter, tagged with the
originating node — is the canonical solution and gives a total
order over events emitted by the deployment.

A foundational module gets one chance to choose the right shape.
If the seam is wrong, every downstream contract that takes a clock
argument re-litigates the choice.

## Detailed design

```go
type Clock interface {
    Now() Instant                       // HLC instant
    Time() time.Time                    // stdlib projection
    NewTimer(d time.Duration) Timer
    Update(observed Instant) Instant    // HLC merge
}

type Instant struct {
    Wall    int64    // unix nanoseconds
    Logical uint32   // Lamport counter for the current Wall tick
    Node    NodeID   // origin node, final ordering tiebreaker
}
```

`Sleep` and `After` are package-level helpers on `NewTimer`, not
interface methods, so the interface stays at four. `Compare`,
`HappensBefore`, `Sub`, `Add`, `Time`, and `IsZero` are methods on
`Instant`.

`Time()` is a first-class interface method rather than a
`Now().Time()` shorthand because plain wall-time reads are the
dominant call site (log timestamping, calendar arithmetic). The
cost is one extra interface method; the benefit is removing
`.Time()` boilerplate from every site that does not need HLC.

`Update` is on the interface even though pure-test clocks do not
strictly need it, so distributed consumers can drive HLC merges
through the same `Clock` value they hold for everything else.
Implementations that do not carry causal state still honour the
contract that the returned instant is causally after the observed
instant.

The `clock/hlc` implementation follows Kulkarni et al., "Logical
Physical Clocks": `Update` advances local state to
`max(local, observed, physical)` and resets or increments `Logical`
based on which input wins. The `clock/fake` implementation performs
the same merge so test-time `Update` calls honour the same contract
as production.

Allocation contract: `Now`, `Time`, and `Update` are
zero-allocation on both implementations. `NewTimer` allocates the
wrapper struct and the underlying `*time.Timer`; this cost is
documented and not gated.

## Alternatives considered

### A. Plain `time.Time`-based interface

Drop `Update` and the `Instant` type; return only `time.Time`.
Distributed callers layer HLC on top.

**Rejected:** every distributed call site re-implements the HLC
machinery against this thinner seam. Repeated implementations
diverge in the merge logic, the causal-ordering contract, and the
Lamport counter rollover behaviour. Centralising HLC at the seam
removes the divergence.

### B. Two seams — one stdlib-shaped, one HLC-shaped

Each consumer picks the shape they want.

**Rejected:** when a kernel primitive accepts a `clock.Clock`,
callers cannot pass an HLC clock without an adapter. Every
cross-package boundary that wants the HLC guarantee re-introduces
the translation layer this design exists to remove.

### C. HLC interface without `Time()`

Force callers that want a `time.Time` to write `clk.Now().Time()`.

**Rejected:** wall-time-only reads are the dominant call site. One
extra interface method is cheap; threading `.Time()` through every
log statement is not.

## Drawbacks

- The interface carries HLC vocabulary even for callers that do
  not need causal ordering. Mitigated by `Time()` as the
  projection.
- `Instant.Logical` is `uint32` — within a single nanosecond it
  can roll after ~4 billion calls. Theoretically possible,
  practically not. Documented; not addressed here.
- `NewTimer` allocates. No path to zero-alloc with the current
  `*time.Timer` wrapping; revisit if profiling demands it.

## Open questions

- **`Time()` state-mutation semantics.** `hlc.Clock.Time` advances
  HLC state (it calls `Now` internally); `fake.Clock.Time` is a
  pure read. The interface contract ("equivalent to
  `Now().Time()`") implies advance, but the divergence is
  undocumented at the interface level. Harmonise in a follow-up.

## Unresolved / future work

- A `clocktest` conformance suite for third-party `clock.Clock`
  implementations.
