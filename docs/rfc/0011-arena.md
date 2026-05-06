---
rfc: 0011
title: Arena — Bump-Allocator for Hot-Path Variable-Length Output
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-05-06
updated: 2026-05-06
discussion: none
supersedes: none
superseded-by: none
produces-adr: none
---

# RFC-0011: Arena — Bump-Allocator for Hot-Path Variable-Length Output

## Summary

A contiguous byte buffer (`Arena`) for batching
variable-length binary output on hot paths. Callers append
into a single arena and receive sub-slices into the backing
buffer; at the ownership-transfer boundary they bulk-copy to
caller-owned memory. With pool reuse the backing buffer is
preserved across requests — steady-state work allocates
nothing for the arena itself.

## Motivation

On a request that produces many small variable-length
outputs (audit chain entries, encoded patches, batched
telemetry payloads), N independent allocations cause heap
fragmentation and GC mark-phase latency. An arena replaces
those with one contiguous allocation per request.

The load-bearing property is **stable references**: a
sub-slice returned by `Append` aliases the arena's backing
buffer and remains readable across subsequent appends until
`Reset`. A `bytes.Buffer` cannot guarantee this — its
underlying buffer may reallocate on growth, invalidating
previously-held slices. The arena's contract — stable
within a Reset cycle, scoped via a lifecycle epoch — is
exactly what consumers want when they reach for a
"bump allocator."

## Detailed design

```go
type Arena struct{ ... }

func New() *Arena
func NewWithCapacity(initialCap int) *Arena

// Append-side
func (*Arena) Append(data []byte) []byte
func (*Arena) Alloc(n int) []byte

// Inspection
func (*Arena) Len() int
func (*Arena) Cap() int
func (*Arena) Bytes() []byte

// Region capture across multiple appends
type Marker struct{ /* opaque */ }
func (*Arena) Mark() Marker
func (*Arena) SliceSince(m Marker) []byte

// Boundary transfer (caller-owned output)
func (*Arena) CopyOut() []byte
func (*Arena) CopyOutTo(dst []byte) []byte

// Slice rebasing — free functions, no Arena receiver
func RebaseSlices(slices [][]byte) []byte
func RebaseSlicesTo(dst []byte, slices [][]byte) []byte

// Lifecycle / pool support
func (*Arena) Reset()
func (*Arena) CapExceeds(maxCap int) bool
func (*Arena) Shrink()
```

### Append vs Alloc

`Append(data)` copies caller-owned bytes into the arena. Use
when the data already exists somewhere else.

`Alloc(n)` returns a zeroed n-byte sub-slice the caller can
fill in directly. Use when the data is produced in-place
(e.g. a decoder writes into the buffer). Avoids the
caller-owned-buffer-then-copy round trip.

Both return three-index-capped sub-slices (`buf[start:end:end]`):
downstream `append` cannot extend a returned slice into a
neighbouring item's region.

### Backing-array lifetime — the big hazard

Sub-slices returned by `Append` / `Alloc` / `Bytes` /
`SliceSince` alias the arena's **current** backing array.
When an append exceeds capacity, the arena reallocates:
previously-returned sub-slices reference the orphaned
backing array (still readable, kept alive by the slice
header) but no longer alias subsequent calls' returns or
`Bytes()`.

```go
a := arena.NewWithCapacity(8)
first := a.Append([]byte("a"))     // first → array A
a.Append(make([]byte, 1024))        // exceeds cap, realloc → array B
view := a.Bytes()                   // view → array B
// first and view DO NOT alias the same memory.
```

The doc spells this out explicitly because the failure mode
is silent — no panic, no error, just two slices that mostly
look right and quietly disagree on byte values.

To keep the alias-stable contract, pre-size the arena via
`NewWithCapacity` above the highest expected total. Once
capacity covers the entire Reset cycle, no realloc occurs
and every returned sub-slice aliases the same array.

### Marker is epoch-tagged

`Mark()` returns an opaque `Marker` carrying both the write
position AND the arena's current `epoch.Epoch`. `Reset()`
and `Shrink()` advance the epoch via `epoch.Successor`,
invalidating every Marker captured before the lifecycle
boundary.

