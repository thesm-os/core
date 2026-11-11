---
adr: 0087
title: A Lock Hands Off the Last Section of a Close
status: Accepted
date: 2026-11-11
supersedes: none
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0087: A Lock Hands Off the Last Section of a Close

## Status

Accepted

## Context

`resilience.Lock` ends every wait at the end of its owner, and refuses every
call to `Lock` once the owner has ended. A storage engine fails a disk by the
end of its context. A call of the disk can stay in the kernel after the
failure.

A component that takes such a lock also ends itself under the lock in its
Close. The Close seals a segment, closes its files or releases its memtable.
The Close of the engine must return without a wait for a call of a failed
disk, and `Lock` refuses the Close once the owner has ended:

- A Close that runs its last section without the lock races with the call
  when the call returns from the kernel.
- A Close that skips its last section leaves the memory and the files of the
  component taken after the call returns.

The last section must run once under the lock after the call frees the lock.
A component can keep the section in a flag of its own that each of its Unlock
calls checks. A call that checks the flag before the Close sets it and frees
the lock after the Close found it taken loses the section.

## Decision

We will add `Lock.Finally` to run the last section of a Close under the lock.
Once the owner has ended, `Finally` hands the section to the caller that has
the lock because that caller can run it under the lock when its call
returns.

```go
func (l *Lock) Finally(f func()) bool
```

- While the owner lives, `Finally` takes the lock, and waits while another
  caller has it. It runs f and frees the lock.
- Once the owner has ended, `Finally` does not wait. It marks the section with
  a compare-and-swap on an atomic pointer. It then takes the lock when the
  lock is free, and frees it.
- `Unlock` reads the mark with one atomic load after it frees the lock. When
  a section is marked, it takes the lock again and swaps the mark with nil.
  It runs the section and frees the lock.
- `Finally` tries the lock after its mark, and `Unlock` reads the mark after
  its free. In the sequentially consistent order that a data-race-free Go
  program has, at least one of the two callers finds the step of the other.
  The swap then gives the section to one caller.
- `Finally` reports whether the section ran before it returned. The caller
  that runs the section sets a flag of the mark before it frees the lock.
- A Lock marks one section at a time. A `Finally` that finds another section
  marked does not run f, and returns false.

## Alternatives Considered

### A flag of the component

Each component would keep its section in a flag of its own that each of its
Unlock calls checks.

Rejected. Each Close repeats the race between the check of an Unlock and the
mark of the Close. Every Unlock of the component needs the check too.

### A wait for the lock after the end of the owner

`Finally` would wait for the lock as `Lock` does while the owner lives.

Rejected. A call of a failed disk can stay in the kernel without end, and the
Close of the engine would wait with it.

### A check of the mark before the free

`Unlock` would run a marked section before it frees the lock, and would not
read the mark after the free.

Rejected. A caller can read the mark before `Finally` marks the section. That
caller can then free the lock after `Finally` found it taken, and the section
has no caller.

### A queue of sections

The Lock would keep every section that a `Finally` marks.

Rejected. A component runs its last section once, and one mark keeps the
state of the Lock to one atomic pointer. A second `Finally` learns from its
result that its f did not run.

## Consequences

**Positive:**

- A Close runs its last section under the lock once, and returns without a
  wait for a call of a failed disk.
- `Lock`, `TryLock` and `Unlock` do not allocate. The load of the mark adds 0
  to 1.25% to a lock and an unlock, measured in four alternating rounds of
  three runs per build on an AMD Ryzen 9 9950X3D with Go 1.27.4.

**Negative:**

- The section runs inside the Unlock of the call that returns from the
  kernel, on its goroutine. A slow section delays that caller, and a panic of
  the section leaves the lock taken.
- `Unlock` frees the lock before it takes the lock again for the section. A
  caller that takes the lock between the free and the second take runs its
  own section first. That caller runs the marked section in its own Unlock.
- `Finally` allocates its mark once the owner has ended, and f escapes to the
  heap.
- A second `Finally` while a section is marked does not run its f.
- No test can order a swap that returns nil after another caller ran the
  section. The removal of the check of the owner before the wait of `Finally`
  leaves the same results, because the select after it makes the same choice
  at random. The mutants of both lines have annotations.

**Neutral:**

- `Finally` takes no context. While the owner lives, it waits for the caller
  that has the lock.

## References

- Issue 142, <https://github.com/thesm-os/core/issues/142>.
- ADR-0086, a lock ends its waits at the end of a context: `resilience.Lock`.
- Go 1.27.4, `src/internal/poll/fd_mutex.go:76-120` and `:219-227`:
  `increfAndClose` marks a descriptor closed, and the `decref` of the last
  reference closes it.
- Go 1.27.4, `src/internal/poll/fd_unix.go:91-116`: `Close` does not wait for
  the I/O of a descriptor in blocking mode, because that I/O can block.
- The Go memory model, DRF-SC: data-race-free programs execute in a
  sequentially consistent manner, <https://go.dev/ref/mem>.
