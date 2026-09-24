---
rfc: 0036
title: UTC Readings with an Error Bound
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Draft
created: 2026-09-24
updated: 2026-09-24
discussion: none
supersedes: none
superseded-by: none
produces-adr: tbd
---

# RFC-0036: UTC Readings with an Error Bound

## Summary

We propose a `clock.UTCSource` interface that returns a reading of UTC
together with a bound on its error, and a package `clock/kernel` that
reads both from the Linux kernel's time discipline. The kernel source
calls the kernel at most once per refresh interval, so a read costs
about as much as `time.Now()` instead of a system call. `clock/fake`
gains a settable source for tests. A caller that stamps records with
UTC can then refuse to stamp when the clock's error exceeds what a
regulation or a protocol allows.

## Motivation

- `clock.Clock` returns an `Instant` and a `time.Time`, and states
  nothing about how far either is from UTC. A clock that lost
  synchronisation an hour ago looks the same as one that is accurate
  to microseconds.
- Some rules bound that distance. The EU's RTS 25, Commission Delegated
  Regulation (EU) 2017/574, requires business clocks to remain within
  100 µs or 1 ms of UTC, depending on the trading activity. A system
  that must meet a bound needs to know when it does not.
- The Linux kernel already tracks a bound. Its time discipline keeps a
  maximum error in microseconds and a flag that reports whether the
  clock is unsynchronised, and `adjtimex(2)` returns both. Go's
  `syscall.Adjtimex` calls it. The kernel adds 500 µs to the maximum
  error every second, its `MAXFREQ` of 500,000 ns/s, and sets
  `STA_UNSYNC` once the error passes 16 s (`kernel/time/ntp.c`,
  `include/linux/timex.h`). The synchronisation daemon resets the
  error each time it adjusts the clock.

## Detailed design

### The interface

```go
// UTCReading is one reading of UTC with a bound on its error.
type UTCReading struct {
    // Time is the reading.
    Time time.Time

    // MaxError bounds the distance between Time and UTC at the
    // moment of the reading: UTC lies within [Time-MaxError,
    // Time+MaxError]. It is a bound only when Synced is true.
    MaxError time.Duration

    // Synced reports whether the source considers the clock
    // synchronised to UTC.
    Synced bool
}

// Within reports whether the reading is synchronised and its error
// bound is at most limit.
func (r UTCReading) Within(limit time.Duration) bool

// Earliest returns Time minus MaxError, the earliest UTC the reading
// allows.
func (r UTCReading) Earliest() time.Time

// Latest returns Time plus MaxError, the latest UTC the reading
// allows.
func (r UTCReading) Latest() time.Time

// UTCSource reads UTC with a bound on its error.
//
// # Concurrency
//
// Implementations must be safe for concurrent use.
type UTCSource interface {
    // ReadUTC returns a reading. An error means the source could not
    // be read at all, not that the clock is unsynchronised: that is a
    // reading with Synced false.
    ReadUTC() (UTCReading, error)
}
```

`UTCSource` is separate from `clock.Clock`. A caller that needs the
bound asks for a `UTCSource`, and every existing `Clock` keeps working
unchanged.

### The kernel source

```go
// Package kernel reads UTC and its error bound from the operating
// system kernel's time discipline.
package kernel

// New returns a Source, a UTCSource over the kernel's clock that calls
// adjtimex(2) at most once per refresh.
//
// Time is time.Now(). MaxError is the kernel's maxerror at the last
// call, plus 500 µs for every second elapsed since that call on the
// monotonic clock, which is the rate at which the kernel itself grows
// maxerror. Synced is false when the last call reported STA_UNSYNC or
// returned TIME_ERROR, and when MaxError passes 16 s, where the kernel
// also marks the clock unsynchronised.
//
// A change the daemon makes to maxerror or to the status becomes
// visible within one refresh interval. A reader that finds the last
// call older than refresh makes the next call and publishes the
// result. Other readers use the previous result meanwhile, so at most
// one caller waits on the kernel at a time.
//
// On systems other than Linux, ReadUTC returns an error wrapping
// errors.ErrUnsupported, which classifies as errs.Unsupported.
//
// Returns ErrRefresh for a refresh that is not positive.
//
// # Allocation contract
//
// ReadUTC is zero-alloc. A refresh publishes one snapshot.
func New(refresh time.Duration) (*Source, error)
```

`New` returns the concrete `*Source`, which satisfies `clock.UTCSource`.
It makes the first kernel call before it returns. A failed call does
not fail `New`: `ReadUTC` returns the error until a later call
succeeds.

The package calls one system call and opens no file or socket, so
ADR-0008 admits it. A test replaces the system call through an
unexported function, so the unsynchronised and failure paths are
covered without changing the machine's clock.

### Cost

Measured with Go 1.27.1 on an AMD Ryzen 9 9950X3D, three runs of
200,000 calls:

| Operation | Cost |
|---|---|
| `syscall.Adjtimex` | 551-565 ns |
| `time.Now` | 32-35 ns |
| `kernel.Source.ReadUTC`, 100 ms refresh | 38.9-39.7 ns, 0 allocations |

A source that called the kernel on every read would cost a caller that
stamps a million records a second more than half a core. With a
refresh of 100 ms, the kernel call happens ten times a second, and a
read costs `time.Now()` plus an atomic load.

### The daemon behind the bound

The kernel reports what the synchronisation daemon told it, and
daemons differ:

- chrony sets maxerror to half the root delay plus the root
  dispersion, a hard bound that includes the error of its time
  sources (`reference.c`, `update_sync_status`).
- systemd-timesyncd adjusts the clock with `ADJ_MAXERROR` and a
  maxerror of zero (`timesyncd-manager.c`). After each adjustment the
  kernel reports 500 µs per second since that adjustment, which
  excludes the error of the time source.

