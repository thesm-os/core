# Testkit Guide

> How to apply `go.thesmos.sh/testkit` generators to interfaces and
> types in this codebase. Follow these patterns exactly.

---

## Layout convention

Generated test infrastructure for package `<pkg>` lives under the
umbrella `coretest/<pkg>test/` directory at the module root, NOT in a
sibling-of-source `<pkg>/<pkg>test/` subdirectory. Rationale: a single
tree of test infrastructure is easier to enumerate, decouples generated
artefacts from production package directories, and gives `coretest` a
meaningful purpose as the parent.

Examples:

- `clock.Clock` interface → `coretest/clocktest/`
- `crypto.Hasher` interface → `coretest/cryptotest/`
- `crypto.MAC` interface → `coretest/cryptotest/` (same package, different files)

Sub-package interfaces collapse into the parent's testkit dir
(`crypto/hmac/sha256.MAC` → `coretest/cryptotest/`). One dir per top-
level package keeps the tree shallow.

`//go:generate` directives live in `coretest/<pkg>test/doc.go`, NOT in
the production source file. The directives use `-p
go.thesmos.sh/core/<pkg>` to point testkit at the source package.

## Quick Reference

| Generator | Target | Command (run from `coretest/<pkg>test/`) |
|-----------|--------|---------|
| `stub` | interface | `testkit stub -p go.thesmos.sh/core/<pkg> -o <name>_stub.gen.go <Interface>` |
| `suite` | interface | `testkit suite -p go.thesmos.sh/core/<pkg> -o <name>_spec.gen.go <Interface>` |
| `model` | interface | `testkit model -p go.thesmos.sh/core/<pkg> -o <name>_model.gen.go <Interface>` |
| `bench` | interface | `testkit bench -p go.thesmos.sh/core/<pkg> -o <name>_bench.gen.go <Interface>` |
| `builder` | struct | `testkit builder -p go.thesmos.sh/core/<pkg> -o <name>_fixtures.gen.go <Struct>...` |
| `sentinel` | file | `testkit sentinel -o errors.gen_test.go` (runs in source pkg) |
| `enum` | int type | `testkit enum -o enum.gen_test.go <Type1> <Type2>` (runs in source pkg) |

All commands are run via `//go:generate` directives. For interface
generators (`stub` / `suite` / `model` / `bench` / `builder`), put the
directive in `coretest/<pkg>test/doc.go`. For per-source-file generators
(`sentinel` / `enum`), put the directive in the source file declaring
the errors / enum type — those generated files stay in the source
package.

---

## Stub Generator

**Purpose:** Generate a configurable test double for an interface.

**When to use:** Any time production code depends on an interface and
tests need to control its behavior (return values, errors, latency,
faults).

**Directive (in `coretest/<pkg>test/doc.go`):**

```go
//go:generate testkit stub -p go.thesmos.sh/core/crypto -o hasher_stub.gen.go Hasher
```

**No per-method directives needed.** Works from method signatures alone.

**Consumer test pattern:**

```go
func TestDigest(t *testing.T) {
    stub := hashertest.NewHasherStub(t, hashertest.HasherStubStrict())
    stub.OnHash.Returns(crypto.Digest{...})
    stub.OnCombine.Func(func(l, r crypto.Digest) crypto.Digest {
        return l // test-specific logic
    })

    result := productionCode(stub)
    testkit.Equal(t, result, expected)
    stub.OnHash.AssertCalledOnce()
}
```

**Key capabilities:**

- `.Returns(...)` — fixed return
- `.Func(fn)` — dynamic behavior
- `.Faults(err, n)` — error on every Nth call
- `.FaultsFor(duration, err)` — time-windowed faults
- `.Latency(d)` — simulated delay (honors virtual clock)
- `.Times(n)` — assert exact call count
- `HasherStubStrict()` — unconfigured methods fail the test
- `HasherStubDelegateTo(impl)` — wrap a real implementation

---

## Suite Generator

**Purpose:** Generate conformance tests that verify an implementation
satisfies its interface contract.

**When to use:** Every interface implementation needs a conformance
spec. Run it against in-memory fakes, production implementations,
and any new backend.

