# Testkit Guide

> How to apply `go.thesmos.sh/testkit` generators to interfaces and
> types in this codebase. Follow these patterns exactly.

---

## Quick Reference

| Generator | Target | Command |
|-----------|--------|---------|
| `stub` | interface | `testkit stub -o <pkg>test/<name>_stub.gen.go <Interface>` |
| `suite` | interface | `testkit suite -o <pkg>test/<name>_spec.gen.go <Interface>` |
| `model` | interface | `testkit model -o <pkg>test/<name>_model.gen.go <Interface>` |
| `bench` | interface | `testkit bench -o <pkg>test/<name>_bench.gen.go <Interface>` |
| `builder` | struct | `testkit builder -o <pkg>test/builders.gen.go <Struct>` |
| `sentinel` | file | `testkit sentinel -o errors.gen_test.go` |
| `enum` | int type | `testkit enum -o enum.gen_test.go <Type1> <Type2>` |

All commands are run via `//go:generate` directives in the source file
that defines the type.

---

## Stub Generator

**Purpose:** Generate a configurable test double for an interface.

**When to use:** Any time production code depends on an interface and
tests need to control its behavior (return values, errors, latency,
faults).

**Directive (in source file):**

```go
//go:generate testkit stub -o hashertest/hasher_stub.gen.go Hasher
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

**Directive (in source file):**

```go
//go:generate testkit suite -o hashertest/hasher_spec.gen.go Hasher
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
func TestHLCClock(t *testing.T) {
    coretest.AssertClockContract(t,
        func() clock.Clock { return hlc.New(0) },

        // Now: HLC monotonicity — every instant is causally after the previous.
        coretest.ClockCustom("Now is HLC-monotone", func(t *testing.T, c clock.Clock) {
            prev := c.Now()
            for range 100 {
                next := c.Now()
                if !prev.HappensBefore(next) {
                    t.Fatalf("non-monotone: %+v then %+v", prev, next)
                }
                prev = next
            }
        }),

        // Now: tags every instant with the configured node.
        coretest.ClockCustom("Now tags node", func(t *testing.T, c clock.Clock) {
            if got := c.Now().Node; got != 0 {
                t.Fatalf("Node: got %d, want 0", got)
            }
        }),

        // Time: returns UTC.
        coretest.ClockCustom("Time returns UTC", func(t *testing.T, c clock.Clock) {
            if got := c.Time().Location(); got != time.UTC {
                t.Fatalf("Location: got %v, want UTC", got)
            }
        }),

        // Update: result is causally after the observed instant.
        coretest.ClockCustom("Update is causal", func(t *testing.T, c clock.Clock) {
            observed := clock.Instant{Wall: time.Now().UnixNano(), Logical: 99, Node: 7}
            got := c.Update(observed)
            if !observed.HappensBefore(got) {
                t.Fatalf("Update must be causally after observed: got=%+v obs=%+v", got, observed)
            }
        }),

        // NewTimer: zero-duration timer fires immediately.
        coretest.ClockCustom("NewTimer zero fires immediately", func(t *testing.T, c clock.Clock) {
            tm := c.NewTimer(0)
            select {
            case <-tm.C():
                // OK
            case <-time.After(time.Second):
                t.Fatal("zero-duration timer did not fire")
            }
        }),

        // NewTimer: Stop prevents firing.
        coretest.ClockCustom("NewTimer Stop prevents fire", func(t *testing.T, c clock.Clock) {
            tm := c.NewTimer(time.Hour)
            if !tm.Stop() {
                t.Fatal("Stop on pending timer must return true")
            }
            if tm.Stop() {
                t.Fatal("second Stop must return false")
            }
        }),
    )
}
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

// Store has Reader/Writer/Deleter shapes.
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

**Directive (in source file):**

```go
//go:generate testkit model -o cursortest/cursor_model.gen.go Cursor
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

**Directive (in source file):**

```go
//go:generate testkit bench -o hashertest/hasher_bench.gen.go Hasher
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

**Directive (in source file):**

```go
//go:generate testkit builder -o clocktest/builders.gen.go Instant InstantRange
```

**No per-field directives.** Works from exported struct fields.

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

| Package | Interface/Type | Generators |
|---------|---------------|------------|
| `crypto` | `Hasher` | stub, suite, model, bench |
| `crypto` | `MAC` | stub, suite, model, bench |
| `crypto/sign` | `Signer`, `Verifier` | stub, suite, bench |
| `clock` | `Clock` | stub, suite, model |
| `telemetry` | `Reporter` | stub, suite |
| `telemetry` | `Span` | stub, suite |
| `page` | `Cursor[T]` | stub, suite, model |
| `rand` | `Rand` | stub, suite, bench |
| `id` | `Generator` | stub |
| `telemetry` | `AttrKind`, `SpanKind` | enum |
| `id/ksuid` | errors | sentinel |
| `id/ulid` | errors | sentinel |
| `id/uuidv4` | errors | sentinel |
| `crypto/sign/ed25519` | errors | sentinel |
| `crypto/sign/ecdsap384` | errors | sentinel |
| `clock` | `Instant`, `InstantRange` | builder |
| `page` | `Page` | builder |
| `telemetry` | `InstrumentSpec`, `Attr` | builder |

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
7. **Generated files go in a `<pkg>test/` subdirectory.** External
   test package (`_test` suffix) for clean dependency boundaries.
8. **Run `make generate` after adding directives.** Then commit the
   generated files alongside the source.
