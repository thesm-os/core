---
rfc: 0084
title: Durations and Peer Instants of a Clock
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-11-01
updated: 2026-11-01
discussion: https://github.com/thesm-os/core/issues/112
supersedes: RFC-0001 in part, RFC-0050 in part, RFC-0051 in part
superseded-by: none
produces-adr: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# RFC-0084: Durations and Peer Instants of a Clock

## Summary

We propose three changes of `clock.Clock`:

- `Clock.Elapsed` returns the time since an origin that the clock fixes at
  its construction. A step of the wall time does not change it, and
  `hlc.Clock` reads it without a lock. Every duration that core measures
  moves from `Clock.Time` to `Clock.Elapsed`.
- `Clock.Update` returns `ErrOffset` for a peer instant whose wall is
  further ahead of the physical time than the max offset of the clock.
  `hlc.Clock` takes its max offset at construction.
- A Logical counter at its maximum moves the wall forward by one nanosecond
  and starts again at 1. It no longer wraps to 0.

## Motivation

### Durations

A caller that times a call reads `Clock.Time` twice and subtracts the
readings. `hlc.Clock.Time` calls `Now`, which locks a mutex for every
reading and keeps the last wall value until the source passes it
(`clock/hlc/hlc.go:71-85`). The reproduction of issue 112 measures both
effects:

- A source that steps back by 500 ms measures a duration of 0 s for the
  next 100 ms. A forward step of 2 s measures 2.1 s.
- Two readings of `hlc.Clock.Time` cost 89.0 to 89.1 ns on one goroutine,
  and 40.5 to 44.5 ns per pair on four goroutines. `time.Now` with
  `time.Since` costs 49.2 to 50.3 ns and 12.3 to 12.4 ns.

Core measures these durations with `Clock.Time`:

| Package | Duration |
|---|---|
| `net/httpclient` | The time of each request (`client.go:865-867`) |
| `net/httpserver` | The time of each request (`chain.go:146`, `:205`) |
| `resilience` | The bucket of `Limiter`, the budget window of `Retrier` and the open interval of `Breaker` |
| `telemetry` | The rate of `BoundedHistogram.Flush` and the interval of `RateLimitHandler` |
| `tlog/witness` | The time of a commit and the age of the state |

RFC-0050 accepts that a `Limiter` does not gain units after a backward step
until the clock passes its last reading again. RFC-0051 accepts that a
`RateLimitHandler` delays the records of an event in the same case. A
forward step gives a full bucket, ends an open interval early and drops
the records of a whole interval from a rate.

### Peer instants

`hlc.Clock.Update` takes the largest of the local wall, the peer wall and
the physical time (`clock/hlc/hlc.go:122`). One peer whose clock is a year
ahead moves the clock of each node that receives its instants a year
ahead. The wall of an HLC never moves back, so the clock of that node then
stays a year ahead.

`Update` computes the next Logical as a `uint32` plus 1
(`clock/hlc/hlc.go:132`). A peer instant at the wall of the clock with
Logical 2^32 − 1 makes the next Logical 0, so the instant that `Update`
returns happens before the instant of the peer. One message of a peer
triggers it. RFC-0001 lists the rollover as a drawback that it does not
address.

## Detailed design

### Elapsed

```go
// Elapsed returns the time since an origin that the clock fixes once, at
// its construction. The difference of two readings is the duration
// between them. Elapsed never decreases, and a step of the wall time does
// not change it.
Elapsed() time.Duration
```

| Implementation | Elapsed |
|---|---|
| `hlc.Clock` | `time.Since` of a `time.Now` that `New` and `NewWithSource` read. The monotonic reading of Go's time does not step, and the call takes no lock |
| `fake.Clock` | The sum of the durations of `Advance` since `New`. `Set` and `Update` change the wall time alone, as a step of a wall clock leaves a monotonic clock unchanged |

`Elapsed` does not allocate. The package documentation of `clock` states
that a caller measures a duration with `Elapsed` and reads an instant with
`Time` or `Now`.

### Durations in core

Each duration of the motivation moves to `Elapsed`:

- `httpclient` and `httpserver` read `Elapsed` before and after a request.
- `Limiter` measures its time as the `Elapsed` since its construction. Its
  time never moves back, so the bucket is never below empty after a step of
  the clock. `SetRate` no longer keeps the time at which such a bucket is
  empty.
- `Retrier` moves its budget buckets by `Elapsed`.
- `Breaker` ends its open interval when `Elapsed` passes the `Elapsed` at
  which the circuit opened plus `OpenFor`.
- `BoundedHistogram.Flush` computes its rate from the `Elapsed` since the
  previous Flush.
- `RateLimitHandler` measures the interval of an event with `Elapsed`.
- The witness server measures the time of a commit and the age of its state
  with `Elapsed`.

The instants of core stay on `Clock.Time`: the expiries of `cache`, the
expiry of a signed request of `blob/memory`, the modification time of
`blob/memory`, the date of a `Retry-After` of `httpclient`, the time of
destruction of `crypto/localkey` and the time of a token of `tsptest`.

### Peer instants