**Directive (in `coretest/<pkg>test/doc.go`):**

```go
//go:generate testkit suite -p go.thesmos.sh/core/crypto -o hasher_spec.gen.go Hasher
```

**Per-method directives (placed above method signatures):**

```go
type Hasher interface {
    //testkit:ctx
    //testkit:errors ErrUnsupported
    Hash(ctx context.Context, data []byte) (Digest, error)

    //testkit:pure
    Combine(left, right Digest) Digest

    //testkit:ctx
    NewStream() (Stream, error)
}
```

| Directive | Effect |
|-----------|--------|
| `//testkit:ctx` | Generates context cancellation/deadline subtests |
| `//testkit:errors ErrX` | Tests that the sentinel is returned on empty/invalid input |
| `//testkit:pure` | Tests determinism (multiple calls, same result) |
| `//testkit:nilsafe` | Tests that nil/zero input doesn't panic |
| `//testkit:bounded min max` | Tests return value within range |

**Consumer test pattern:**

The generated suite detects method shapes automatically. Each method
gets a typed context (ReaderContext, WriterContext, PureContext, etc.)
and consumer-provided assertion primitives run against it.

```go
// Clock methods are Pure-shaped (no ctx, no error) but NOT
// deterministic — Now() advances HLC logical, Time() reads
// wall clock. The auto-generated smoke tests verify each method
// is callable. ClockCustom adds domain-specific assertions.
//
// Standard contract assertions are packaged in
// clocktest.ClockContractAssertions(); spread them via `...` and
// add per-test custom assertions on top.
func TestHLCClock(t *testing.T) {
    clocktest.AssertClockContract(t,
        func() clock.Clock { return hlc.New(0) },
        clocktest.ClockContractAssertions()...,
    )
}
```

The standard assertion bundle (`ClockContractAssertions`) is
hand-written in `coretest/clocktest/clock_assertions.go` — see that
file for the full list. Test-specific cases that don't belong in the
shared bundle are added inline:

```go
clocktest.AssertClockContract(t,
    factory,
    append(
        clocktest.ClockContractAssertions(),
        clocktest.ClockCustom("HLC tags node 7", func(t *testing.T, c clock.Clock) {
            if got := c.Now().Node; got != 7 {
                t.Fatalf("Node: got %d, want 7", got)
            }
        }),
    )...,
)
```

The generated suite provides:

- **Auto smoke tests** per method (`Now/smoke`, `Time/smoke`, etc.)
- **ClockOnNow/ClockOnTime/ClockOnUpdate/ClockOnNewTimer** for
  PureAssertion plug-ins (use for methods that ARE deterministic)
