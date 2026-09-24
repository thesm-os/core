---
rfc: 0038
title: Conformance for Durable Adapters and Decorators
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Draft
created: 2026-09-24
updated: 2026-09-24
discussion: none
supersedes: none
superseded-by: none
produces-adr: tbd
---

# RFC-0038: Conformance for Durable Adapters and Decorators

## Summary

We propose three additions to core's conformance suites and seams:

- Options for `blobtest.AssertStore` and `castest.AssertStore` that let
  a durable adapter prove its laws across a restart and across a crash
  in the middle of a write.
- A check that each suite rejects a deliberately broken store for
  every law it states, so a law the suite cannot detect breaking fails
  core's own gate.
- A convention for optional capabilities behind decorators: a
  decorator implements `Unwrap`, and each seam provides `As`
  functions that find a capability through a chain of decorators.

## Motivation

### The suites test only stores that never restart

`blobtest.AssertStore` and `castest.AssertStore` take a function that
returns a fresh store, and run every case against it. For the
in-memory references that is the whole contract. A store over a disk
or a remote service has laws that only a restart can test:

- A `Put` that returned is still there, with the same version and
  bytes, after the process restarts.
- A `Put` interrupted by a crash leaves the object absent or whole
  after the restart, never partial.

Neither suite can express a restart or a crash, so an adapter that
keeps its index only in memory passes both.

### Nothing checks that a suite can fail

A suite that passes every store it is given proves nothing about the
laws it does not check. Mutation testing checks core's own code. It
does not check that `blobtest` would reject a store that breaks atomic
visibility.

### Decorators hide optional capabilities

Core's seams discover optional capabilities by type assertion:
`cas.PutStream` asserts for `cas.Streamer`, and `crypto.GenerateKey`
asserts for `crypto.KeyGenerator`. A decorator that adds tracing or
metrics implements the mandatory interface and hides the capability of
the value it wraps. The caller silently takes the fallback path: a
buffered copy instead of a stream, or a key generated outside the
custodian instead of inside it.

## Detailed design

### Suite options

```go
// Option configures a conformance run. The same options exist in
// blobtest and castest.
type Option func(*config)

// WithReopen gives the suite a way to open the storage behind s again,
// as after a process restart. The suite writes through s, reopens,
// and requires every write that returned to be readable with the
// same version and bytes, every refused write to be absent, and no
// version issued before the reopen to be issued again for its key.
func WithReopen(reopen func(t *testing.T, s blob.Store) blob.Store) Option

// WithCrash gives the suite a way to crash the storage behind s while
// write runs, at a point the adapter chooses, and to open it again.
// crash calls write once and returns after write returns. The suite
// requires each object write was putting to be as it was before write
// or whole in the reopened store, and whole when its Put returned
// without error.
func WithCrash(crash func(t *testing.T, s blob.Store, write func()) blob.Store) Option
```

`AssertStore` gains a variadic `...Option` parameter, so every existing
call keeps compiling and keeps its meaning. A durable adapter passes
both options, built over whatever fault injection its storage allows.
The in-memory references pass both as well: a reopen returns the same
store, and a crash comes after `write` returns. That runs the cases in
core's own gate, where the broken stores of the next section fail them.

The crash case requires the whole object for every `Put` that returned
without error. A case that accepted any absent or whole object would
pass an adapter that acknowledges a `Put` before its data is durable,
which is the bug `CrashClone` exists to find.

Storage engines already test this way. Pebble's `vfs.NewCrashableMem`
returns an in-memory filesystem whose `CrashClone` keeps the data a
`Sync` made durable and drops, or keeps at random, the data that was
never synced. Its `errorfs` package injects a failure at a chosen
operation. Pillai et al. showed at OSDI 2014 that applications built on
real filesystems lose data at crash points like these. A `WithCrash`
built over such a filesystem crashes the adapter at every sync
boundary of one `Put` in turn.

`castest` gains one more option, `WithZeroAllocGet`, which asserts that
`Get` into a buffer with capacity allocates nothing, for adapters that
make that claim. It measures with `testing.AllocsPerRun`, which panics
while a parallel test runs, so the test that passes it does not call
`t.Parallel`. The zero-allocation test of `cas/memory` moves into the
suite as this option.

### Checking the suites

Each suite package gains a broken store for every top-level case of
its suite. The broken stores live in the package's external test file,
`blobtest_test.go` or `castest_test.go`, so no adapter imports them.
Each one wraps the memory store and replaces one method, for example:

- A `Put` that commits the bytes it read before its reader failed.
- A `Put` that ignores `IfMatch` or `IfNoneMatch`.
- A cursor that drops the first object of every page after the first.
- A `Get` that returns an unclassified error for an absent key.
- For `cas`, a `Put` that keeps bytes that do not hash to their
  address.
- A reopen or a crash that returns an empty store.

