---
adr: 0082
title: An HLC Clock Reads Its Time from a Physical Clock
status: Accepted
date: 2026-11-08
supersedes: none
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0082: An HLC Clock Reads Its Time from a Physical Clock

## Status

Accepted

## Context

`hlc.New` reads the wall time from `time.Now`, and `hlc.NewWithSource`
reads it from a function of the caller. Both constructors read `time.Now`
once. `Clock.Elapsed` measures from that reading on the monotonic clock of
the system, and `Clock.NewTimer` returns a timer of `time.NewTimer`.

A deterministic simulation runs each machine over the clock of a simulated
host. It replays each case and compares the two runs. Every time that a
machine reads must then come from the simulation.

- A clock of `NewWithSource` over the clock of a host keeps a reading of
  the system in its state, and its `Elapsed` reads the system on every
  call. On core v0.41.0, two clocks over one source that never advances
  returned the durations 1.08 ms and 23 µs.
- A simulation that fingerprints the effects of its machines diverges at
  the first effect that contains such a clock.
- `fake.Clock` reads nothing from the system. It advances only through
  `Advance` and `Set`, so it cannot follow the clock of a host.

The durations and the timers of a clock depend on each other.
`Clock.NewTimer` sends its tick once at least the duration has elapsed on
the time source of the clock. `resilience.Limiter` waits on a timer for the
time in which `Elapsed` says its bucket gains the units of a call, and then
reads `Elapsed` again. Under a clock that reads `Elapsed` from a source and
its timers from the system, the Limiter wakes on the schedule of the
system. A source that does not advance then keeps the Limiter waiting on
one timer after another.

## Decision

We will let an HLC `Clock` read its wall time, its durations and its timers
from a physical clock of the caller. Every reading and every timer of such
a clock then comes from the caller, and its timers end on the time of its
`Elapsed`.

```go
// Physical is the physical clock of a Clock.
type Physical interface {
	Time() time.Time
	Elapsed() time.Duration
	NewTimer(d time.Duration) clock.Timer
}

func NewOver(node clock.NodeID, maxOffset time.Duration, physical Physical) *Clock
```

- `Now` and `Update` merge the Unix time of `physical.Time` in
  nanoseconds. `Elapsed` returns `physical.Elapsed`, and `NewTimer` returns
  `physical.NewTimer`.
- Every `clock.Clock` is a `Physical`. A simulation that has only the wall
  time of a host takes the durations and the timers of a `fake.Clock` that
  it does not advance.
- `New` and `NewWithSource` keep their contracts. Each one returns a Clock
  over a physical clock of the package. `New` reads the system clock.
  `NewWithSource` reads the wall time of its source and the durations and
  the timers of the system.
- The suite of `coretest/clocktest` checks a Clock over a `fake.Clock`.

## Alternatives Considered

### A second source for Elapsed

`hlc.NewWithSources(node, maxOffset, wall, elapsed func() int64)` reads the
wall time and the time of `Elapsed` from two functions.

Rejected. A `Limiter` over such a clock would wait on the schedule of the
system, because `NewTimer` would still return a timer of the system. The
tick of that timer arrives before the duration has elapsed on the source
of `Elapsed` whenever that source runs slower than the system.

### Three functions of the caller

`NewOver` takes one function for the wall time, one for `Elapsed` and one
for the timers.

Rejected. An interface takes the methods of one physical clock as one
value. A `clock.Clock` satisfies that interface without an adapter, and a
`fake.Clock` is one.

### An origin of Elapsed at the start of the process

`New` and `NewWithSource` measure `Elapsed` from a reading of the package,
so the state of a clock contains no reading of the system.

Rejected. Two clocks over one source would have equal states, but
`Elapsed` would still read the system.

## Consequences

**Positive:**

- A Clock over a physical clock that reads nothing from the system is
  deterministic. Two such clocks over equal physical clocks return equal
  instants and durations.
- The timers of every Clock end on the time of its `Elapsed`.
- A test can run the merge of an HLC over the virtual time of a
  `fake.Clock`, with a node and a max offset of its own.

**Negative:**

- A caller that has only a wall time supplies `Elapsed` and `NewTimer` as
  well.
- A Clock of `New` reads its time through an interface. On one core of an
  AMD Ryzen 9 9950X3D with Go 1.27.3, in four alternating rounds of three
  runs per side, `Elapsed` took 17.78 ns against 17.06 ns before, 4.2%
  more. benchstat did not find a difference for `Now` and `Update`, and
  none of the three allocates.

**Neutral:**

- The package has three constructors. `NewWithSource` remains for the tests
  of the merge, which change the wall time alone.

## References

- Issue 130, <https://github.com/thesm-os/core/issues/130>.
- RFC-0084, durations and peer instants of a clock: `Elapsed` of
  `hlc.Clock`.
- RFC-0001, the clock seam: simulations that replay a trace.
- `resilience.Limiter`, the wait of `WaitN`.
- CockroachDB at d30c905, `pkg/util/hlc/hlc.go`: `NewClock` over the
  `WallClock` of its caller,
  <https://github.com/cockroachdb/cockroach/blob/d30c905fff79ef825adc96bcc647f1872a90f2ff/pkg/util/hlc/hlc.go#L96-L99>.
- TigerBeetle at 68958eb, `src/stdx/time.zig`: `Time`, the monotonic and
  the real time that the code of a replica reads in place of the operating
  system,
  <https://github.com/tigerbeetle/tigerbeetle/blob/68958eb757747d0fc96a79d1f9c2fa6766b67b48/src/stdx/time.zig#L15-L37>.