```go
// Update merges an observed peer instant into the local clock and returns
// the resulting instant, which is causally after the observed instant.
//
// Update returns ErrOffset, and leaves the clock unchanged, when the wall
// of observed is further ahead of the physical time of the clock than its
// max offset.
Update(observed Instant) (Instant, error)
```

- `clock.ErrOffset` is a sentinel of package `clock`, classified
  `Integrity`, because the instant of the peer failed a check of its
  plausibility. A caller refuses the message that contains the instant.
- `Update` returns the sentinel itself, so the call does not allocate.
- `hlc.New(node, maxOffset)` and `hlc.NewWithSource(node, maxOffset,
  source)` take the max offset. A max offset below zero counts as zero, so
  the clock refuses every peer wall ahead of its physical time.
- `clock.DefaultMaxOffset` is 500 ms. A deployment whose clocks NTP keeps
  within that bound of each other passes it to `hlc.New`.
- `fake.Clock` refuses a peer wall more than `clock.DefaultMaxOffset` ahead
  of its virtual time.
- `hlc.Clock` compares the peer wall with the time of its source, not with
  its own wall, so a clock that a peer moved ahead does not widen its own
  bound.
- The sum of the physical time and the max offset saturates at the largest
  wall, so the check does not overflow for any wall of the source.

Each of these systems refuses a peer that is too far ahead:

- CockroachDB refuses a batch whose remote wall time is more than its max
  offset ahead of its physical clock, 500 ms by default, and leaves its
  clock unchanged (`UpdateAndCheckMaxOffset` of `pkg/util/hlc`).
- MongoDB refuses a command whose cluster time is more than
  `maxAcceptableLogicalClockDriftSecs` ahead of its wall clock, a year by
  default.
- The HLC paper of Kulkarni et al. ignores a message that moves the clock
  too far from the physical time, and notifies the administrator.

### Logical at its maximum

`Now` and `Update` compute the next instant from a wall and a Logical. When
the Logical is 2^32 − 1, the next instant has the wall plus one nanosecond
and Logical 1. That instant is after every instant of the same wall. The
same rule applies to `fake.Clock`.

YugabyteDB moves the physical part of its time forward by a microsecond
when its 12 logical bits overflow. MongoDB moves its cluster time to the
next second when its increment passes 2^31 − 1. `Timestamp.Next` of
CockroachDB returns the next wall time after the largest Logical.

### The suite of a Clock

The suite of `coretest/clocktest` gains these checks, each with a planted
defect:

| Check | Law |
|---|---|
| `ElapsedDoesNotDecrease` | Each of 100 readings of `Elapsed` is at or above the reading before it |
| `ElapsedIsNotNegative` | `Elapsed` does not return a negative duration |
| `UpdateRefusesAPeerAYearAhead` | `Update` returns an error that wraps `ErrOffset` for a peer a year ahead |
| `UpdateKeepsTheWallForARefusedPeer` | After a refused `Update`, the instant of `Now` happens before the instant of the peer |
| `UpdatePassesAPeerAtTheLastLogical` | `Update` of a peer at the wall of the clock with Logical 2^32 − 1 returns a later instant |

`UpdateFollowsAPeerAhead` checks a peer a millisecond ahead in place of an
hour ahead. `UpdateReturnsALaterInstant` and `UpdateKeepsTheNode` check a
peer at the wall of `Now` in place of a peer at 2026-01-01, which a clock at
the Unix epoch refuses.

### Invariants

1. `Elapsed` never decreases, and a step of the wall time does not change
   it.
2. Each instant of a clock happens after every earlier instant of the same
   clock, also when the Logical reaches 2^32 − 1.
3. `Update` that returns nil returns an instant after the observed instant.
4. `Update` that returns `ErrOffset` leaves the wall and the Logical of the
   clock unchanged.
5. The wall of a clock is at most one nanosecond per rollover of the Logical
   beyond the largest of its physical time plus its max offset and its
   earlier wall.

### API

```go
package clock

// DefaultMaxOffset is the max offset of a clock in a deployment whose
// clocks NTP keeps within 500 ms of each other.
const DefaultMaxOffset = 500 * time.Millisecond

// ErrOffset is returned by Clock.Update for a peer instant whose wall is
// further ahead of the physical time than the max offset of the clock. It
// classifies as errs.Integrity.
var ErrOffset error

type Clock interface {
	Now() Instant
	Time() time.Time
	Elapsed() time.Duration
	NewTimer(d time.Duration) Timer
	Update(observed Instant) (Instant, error)
}
```

```go
package hlc

func New(node clock.NodeID, maxOffset time.Duration) *Clock
func NewWithSource(node clock.NodeID, maxOffset time.Duration, source func() int64) *Clock
```

### Failure handling

| Condition | Result |
|---|---|
| A peer wall at most the max offset ahead of the physical time | `Update` merges it |
| A peer wall further ahead | `ErrOffset`, and the clock is unchanged |
| A max offset below zero | The clock refuses every peer wall ahead of its physical time |
| A Logical of 2^32 − 1 | The next instant has the wall plus 1 ns and Logical 1 |
| A step of the wall time | `Elapsed` and every duration of core are unchanged |