The kernel source therefore reports a bound only under chrony or
ntpd. It cannot detect which daemon runs, so a deployment that relies
on the bound states its daemon and checks it.

On the machine that measured the costs, chrony synchronised to public
NTP servers reports a maxerror of 289,958 µs and an estimated error of
816 µs, so the bound is only as tight as the time sources. A
caller with a 1 ms limit on that machine refuses to stamp, which is
correct: nothing proves the clock is within 1 ms. A tight bound needs
a close reference, such as a precision time protocol hardware clock.
AWS reports a ClockBound error bound under 40 µs with that
connection.

### The fake source

`clock/fake.Clock` implements `UTCSource`. Its reading's `Time` is the
fake clock's time. `SetUTCError(maxError time.Duration, synced bool)`
sets the other two fields, and a new fake clock reports a synchronised
reading with no error.

### Example

A caller refuses to stamp a record when the clock is outside its
bound:

```go
utc, err := kernel.New(100 * time.Millisecond)
if err != nil {
    return err
}

// For each record:
reading, err := utc.ReadUTC()
if err != nil {
    return err
}
if !reading.Within(time.Millisecond) {
    return ErrClockUnsynced
}
record.Stamp = reading.Time
```

### Errors

```go
// ErrRefresh reports a refresh interval that is not positive. It
// classifies as errs.Invalid.
var ErrRefresh = errs.WithClass(errors.New("kernel: refresh must be positive"), errs.Invalid)
```

### Tests

- `Within`, `Earliest` and `Latest` follow their definitions at the
  boundaries.
- `kernel` maps a status with `STA_UNSYNC`, a `TIME_ERROR` return and
  a system call error to their readings and errors, through the
  replaced system call.
- Between refreshes, `kernel` reports the last maxerror plus 500 µs
  per elapsed second, and it calls the replaced system call once per
  refresh interval under concurrent readers.
- A reading at 16 s stays synchronised, and a reading past it does
  not, as in the kernel.
- Off Linux, `ReadUTC` returns an error that classifies as
  `errs.Unsupported`.
- A benchmark measures `ReadUTC`, and `TestZeroAlloc` covers it.
- On Linux, one test calls the real system call and checks that the
  reading's time is within a second of `time.Now()`.
- `fake.Clock` returns what `SetUTCError` set.

## Alternatives considered

### A. Add the bound to `clock.Clock`

`Clock` would gain a method that returns the error bound.

**Why not:** every implementation of `Clock` would have to report a
bound, including the hybrid logical clock, which has no notion of one.
A separate interface lets the sources that know the bound report it.

### B. A client for NTP or PTP

Core would query time servers and compute the bound itself.

**Why not:** that is network code, which ADR-0008 keeps out of core.
The kernel already has the result of the synchronisation daemon's
work, and a system that runs AWS ClockBound or another daemon can
implement `UTCSource` over it in its own module.

### C. An interval type, as TrueTime has

Google's Spanner reads time as an interval [earliest, latest].

**Why not:** the reading contains the same information, and `Earliest`
and `Latest` return the interval. A time with a bound also passes
straight to code that takes a `time.Time`.

### D. Call the kernel on every read

Every `ReadUTC` would call `adjtimex(2)`, so the bound would always be
current.

**Why not:** the call costs 551 to 565 ns against 32 to 35 ns for
`time.Now()`, measured on the same machine. Between the daemon's
updates the kernel grows maxerror at a fixed rate that the source can
compute, so a fresh call adds information only when the daemon has
changed something. A refresh interval bounds how late the source sees
that change.

## Drawbacks

- The kernel source reports what the synchronisation daemon told the
  kernel. Under systemd-timesyncd that excludes the error of the time
  source, and a misconfigured daemon can report a small bound for a
  clock that is wrong. The source cannot detect either.
- A change the daemon makes, such as a larger error after it loses its
  sources, is visible to readers up to one refresh interval late.
- The kernel source works on Linux only. Elsewhere a caller supplies
  its own `UTCSource` or runs without the check.
- A caller has to choose the limit it passes to `Within` and the
  refresh interval, and core cannot choose either.

## Open questions

None.

## Unresolved / future work

- A `UTCSource` over AWS ClockBound or another daemon's shared memory,
  in a consumer module.
- The kernel's estimated error, `esterror`, if a caller needs an
  estimate as well as a bound.

## References

- Linux `adjtimex(2)`.
- Linux `include/linux/timex.h`, for `MAXFREQ`, `MAXPHASE` and
  `NTP_PHASE_LIMIT`.
- Linux `kernel/time/ntp.c`, `second_overflow`.
- chrony, `reference.c`, `update_sync_status`,
  <https://github.com/mlichvar/chrony>.
- systemd, `src/timesync/timesyncd-manager.c`,
  <https://github.com/systemd/systemd>.
- AWS, "It's About Time: Microsecond-Accurate Clocks on Amazon EC2
  Instances",
  <https://aws.amazon.com/blogs/compute/its-about-time-microsecond-accurate-clocks-on-amazon-ec2-instances/>.
- Go: `syscall.Adjtimex` and `syscall.Timex` on Linux.
- Commission Delegated Regulation (EU) 2017/574 (RTS 25),
  <https://eur-lex.europa.eu/legal-content/EN/TXT/?uri=CELEX:32017R0574>.
- AWS ClockBound, <https://github.com/aws/clock-bound>.
- J. C. Corbett et al., "Spanner: Google's Globally-Distributed
  Database", OSDI 2012.
- RFC-0001, the clock seam.
- ADR-0008, core defines contracts that describe IO.