`SliceSince(m)` checks the epoch first: a stale Marker
yields nil, not a silently-wrong region. This closes the
hazard where a consumer holds a Marker across `Reset` and
calls `SliceSince` afterward — without epoch tagging,
SliceSince would return whatever the new lifecycle's bytes
happen to be.

### CapExceeds, not ShouldShrink

The function reports an observation ("capacity exceeds the
threshold"), not a recommendation. A pool wrapper consults
it after Reset to discard arenas that bloated during
anomalous load; the consumer decides whether to shrink, log,
or escalate.

### RebaseSlices as free functions

`RebaseSlices` and `RebaseSlicesTo` consolidate sub-slices
from one or more arenas into a single caller-owned
allocation. Each rebased entry uses three-index slicing to
prevent cross-item aliasing.

These are package-level functions, not Arena methods,
because they don't depend on Arena state — they read source
slice headers, write into a fresh allocation, then rewrite
the source headers in place. Input slices may originate in
any arena (or none), may overlap in source memory, and may
be header-equal across indices: the read-then-rewrite-per-
index pattern is independent of source aliasing.

### Pool integration

`(*Arena).Reset` satisfies `pool.Resettable`, so an
arena-of-arenas pool is one line:

```go
var arenaPool = pool.NewResetPool(arena.New)
```

`CapExceeds(maxCap)` lets the consumer wrap `pool.Put` with
a "drop oversized" decision; `Shrink()` releases the
backing buffer when an outlier request pushed capacity
beyond a sustainable threshold.

## Alternatives considered

### A. Fixed-capacity, panic-on-overflow

Caller sizes the arena up front; appends past capacity
panic.

**Rejected:** consumers don't always know the upper bound
in advance, and the panic-on-overflow contract conflicts
with the no-panic discipline. Growable with stable-within-
cycle references is more consumer-friendly and the
lifetime hazard is mitigated by docs and pre-sizing
guidance.

### B. Chunked arena (linked fixed-size chunks)

Stable references AND growable, by allocating new chunks on
overflow rather than reallocating the existing buffer.

**Deferred:** more complex implementation; the "pre-size if
you need stable references across grow" pattern covers most
consumer needs. Add chunked variant later if a consumer
demonstrates a workload that genuinely needs both
properties.

### C. Mark as int (no epoch tagging)

Original sketch and the reference implementations in
foundation/arena.

**Rejected:** silently corrupts when a Marker survives a
Reset. The fix — tagging Marker with the lifecycle epoch
and checking it in SliceSince — costs one uint64 in the
Marker and one comparison in SliceSince. Cheap;
silent-corruption avoidance is worth the surface change.

### D. CopyOutSlices as Arena method

`(*Arena) CopyOutSlices(slices [][]byte) []byte`.

**Rejected:** the receiver is unused — the function reads
input slice headers and writes into a fresh allocation.
Lifting to a free function (`arena.RebaseSlices`) is
honest about the dependency surface and avoids the smell of
a method that ignores its receiver.

## Drawbacks

- The "stable until grow OR Reset" contract is subtle. Doc
  spells it out, but consumers that don't read the lifetime
  doc may hold a slice across an unexpected realloc and
  observe silent divergence. Pre-sizing eliminates the
  hazard.
- Marker carries 16 bytes (int + uint64). Negligible vs the
  silent-corruption avoidance.
- `growCap` doubles via `have*2`, which overflows on arenas
  approaching `math.MaxInt/2`. The arena would already
  exceed addressable memory — documented as out of scope.
- `Reset` only truncates the slice; it does not zero the
  backing bytes. Tenants reading past Len cannot see prior
  data because Len is the boundary, but a buggy consumer
  reading via the orphaned-array slice header could observe
  bytes that "look like" current state. `Alloc` zeroes the
  reused region defensively.

## Open questions

None.

## Unresolved / future work

- Chunked arena variant (alternative B) when a consumer
  demonstrates the need for stable references AND
  unbounded growth.
- Specialized arenas for common patterns (string interning
  arena, struct slab) when cross-consumer usage emerges.
