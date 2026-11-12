---
adr: 0091
title: The Last Member of a Group Runs the Last Section of a Close
status: Accepted
date: 2026-11-12
supersedes: none
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0091: The Last Member of a Group Runs the Last Section of a Close

## Status

Accepted

## Context

A component of a storage engine ends its Close after its goroutines return,
such as the leader of a write-ahead log and the jobs of a scheduler. Such a
goroutine can be in a call of a disk that does not return. The engine fails
the disk by the end of its context, and the Close of the engine must then
return without a wait for that goroutine.

A `sync.WaitGroup` keeps its waiter until the last goroutine returns. The
end of the context of the disk cannot end that wait. `Lock.Finally` passes
the last section of a Close to the caller that has the lock, and these
goroutines take no lock.

A component that counts its goroutines itself repeats one race in each Close.
The last goroutine can return after the Close counted it and before the Close
marked its section for that goroutine. The section then has no caller.

## Decision

We will add `resilience.Group` to count the goroutines of a component,
because the last member of a component is the one caller that knows when
every member has returned. Once the owner has ended, the Group passes the
last section of a Close to that member.

```go
func NewGroup(owner context.Context) *Group
func (g *Group) Go(f func()) bool
func (g *Group) Add() bool
func (g *Group) Done()
func (g *Group) Finally(f func()) bool
```

- One atomic word counts the members. `Finally` sets a bit of the word, and
  `Go` and `Add` refuse new members once the bit is set.
- Before the owner ends, `Finally` waits for the members and runs f. The last
  member that ends after `Finally` started closes a channel, which wakes the
  waiting `Finally`.
- Once the owner has ended, `Finally` does not wait. It marks the section
  with a compare-and-swap and counts the members again. It runs the section
  when every member has returned. Otherwise the last member runs it in its
  `Done`.
- `Finally` counts the members after its mark, and `Done` swaps the mark after
  its decrement. One of the two finds the step of the other in the
  sequentially consistent order of a data-race-free Go program, and the swap
  gives the section to one caller.
- A Group marks one section at a time, as a `Lock` does.
- The zero Group refuses members, and its `Finally` runs f at once.

## Alternatives Considered

### A sync.WaitGroup with a wait in a goroutine

The Close would wait for the `sync.WaitGroup` on a goroutine of its own and
return at the end of the owner.

Rejected. The goroutine of the wait remains until the call of the failed disk
returns, and the section then needs a second hand-off from that goroutine.

### A Lock for each member

Each goroutine would take a `Lock` while it runs, and the Close would use
`Lock.Finally`.

Rejected. A Lock lets one caller at a time in, and a component runs many
goroutines at once.

## Consequences

**Positive:**

- A Close runs its last section once, after the last member, and returns
  without a wait for a member in a call of a failed disk.
- `Add` and `Done` do not allocate, so a call that joins the group on a hot
  path keeps its allocation contract.

**Negative:**

- The section runs inside the `Done` of the last member, on its goroutine. A
  slow section delays that member.
- A second `Finally` while a section is marked does not run its f.
- `Finally` allocates its mark once the owner has ended.

**Neutral:**

- A Group refuses members once `Finally` has started, so a Close that runs
  `Finally` twice runs the second f at once.

## References

- Issue 145, <https://github.com/thesm-os/core/issues/145>.
- ADR-0087, a lock hands off the last section of a Close: the mark and the
  check after it.
- Go, `sync.WaitGroup.Go` and `Wait`, <https://pkg.go.dev/sync#WaitGroup>.
- Go 1.27.4, `src/internal/poll/fd_mutex.go:76-120`: the last reference of a
  descriptor closes it.
