---
rfc: 0005
title: Epoch — In-Process Monotonic Counter
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-05-06
updated: 2026-05-06
discussion: none
supersedes: none
superseded-by: none
produces-adr: none
---

# RFC-0005: Epoch — In-Process Monotonic Counter

## Summary

A `uint64`-shaped value type expressing position in a strictly-
monotonic sequence, plus a thread-safe `Counter` for in-process
advancement. Used for leader generations, schema versions,
optimistic-concurrency tokens, membership incarnations.

## Motivation

Every component that has versioned state — kernels, schedulers,
caches, leader-elected services — reaches for "some monotonic
counter" and ends up with three incompatible spellings: a bare
`uint64`, an `atomic.Uint64`, a wrapping struct with arbitrary
methods. Promoting the shape into core gives every consumer a
single vocabulary for "which generation am I in?"

## Detailed design

```go
type Epoch uint64

const Zero Epoch = 0

func (e Epoch) IsZero() bool
func (e Epoch) Compare(other Epoch) int
func (e Epoch) Successor() Epoch
func (e Epoch) String() string

type Counter struct { ... }
func NewCounter(start Epoch) *Counter
func (c *Counter) Next() Epoch
func (c *Counter) Current() Epoch
```

`Epoch` is the position; `Counter` is the producer. `Next` is
zero-allocation atomic; `NewCounter(start)` lets callers resume
from persisted state.

### In-process scope

`Epoch` expresses position in a sequence, NOT identity of a
specific epoch instance across a distributed system. Two
uncoordinated producers can both reach epoch 5 with no
detectable conflict — the type guards position, not identity.
For cluster-wide epoch identity (where each leadership tenure
needs a globally-unique handle that survives process restarts),
use a ULID-shaped identifier from `core/id` and keep `Epoch` for
the within-producer position.

A typical leader composition uses both: a ULID-shaped identifier
names the leadership tenure across the cluster; an
`epoch.Counter` issues sequence positions within the tenure.

### Overflow

`Successor` and `Counter.Next` wrap silently at
`math.MaxUint64`. The wrap is unreachable in practice — a
producer advancing one epoch per nanosecond would take ~584
years to exhaust the range — and is therefore not guarded.
Consumers that genuinely need bounds-checked monotonicity
enforce it at a higher layer.

## Alternatives considered

### A. `Epoch [16]byte` (ULID-shaped)

Conflate epoch position with epoch identity.

**Rejected:** they are different concepts. Epoch position
("which one in the sequence?") wants `uint64` for cheap
arithmetic and atomic operations. Epoch identity ("which
specific instance of the epoch?") wants ULID-shaped 16 bytes
for distributed uniqueness. A leader composition typically uses
both; conflating the types into one forces the simpler use case
to pay the complexity tax of the harder one.

### B. Panic on overflow

Detect overflow and panic with a diagnostic message.

**Rejected:** the wrap is unreachable in practice (~584 years
at one epoch per nanosecond). Defensive code for impossible
scenarios is dead code. Documenting the wrap and trusting the
horizon is the honest shape.

### C. Return `(Epoch, error)` from `Counter.Next`

Force callers to acknowledge potential overflow.

**Rejected:** verbose for a hot-path counter, and the error is
unreachable in practice.

## Drawbacks

- The wrap-on-overflow contract means a hypothetical
  584-year-running producer experiences silent monotonicity
  break. Documented; not a real-world concern.
- `Epoch` doesn't support saturating arithmetic, snapshot
  arithmetic, or vector-clock semantics. Consumers needing
  those compose `Epoch` with their own structure.

## Open questions

None.

## Unresolved / future work

None planned.