- **ClockCustom** for domain-specific assertions (use when the
  method is stateful, like HLC's Now)
- **ClockOnAll** for cross-method assertions

Timer is tested through Clock's NewTimer — there is no separate
Timer conformance suite. Timer assertions live in ClockCustom
subtests that exercise the Timer returned by NewTimer.

**Other shape examples** (the suite generator detects shapes from
method signatures and emits matching plug-in points). For a
hypothetical CRUD store with Reader, Writer, and Deleter methods:

```go
func TestInMemoryStore(t *testing.T) {
    storetest.AssertStoreContract(t, factory,
        storetest.StorePrePopulate(func(ctx context.Context, s Store) {
            _ = s.Put(ctx, Item{ID: "k1", Name: "test"})
        }),
        storetest.StoreOnGet(
            suite.AssertReturnsForKey[Store, string, Item]("k1", Item{ID: "k1", Name: "test"}),
            suite.AssertReturnsSentinel[Store, string, Item]("missing", ErrNotFound),
            suite.AssertConsistentReads[Store, string, Item]("k1", 3),
        ),
        storetest.StoreOnPut(
            suite.AssertWriteSucceeds[Store, Item](Item{ID: "k2", Name: "new"}),
        ),
        storetest.StoreOnDelete(
            suite.AssertDeleteSucceeds[Store, string]("k1"),
            suite.AssertDeleteIdempotent[Store, string]("k1"),
        ),
    )
}
```

**Available assertion primitives by shape:**

| Shape | Primitives |
|-------|-----------|
| Reader | `AssertReturnsForKey`, `AssertReturnsSentinel`, `AssertConsistentReads`, `AssertReadsAreNonMutating`, `AssertReaderConcurrentSafe` |
| Writer | `AssertWriteSucceeds`, `AssertWriteIsObservable`, `AssertWriteRejectInvalid`, `AssertWriteOverwrite` |
| Deleter | `AssertDeleteSucceeds`, `AssertDeleteIdempotent`, `AssertDeleteReturnsNotFound` |
| Aggregator | `AssertAggregatorReturns`, `AssertAggregatorBounded`, `AssertAggregatorConsistent` |
| Pure | `AssertDeterministic`, `AssertNoSideEffects` |
| Predicate | `AssertPredicateConsistent`, `AssertPredicateReturns` |
| Lifecycle | `AssertLifecycleSucceeds`, `AssertLifecycleIdempotent`, `AssertLifecycleRespectsContext` |
| Stream | `AssertStreamCompletes`, `AssertStreamRespectsBreak`, `AssertStreamReentrant`, `AssertStreamYieldsInOrder`, `AssertStreamHasNoDuplicates` |
| Cross | `AssertReadAfterWrite`, `AssertDeleteRemovesValue`, `AssertStreamReflectsMutations` |

---

## Model Generator

**Purpose:** Property-based state-machine testing. Generates random
action sequences, compares SUT against a reference, checks algebraic
invariants.

**When to use:** Interfaces with state (CRUD stores, caches, ledgers,
registries). Catches bugs that targeted unit tests miss: ordering
violations, lost writes, stale reads under concurrency.

**Directive (in `coretest/<pkg>test/doc.go`):**

```go
//go:generate testkit model -p go.thesmos.sh/core/page -o cursor_model.gen.go Cursor
```

**Per-method directives:**

```go
type Store interface {
    //testkit:errors ErrNotFound
    Get(ctx context.Context, id string) (Item, error)

    Put(ctx context.Context, item Item) error

    //testkit:deleter
    Delete(ctx context.Context, id string) error
}
```

| Directive | Effect |
|-----------|--------|
| `//testkit:errors ErrX` | Sentinel for the reference model |
| `//testkit:deleter` | Marks method as Deleter shape (vs Writer) |
| `//testkit:keyfield Field` | Overrides key extraction (default: "ID") |
| `//testkit:appends` | Chain-append shape (audit chains) |
| `//testkit:verifies` | Chain-verify shape |
| `//testkit:replays` | Chain-replay shape |
| `//testkit:time-aware` | Inject TestClock pair for TTL testing |

**Three tiers of adoption:**

- **Tier 0** — zero consumer code. Framework auto-synthesizes reference + actions + laws.
- **Tier 1** — consumer supplies a reference implementation.
- **Tier 2** — consumer adds domain-specific laws.

**Consumer test pattern:**

```go
func TestStoreModel(t *testing.T) {
    // Tier 0: fully automatic
    storetest.AssertStoreModel(t, func() Store {
        return NewInMemoryStore()
    })
}

func TestStoreModelConcurrent(t *testing.T) {
    // Concurrent linearizability via Porcupine
    storetest.AssertStoreModel(t, factory,
        storetest.StoreModelConcurrent(4, 50),
    )
}
```

**Key options:**

- `StoreModelReference(factory)` — custom reference
- `StoreModelLaw(l)` — add a custom invariant
- `StoreModelSkipLaw(id)` — opt out of an auto-law
- `StoreModelConcurrent(workers, ops)` — Porcupine linearizability
- `StoreModelGoroutineLeakCheck()` — detect goroutine leaks

---

## Bench Generator

**Purpose:** Generate performance benchmarks per method with
allocation tracking and concurrency throughput.

**When to use:** Any interface on a hot path. Establishes performance
baselines and catches allocation regressions.

**Directive (in `coretest/<pkg>test/doc.go`):**

```go
//go:generate testkit bench -p go.thesmos.sh/core/crypto -o hasher_bench.gen.go Hasher
```

**Uses the same per-method directives as `suite`.**

**Consumer test pattern:**

```go
func BenchmarkHasher(b *testing.B) {
    hashertest.BenchmarkHasherContract(b,
        func() crypto.Hasher { return sha256.New() },
        hashertest.HasherBenchOnHash(
            bench.ReaderHotPath[...](testData),
            bench.ReaderAllocsWithin[...](testData, 0),
            bench.ReaderConcurrentThroughput[...](testData, 8),
        ),
    )
}
```

---

## Builder Generator

**Purpose:** Generate fluent builders for struct test fixtures.

**When to use:** Structs with many fields where tests need variations.
Eliminates repetitive struct literals.

**Directive (in `coretest/<pkg>test/doc.go`):**

```go
//go:generate testkit builder -p go.thesmos.sh/core/clock -o clock_fixtures.gen.go Instant InstantRange
```

**Consumer pattern:**

```go
func TestSomething(t *testing.T) {
    inst := clocktest.NewInstant().
        WithWall(time.Now().UnixNano()).
        WithLogical(5).
        WithNode(1).
        Build()
}
```

### Defaults

`NewT()` returns an empty builder by default. Two opt-in mechanisms
seed it with non-zero values:

**1. Convention-based — `<Type>Defaults()` function.** Define
`<Type>Defaults() <Type>` in either the source package or the test
package (`coretest/<pkg>test/`). The generator auto-discovers it and
makes `NewT()` return a builder seeded with the result:

```go
// coretest/clocktest/clock_fixtures.go (hand-written)

func InstantDefaults() clock.Instant {
    return clock.Instant{
        Wall:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano(),
        Logical: 0,
        Node:    1,
    }
}
```

After this, `clocktest.NewInstant()` returns a builder pre-seeded with
the 2026-01-01 / Node 1 baseline. `NewInstantFrom(clock.Instant{})`
remains the explicit-zero escape hatch.

**2. Per-field directive — `//testkit:default`.** Annotate the source
struct field with a trailing comment carrying the literal default
value (same-line, struct-tag-style):

```go
// clock/instant.go (production source)

type Instant struct {
    Wall    int64  //testkit:default "1735689600000000000"
    Logical uint32
    Node    NodeID //testkit:default "1"
}
```

The generator emits `NewInstant()` returning a builder with those
fields pre-populated; un-annotated fields stay zero. Doc-comment
placement (the directive on its own line above the field) also
works, but trailing is preferred — matches the visual weight of
struct tags and keeps the field declaration compact.

**When to pick which:**

- **Convention** when defaults need runtime computation (e.g. a
  `time.Now()`-based fixture, or values derived from other defaults).
- **Directive** when literals suffice and you want defaults visible
  at the type declaration in production source.

Both produce the same surface API. Pick one per type.

### Hand-rolled named variants

Defaults centralise *one* baseline; tests with multiple equally-common
shapes still benefit from named variants. Hand-rolled canonical
fixtures live in `coretest/<pkg>test/<name>_fixtures.go` (no `.gen.`
suffix), using the generated builder under the hood, following
`New<Type><Variant>` naming:

```go
// coretest/clocktest/clock_fixtures.go (hand-written)

func NewInstantOrigin() *InstantBuilder {
    return NewInstant() // Wall=0, Logical=0, Node=0
}

func NewInstantSample() *InstantBuilder {
    return NewInstant().
        WithWall(referenceWall).
        WithLogical(0).
        WithNode(1)
}
```

Tests then grab a baseline and tweak via further `With*` calls:

```go
inst := clocktest.NewInstantSample().WithLogical(5).Build()
```

---

## Sentinel Generator

**Purpose:** Generate tests for sentinel errors — prefix consistency,
uniqueness, `errors.Is` / `errors.As` correctness.

**When to use:** Any package that exports sentinel error variables or
custom error types.

**Directive (in the file declaring the errors):**

```go
//go:generate testkit sentinel -o errors.gen_test.go
```

**No type arguments.** Auto-discovers all exported sentinel vars and
error types in the file.

**What it tests:**

- All sentinels share a consistent package prefix
- No two sentinels have the same `.Error()` message
- `errors.Is(a, b)` is false for different sentinels
- Sentinels survive `errors.Join` and `fmt.Errorf` wrapping
- Custom error types round-trip through `errors.As`
- `Is()` and `Unwrap()` methods (when present) behave correctly

---

## Enum Generator

**Purpose:** Generate exhaustiveness and round-trip tests for
iota-based enum types.

**When to use:** Any `type X int` (or uint8, etc.) with a const
block using `iota`.

**Directive (in the file declaring the enum):**

```go
//go:generate testkit enum -o enum.gen_test.go AttrKind SpanKind
```

**What it tests:**

- Correct value count (catches accidentally deleted/added constants)
- Zero value is first iota
- All values are distinct
- If `String()` exists: stringer output, parse round-trip
- If `MarshalText()` exists: marshal/unmarshal round-trip
- If `MarshalJSON()` exists: JSON round-trip
- Rejects unknown strings/bytes on parse/unmarshal

---

## Applying to This Codebase

**Priority targets in `go.thesmos.sh/core`:**

Interface generators (stub / suite / model / bench / builder) emit to
`coretest/<pkg>test/`. Per-source-file generators (sentinel / enum)
emit alongside the source.

| Source pkg | Interface/Type | Generators | Output dir |
|---|---|---|---|
| `crypto` | `Hasher` | stub, suite, model, bench | `coretest/cryptotest/` |
| `crypto` | `MAC` | stub, suite, model, bench | `coretest/cryptotest/` |
| `crypto/sign` | `Signer`, `Verifier` | stub, suite, bench | `coretest/cryptotest/` |
| `clock` | `Clock` | stub, suite, model, bench | `coretest/clocktest/` |
| `telemetry` | `Reporter` | stub, suite | `coretest/telemetrytest/` |
| `telemetry` | `Span` | stub, suite | `coretest/telemetrytest/` |
| `page` | `Cursor[T]` | stub, suite, model | `coretest/pagetest/` |
| `rand` | `Rand` | stub, suite, bench | `coretest/randtest/` |
| `id` | `Generator` | stub | `coretest/idtest/` |
| `clock` | `Instant`, `InstantRange` | builder | `coretest/clocktest/` |
| `page` | `Page` | builder | `coretest/pagetest/` |
| `telemetry` | `InstrumentSpec`, `Attr` | builder | `coretest/telemetrytest/` |
| `telemetry` | `AttrKind`, `SpanKind` | enum | `telemetry/` (in-source) |
| `id/ksuid` | errors | sentinel | `id/ksuid/` (in-source) |
| `id/ulid` | errors | sentinel | `id/ulid/` (in-source) |
| `id/uuidv4` | errors | sentinel | `id/uuidv4/` (in-source) |
| `crypto/sign/ed25519` | errors | sentinel | `crypto/sign/ed25519/` (in-source) |
| `crypto/sign/ecdsap384` | errors | sentinel | `crypto/sign/ecdsap384/` (in-source) |

---

## Rules

1. **Every interface gets stub + suite.** Minimum viable coverage.
2. **Stateful interfaces also get model.** If state exists, property
   testing catches what unit tests miss.
3. **Hot-path interfaces get bench.** Allocation tracking prevents
   regression.
4. **Every sentinel-error file gets `testkit sentinel`.** Catches
   duplicate messages and broken wrapping.
5. **Every iota enum gets `testkit enum`.** Catches stale stringers
   and missing parse cases.
6. **Structs used in test fixtures get builder.** Eliminates struct
   literal noise in tests.
7. **Generated files go in `coretest/<pkg>test/`.** Umbrella tree at
   the module root, NOT in a sibling-of-source subdirectory. See
   "Layout convention" at the top of this guide.
8. **Hand-rolled fixtures live alongside generated builders.**
   `coretest/<pkg>test/<name>_fixtures.go` (no `.gen.`) holds canonical
   `New<Type><Variant>` factories that wrap the generated builder. See
   the Builder section.
9. **Run `make generate` after adding directives.** Then commit the
   generated files alongside the source.