The suites take a `*testing.T`, and `testing.TB` has unexported
methods. No test double can implement it to observe a failure in
process. The check runs the suite in a child process, as the tests of
`task` panics do, and reads the output of `go test -v`. It requires
the child to fail the case that the broken store breaks.

The list of cases comes from a child run against the memory store with
every option. A case without a broken store fails the check, and so
does a broken store for a case the suite no longer has.

This is mutation testing applied to the suite instead of the code: each
broken store is a hand-written mutant of the reference, and the suite
must kill it. Core's gate already requires gremlins to kill at least
99% of the mutants of the code in 16 of its 19 gated layers. Nothing
checks the suites that other modules rely on, which is the gap this
closes.

### Capabilities behind decorators

A decorator of a seam that has optional capabilities implements
`Unwrap`, returning the value it wraps:

```go
// A tracing decorator over a cas.Store.
type traced struct {
    cas.Store
    tracer telemetry.Tracer
}

// Unwrap returns the store this decorator wraps.
func (t traced) Unwrap() cas.Store { return t.Store }
```

Each seam provides an `As` function per optional capability. It
asserts for the capability on the value, and then on each value
`Unwrap` returns, as `errors.As` walks an error chain:

```go
// AsStreamer returns the first Streamer in the chain that starts at s
// and follows Unwrap, and reports whether it found one.
func AsStreamer(s Store) (Streamer, bool)
```

The functions and the method each follows are:

| Package | Functions | Decorator method |
|---|---|---|
| `cas` | `AsStreamer` | `Unwrap() cas.Store` |
| `crypto` | `AsDestroyer`, `AsKeyGenerator` | `UnwrapKeeper() crypto.Keeper` |
| `crypto/sign` | `AsStreamingSigner`, `AsStreamingVerifier`, `AsContextSigner` | `Unwrap() sign.Signer` or `Unwrap() sign.Verifier` |

A `Keeper` decorator cannot implement `Unwrap() Keeper`, because
`crypto.Keeper` already has an `Unwrap` method that unwraps a data key.
It implements `UnwrapKeeper` instead.

Core's own assertions, in `cas.PutStream`, `cas.GetStream`,
`crypto.GenerateKey` and `sign.SignContext`, use them. Through two
decorators, each `As` function allocates nothing and takes 5 to 14 ns
in its package benchmark.

A suite case that wraps the store under test in the suite's own
decorator and calls the `As` function passes for every store, because
the result depends only on the `As` function. The check of the
preceding section found no broken store for such a case in `castest`,
so the suites have none. Each `As` function's own tests cover the
chain.

A decorator that also implements the capability itself, for example
to trace streaming calls, is found first and hides the capability of
the value it wraps, as a wrapping error with its own `As` method does.

## Alternatives considered

### A. Capability flags

A seam would report its capabilities through a method such as
`Capabilities() Set`.

**Why not:** a flag and the methods it describes are two sources of
truth that can disagree. A decorator would still have to forward
every capability method, so the flag would add a check without
removing the forwarding.

### B. Decorators forward every capability

The convention would require a decorator to implement every optional
interface of the value it wraps.

**Why not:** a decorator cannot implement an interface conditionally.
A decorator that implements `Streamer` claims the capability even when
the wrapped store lacks it. The standard library came to the same
conclusion for `http.ResponseWriter`: `http.NewResponseController`
expects the original writer, "or have an Unwrap method returning the
original ResponseWriter".

### C. Crash tests in each adapter's own repository

Each durable adapter would write its own restart and crash tests.

**Why not:** each adapter would restate the laws, and the restatements
would drift from the suite's. The suite states the laws once. The
adapter supplies only the fault injection that its storage allows.

## Drawbacks

- The broken stores and the child-process check add test code to
  `coretest` that no adapter uses.
- `WithCrash` is only as strong as the adapter's fault injection. An
  adapter that crashes only at convenient points passes a weaker
  check.
- Every decorator author has to know the `Unwrap` convention. A
  decorator without `Unwrap` still hides capabilities, and neither the
  compiler nor a suite detects it. A suite sees only the store it is
  given.
- The `As` functions add six exported names across three packages.

## Open questions

None.

## Unresolved / future work

- A filesystem seam with an in-memory, crash-simulating reference, so
  that durable adapters share one fault injector. Its method set
  depends on the operating system, and it needs its own RFC.

## References

- Go: `net/http.NewResponseController`, `errors.As` and `testing.TB`.
- CockroachDB Pebble, `vfs.NewCrashableMem`, `MemFS.CrashClone` and
  `vfs/errorfs`, <https://github.com/cockroachdb/pebble>.
- T. S. Pillai et al., "All File Systems Are Not Created Equal: On the
  Complexity of Crafting Crash-Consistent Applications", OSDI 2014.
- RFC-0027, content-addressed storage.
- RFC-0028, named object storage.
- ADR-0008, core defines contracts that describe IO.