### Tests

| # | Guarantee |
|---|---|
| 1 | `hlc.Clock.Elapsed` does not change when its source steps back or forward |
| 2 | `fake.Clock.Elapsed` adds the duration of each `Advance`, and `Set` and `Update` leave it unchanged |
| 3 | `Update` of `hlc.Clock` and of `fake.Clock` returns `ErrOffset` for a peer beyond the max offset and merges a peer at the max offset |
| 4 | `Now` and `Update` move the wall forward by 1 ns at Logical 2^32 − 1 |
| 5 | Each duration of core is unchanged by a `Set` of a fake clock, and follows its `Advance` |
| 6 | The suite of `coretest/clocktest` falsifies each new check with its planted defect |
| 7 | The allocation tests state 0 allocations for `Elapsed` and `Update` |
| 8 | The specs cover every statement, and dokimi-mutate-go detects every mutant of the changed lines |

### Migration

- An implementation of `clock.Clock` outside core adds `Elapsed` and the
  error result of `Update`.
- A caller of `hlc.New` and `hlc.NewWithSource` passes a max offset, such
  as `clock.DefaultMaxOffset`.
- A caller of `Update` refuses the message of a peer for which `Update`
  returns `ErrOffset`.
- A test that moves a duration forward with `fake.Clock.Set` moves it with
  `Advance`.

## Alternatives considered

### A. An optional interface beside Clock

Issue 112 proposes an interface `clock.Monotonic` with `Elapsed`, which a
caller asserts once.

**Why not:** each caller then keeps a fallback to `Time` for a clock
without the method, and the fallback keeps both defects. `Clock` has four
methods, far below the limit of ten of `interfacebloat`.

### B. Since of a time.Time

`Clock` would gain `Since(t time.Time) time.Duration`. clockwork,
`k8s.io/utils/clock`, quartz and the clocks of benbjohnson and Cloud
Foundry declare it, and their real clocks call `time.Since`.

**Why not:** `Since` measures a duration only when `t` carries the
monotonic reading of `time.Now`. `hlc.Clock.Time` builds its time from the
HLC wall, which carries none. The fakes of these libraries also move one
time, so a test cannot step the wall time apart from the passage of time.

### C. A monotonic reading in Time

`hlc.Clock.Time` would return `time.Now` with its monotonic reading.

**Why not:** `Time` is `Now().Time()` by contract, the wall of an HLC
instant, which can be ahead of `time.Now`.

### D. A clamp of the peer wall

`Update` would adopt at most the physical time plus the max offset.

**Why not:** `Update` would then return an instant that does not happen
after the observed instant, and the caller could not tell.

### E. A checked Update beside Update

`hlc.Clock` would gain a second method that checks the max offset, as
CockroachDB has `UpdateAndCheckMaxOffset` beside `Update`.

**Why not:** the unchecked `Update` would stay on `Clock`, and a caller of
it would still adopt any peer wall.

### F. A Logical that wraps

The Logical would wrap to 0, as RFC-0001 accepts.

**Why not:** one message of a peer then breaks the order of the instants
of a clock.

## Drawbacks

- The change breaks each implementation of `clock.Clock` outside core, each
  caller of `Update`, and each caller of `hlc.New` and `hlc.NewWithSource`.
- A clock reads two kinds of time. A duration of `Elapsed` and an instant
  of `Time` do not mix, and a caller that subtracts one from the other gets
  a meaningless value.
- A node whose clock is behind its peers by more than the max offset
  refuses their instants until NTP corrects its clock.
- `fake.Clock.Set` no longer moves the durations of core.

## Open questions

None.

## Unresolved / future work

None.

## References

- Issue 112, <https://github.com/thesm-os/core/issues/112>.
- RFC-0001, the clock seam.
- RFC-0050, the rate limiter.
- RFC-0051, bounded hot-path telemetry.
- S. Kulkarni, M. Demirbas et al., "Logical Physical Clocks and Consistent
  Snapshots in Globally Distributed Databases", 2014:
  <https://cse.buffalo.edu/tech-reports/2014-04.pdf>.
- CockroachDB, `pkg/util/hlc/hlc.go`:
  <https://github.com/cockroachdb/cockroach/blob/d30c905fff79ef825adc96bcc647f1872a90f2ff/pkg/util/hlc/hlc.go#L500-L535>.
- MongoDB, `maxAcceptableLogicalClockDriftSecs`:
  <https://github.com/mongodb/mongo/blob/aedd8cadc73a20fb537e7d397c5b1d07c4da6b19/src/mongo/db/topology/vector_clock/vector_clock_document.idl#L31-L44>.
- YugabyteDB, `HandleLogicalComponentOverflow`:
  <https://github.com/yugabyte/yugabyte-db/blob/8ec04d63f4e71840f56ac855dd658b7b5c5d531f/src/yb/server/hybrid_clock.cc#L327-L340>.
- The package `time` of Go, monotonic clocks:
  <https://pkg.go.dev/time#hdr-Monotonic_Clocks>.
