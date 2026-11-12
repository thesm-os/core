---
adr: 0089
title: A Table of Token Buckets Keeps One Word for Each Bucket
status: Accepted
date: 2026-11-12
supersedes: none
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0089: A Table of Token Buckets Keeps One Word for Each Bucket

## Status

Accepted

## Context

A service bounds the calls of its principals with tables of token buckets
that share one rate and one burst. A call takes a unit from the bucket of
the hash of its principal, so the memory of the table does not grow with the
principals. No call of such a table waits for units.

A `resilience.Limiter` takes 144 bytes. It keeps a clock, a mutex, the head
and two queues of waiting calls, and the times of its bucket. A test against
core v0.44.0 made two tables of 65,536 Limiters each. It measured 19,877,288
bytes of heap in 131,051 objects.

The Limiter keeps the time of its bucket as whole nanoseconds and a remainder
in Rate-ths of a nanosecond, so its arithmetic is exact. That time needs two
words.

## Decision

We will add the table of token buckets `resilience.Buckets` with one atomic
word for each bucket, because a call that does not wait needs only the time
of its bucket. One word fits a compare-and-swap of Go.

```go
type BucketsConfig struct {
	Clock clock.Clock
	Rate  int64
	Burst int64
	Size  int
}

func NewBuckets(cfg BucketsConfig) (*Buckets, error)
func (b *Buckets) AllowN(i int, n int64) bool
func (b *Buckets) ReturnN(i int, n int64)
```

- Each bucket keeps one time of the generic cell rate algorithm in whole
  nanoseconds of the Elapsed of the Clock. A new bucket has the time zero, so
  every bucket of a new table is full.
- A unit costs 10^9/Rate nanoseconds rounded up to a whole nanosecond, so a
  bucket admits at most Rate units per second. A Rate that does not divide
  10^9 admits fewer units per second, and the shortfall is less than
  Rate/10^9 of the units.
- `NewBuckets` refuses a Rate above 10^9 with `ErrConfig`. A Rate of 10^9
  admits one unit per nanosecond.
- `AllowN` and `ReturnN` move the time of a bucket with a compare-and-swap
  and retry while another call changes it.
- `ReturnN` moves the time back by the cost of its units. It does not move the
  time before the current time, so a bucket keeps at most Burst units.
- An index outside the table refuses `AllowN` and gives back nothing in
  `ReturnN`.

## Alternatives Considered

### A Limiter for each bucket

Rejected. Each Limiter keeps the queues and the mutex of the calls that wait,
and no call of such a table waits. The Limiters of two tables took 19.9 MB in
131,051 objects that every collection marks.

### The exact time of the Limiter

Each bucket would store whole nanoseconds and a remainder, as the Limiter
does.

Rejected. Whole nanoseconds and a remainder do not fit one compare-and-swap,
so a call would lock the bucket or a stripe of buckets. One word in Rate-ths
of a nanosecond overflows. At a Rate of 7,919 an int64 counts about 13 days
in Rate-ths of a nanosecond.

### A cache line for each bucket

Each bucket would take 64 bytes, so the calls of two buckets do not contend
for one line.

Rejected. The table would take eight times the memory, and the buckets of the
hashes of principals spread the calls over the table.

## Consequences

**Positive:**

- A bucket takes eight bytes, so a table of 65,536 buckets takes 512 KiB in
  one object.
- `AllowN` and `ReturnN` do not allocate, lock or wait. One `AllowN` and one
  `ReturnN` took 28 ns together on a fake clock. The measurement ran on an
  AMD Ryzen 9 9950X3D with Go 1.27.4.

**Negative:**

- A Rate that does not divide 10^9 admits fewer units per second than its
  value states. At a Rate of 3 a bucket gains its third unit 2 ns after a
  second.
- A Rate above 10^9 is not available.
- The calls of neighbouring buckets contend for a cache line.
- A call of a Buckets does not wait, and the table has no priorities.

**Neutral:**

- `ReturnN` gives units back to a bucket as `Limiter.ReturnN` gives them back
  to a Limiter.

## References

- Issue 151, <https://github.com/thesm-os/core/issues/151>.
- RFC-0050, the rate limiter: the generic cell rate algorithm of the
  `Limiter`.
