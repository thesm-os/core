---
adr: 0094
title: A Cache Loads a Missing Entry Once for the Callers That Miss It
status: Accepted
date: 2026-11-26
supersedes: none
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0094: A Cache Loads a Missing Entry Once for the Callers That Miss It

## Status

Accepted

## Context

A storage engine keeps the centroid set of each vector volume in a
`cache.Cache`. Each search that misses the set reads every cluster of the
volume to build it. A probe made eight callers of one empty cache wait until
all eight had missed, over a volume of 1,024 clusters of 768 components. The
callers built sets of 25,825,280 bytes together in 331 ms at 4 Go
processors, and the cache kept one of them.

On a miss of `Get`, a caller builds the value and stores it with `Set`, so
each caller that misses before the first `Set` builds the value too.
`golang.org/x/sync/singleflight` runs one call for the callers of a key, but
it runs the call under the context of its first caller. A cancel of that
caller then fails the call for every other caller of the key.

## Decision

We will add `Cache.GetOrLoad` to run one load for the callers that miss a key
together. The cache serializes the writes of its keys under its lock, so a
load can tell the write of its key that it overlaps.

```go
func (c *Cache[K, V]) GetOrLoad(
	ctx context.Context, k K, load func(ctx context.Context, k K) (V, time.Time, error),
) (V, error)
```

- A hit returns the value as `Get` does, without a lock and without an
  allocation.
- The first caller that misses the key starts the load on a goroutine of its
  own. Each later caller of the key waits for the result of that load.
- The context of the load has the values of the context of its first caller.
  The context ends once the load returns, or once every waiting caller has
  stopped waiting.
- A caller stops waiting at the end of its own context, and returns the
  cause of its context.
- A load that returns a value stores it with its expiry time as `Set` does.
  A load that returns an error does not store a value, and the next caller
  of the key starts a new load.
- A `Set`, `SetPinned` or `Delete` of the key while the load runs takes the
  load out of the map of loads. The load then does not store its value, and
  its waiting callers still receive the value.
- A nil load on a miss returns `ErrLoad` of the class Invalid. So does a
  load that succeeds with an expiry time in a cache without a Clock. A load
  that fails returns its own error, with or without an expiry time.

## Alternatives Considered

### A wrapper over golang.org/x/sync/singleflight

The caller would wrap the cache and run each load through
`singleflight.Group.Do`.

Rejected. `Do` runs the load under the context of its first caller, so the
end of that context fails the load for every other caller. The load also
cannot tell a write of the key that it overlaps.

### The load on the goroutine of the first caller

The first caller would call the load itself.

Rejected. That caller cannot stop waiting at the end of its context, because
the load runs on its stack.

### The value of a load that a write overlaps

The load would store its value after a `Set` or a `Delete` of its key.

Rejected. The load reads its source before the write, so its value can be
older than the value of the `Set`, and it would bring a deleted key back.

### A new load after a discarded value

The waiting callers would read the key again after a write discarded the
value of their load, and load it again for a miss. Otter v2 does this in its
`Get`.

Rejected. A stream of writes of one key keeps the callers in new loads until
their contexts end. The callers of a discarded load overlap the write, so the
value of their load is a value that the key had during their calls.

### A load in the Config

`Config` would take one load for every key, and `GetOrLoad` would take only a
key.

Rejected. A call site passes a load that knows the source of its keys, and a
load that the caller makes once costs no allocation on a hit either.

## Consequences

**Positive:**

- The callers that miss one key together run one load, and share its value.
- A hit of `GetOrLoad` takes 9.4 ns and allocates nothing on one core.
- A miss that starts a load allocates 7 objects of 464 bytes together and
  takes 1.1 µs, the load included: the state of the load, its channel, the
  closure that starts its goroutine, two contexts, the cancel function of its
  context and the entry.

**Negative:**

- A load that panics crashes the process from its goroutine.
- A load that ignores its context runs until it returns.
- The callers of a discarded load receive a value that the cache does not
  keep.
- Every caller of one key must pass a load that computes the same value,
  because a caller can receive the value of a load that another caller
  passed.

**Neutral:**

- `Set`, `SetPinned` and `Delete` delete their key from the map of loads
  under the lock. Go's delete of a key from an empty map returns at once.

The measurements ran on an AMD Ryzen 9 9950X3D with Go 1.27.4.

## References

- Issue 162, <https://github.com/thesm-os/core/issues/162>.
- `golang.org/x/sync/singleflight`, `Group.Do`,
  <https://pkg.go.dev/golang.org/x/sync/singleflight#Group.Do>.
- resenje.org/singleflight v0.4.3, `Group.Do`: a load whose context ends when
  no caller waits, <https://pkg.go.dev/resenje.org/singleflight#Group.Do>.
- Otter v2.4.0, `Cache.Get`: a write of a key during its load discards the
  loaded value, <https://pkg.go.dev/github.com/maypok86/otter/v2#Cache.Get>.
