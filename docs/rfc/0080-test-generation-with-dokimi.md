---
rfc: 0080
title: Test Generation with dokimi
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Draft
created: 2026-10-27
updated: 2026-10-27
discussion: none
supersedes: none
superseded-by: none
produces-adr: tbd
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# RFC-0080: Test Generation with dokimi

## Summary

We propose that core generates its conformance suites, its test doubles, its
benchmarks and its sentinel tests with dokimi in place of testkit. Directives on
the declarations of core's interfaces generate the double and the suite of each
interface into its package of `coretest`. Hand-written checks with planted defects
replace the assertion bundles of the kits, and the test of each suite proves every
check against its defect. A directive on each implementation generates its
conformance test, its benchmarks and its fuzz targets. A directive on each package
clause generates the sentinel tests. Core then does not require testkit, and CI
checks every generated file against its directives.

## Motivation

### testkit beside assert

Core's hand-written tests use `go.dokimi.dev/assert`, as `docs/guides/testing.md`
states. The generated tests and the kits of `coretest` use testkit:

| Part | Count |
|---|---|
| `//go:generate testkit` directives | 96 in 36 files: 31 `sentinel`, 23 `stub`, 19 `suite`, 17 `bench`, 5 `model` and 1 `builder` |
| Files that import testkit | 178, of which 61 are hand-written |
| Entries of the assertion bundles of the kits | 184 |
| `//testkit:` directives on methods of production interfaces | 24 |

The test of an implementation calls the generated `AssertXContract` with bundles of
testkit assertions, and its benchmark passes the plug-ins of
`go.thesmos.sh/testkit/bench`. A module that imports a kit of `coretest`, such as
the doubles of `telemetrytest`, requires testkit too, because the generated
doubles import it.

### No gate checks the generated files of testkit

`make generate` runs the directives of kanon alone. The key `go.generate.commands`
of `.ergon.yaml` runs `go generate -run 'go tool kanon'`, and
`docs/guides/testkit.md` states the same. A generated file of testkit can drift
from its directive and from the interface that it doubles, and CI does not report
it.

### A check of a bundle need not fail

A bundle entry is a function that a test passes to `AssertXContract`. Core runs
each entry against its implementations alone, and an implementation that keeps a
law cannot show that the entry of the law fails. A probe moved the bundles of
`id.Generator` to dokimi, and the falsification of dokimi showed an entry that
cannot fail on its own. A non-zero `id.ID` always has 16, 20 or 32 bytes, so
"Generate Size matches a canonical width" fails exactly when "Generate returns a
non-zero ID" fails.

### Prior art

dokimi generates doubles, suites, builders, enumerations and sentinel tests from
directives in doc comments. Its suites run on `go.dokimi.dev/conform`, and its
doubles on `go.dokimi.dev/stub`. Each check of a suite has a planted defect.
The generated test of the suite runs every check against every defect. A defect
must fail its own check at the assertion of its statement, and it must pass every
other check of the suite.

## Detailed design

### Components

| Part | testkit | dokimi |
|---|---|---|
| Double of an interface | `//go:generate testkit stub` in `coretest/<pkg>test/doc.go` | `//dokimi:stub` on the interface |
| Suite of an interface | `//go:generate testkit suite` and the bundles `XContractAssertions` | `//dokimi:suite` on the interface, and a function with `//dokimi:check X.Method defect=<defect>` for each law |
| Place of the files of a kit | the flag `-o` in `coretest/<pkg>test` | `//dokimi:out path=../coretest/<pkg>test/` on the interface |
| Application to an implementation | `AssertXContract(t, factory, bundles...)` in its test | `//dokimi:subject X new=<factory>` on the implementation, and `//dokimi:excuse` for a law that it does not keep |
| Benchmarks and allocation ceilings | `//go:generate testkit bench` and `XBenchOnMethod(bench.PureAllocsWithin(0))` | the benchmarks of `subject`, and `//dokimi:budget X.Method allocs=0` |
| Models | `//go:generate testkit model`, a property over random calls | property checks of the hashes and the MACs over `Handle.Reference` |
| Builders of `clock.Instant` and `clock.InstantRange` | `//go:generate testkit builder` | `//dokimi:builder` |
| Sentinel tests | `//go:generate testkit sentinel` in the file of the sentinels | `//dokimi:sentinel` at the end of the package comment |
| Runtime of the generated code | `go.thesmos.sh/testkit` | `go.dokimi.dev/conform`, `go.dokimi.dev/stub` and `go.dokimi.dev/fault` |

### Placement

The kits keep their packages. `out` routes the files of each interface into its
kit. A path override may leave its package but not the root of the workspace
(eidos RFC-0016, Layout and routing). `//dokimi:out
path=../coretest/idtest/` on `id.Generator` writes into `coretest/idtest`.
`//dokimi:out path=../../coretest/cryptotest/` on `sign.Signer` writes into
`coretest/cryptotest`. The import paths of the kits do not change.

dokimi names a generated file after the source file of its declaration.
`id/generator.go` gives `coretest/idtest/generator_stub.go`, `generator_suite.go`
and the specs of both. The interfaces of one source file share the files of a
generator, so `clock/clock.go` gives one `clock_stub.go` with the doubles of
`Clock` and `Timer`.

The directives are in the doc comments of the declarations, in the production
packages. The 24 `//testkit:` directives of methods are deleted: 15
`nondeterministic`, 7 `mutator` and 2 `sample`. A type whose drawn values an
implementation refuses, such as the roles of the tagged hashes that the `sample`
directives of RFC-0029 cover, gets its values through `//dokimi:sample` on the
type or through `conform.Using`.

### Checks

Each entry of a bundle becomes a function of its kit whose doc comment opens with
"<Name> checks that". Its directive is `//dokimi:check X.Method
defect=<defect>`. The defect is a function of the kit. It returns the double of
the interface with the calls that break the law:

```go
// ReturnsNonZero checks that Generate does not return the Zero ID.
//
//dokimi:check Generator.Generate defect=returnsZero
func ReturnsNonZero(h *GeneratorHandle) {
	assert.False(h, h.Subject().Generate().IsZero(), h.Statement())
}

// returnsZero is a Generator whose Generate returns the Zero ID on every call.
func returnsZero(h *GeneratorHandle) id.Generator {
	return NewGeneratorStub(h, WithGeneratorGenerate(func() id.ID { return id.Zero }))
}
```

- Checks share a defect when the defect breaks each of their laws. The checks of
  a non-zero ID and of distinct IDs of `Generator` share `returnsZero`.
- An entry that cannot fail apart from another entry is deleted. The commit that
  moves its kit names it.
- An entry that states a fact of one implementation becomes a test of the package
  of the implementation. The ID of a hash and the width of a ULID are such facts.
  A suite contains the laws that every implementation keeps.
- An implementation that does not keep a law excuses its check.
  `id/constant` excuses `Generator.Generate/returns-distinct-i-ds`, because a
  constant Generator returns one ID. The identity spells the plural `IDs` as
  `i-ds`. dokimasia/eidos#92 asks that the word splitter keep `IDs` whole.

### Models

testkit's models make random calls and check two laws. A method without
`//testkit:nondeterministic` returns the same result for the same input, and the
`Verify` of a MAC accepts the result of its `Sign`. The tests of the hashes and the
MACs also pass the hash of the standard library as a reference.

- The models of `Clock`, `Generator` and `Rand` pass no reference. Every method of
  `Generator` and `Rand` is nondeterministic, and every method of `Clock` apart
  from `NewTimer`. The fuzz targets and the budgets of the generated suites make
  random calls of each callable, and the suite of `Timer` checks the timers of
  `NewTimer`. These models are deleted.
- The models of `Hasher` and `MAC` become property checks of their suites. A
  check compares each drawn call of the subject with the same call of
  `Handle.Reference`, and the application of each hash passes the hash of the
  standard library through `conform.Reference`. The law of determinism becomes a
  check that calls `Hash`, `HashTagged`, `CombineTagged` or `Sign` twice with one
  drawn input. The law of a MAC becomes a check that `Verify` accepts the result
  of `Sign`.

### Budgets

The bench plug-ins of core set 79 allocation ceilings in 21 tests, each of 0:
`PureAllocsWithin`, `PredicateAllocsWithin`, `MutatorAllocsWithin` and the
`ReleaseAllocsWithin` of `telemetrytest`. Each becomes `//dokimi:budget X.Method
allocs=0` on the subject whose test passed it, because an implementation without
a plug-in, such as `rand/crypto`, allocates. No budget states a latency, because
a latency ceiling fails on a slower machine.

### Sentinels

`//dokimi:sentinel` at the end of the package comment of each of the 31 packages
writes `errors_sentinel_test.go`, with a `TestErr<Name>` for each sentinel. It
checks that each message opens with the prefix of the package, that no two
messages are equal, that no message opens with another, and that `errors.Is`
matches no other sentinel of the package. The commit deletes `errors.gen_test.go`
and its `//go:generate` line. The hand-written `TestErrors` of `errors_test.go`
keeps the class of each sentinel.

### The hand-written kits

| Kit | Design |
|---|---|
| `blobtest` | A suite of `blob.Store` and a suite of `blob.RangeReader`. `WithReopen` becomes `conform.Restartable`. `WithCrash` becomes the crash of a subject during a call, which dokimasia/dokimi-go#15 requests. A store that reads ranges takes a second `subject` directive in place of `WithRangeReader` |
| `castest` | A suite of `cas.Store`. `WithReopen` and `WithCrash` move as in `blobtest`. `WithZeroAllocGet` becomes `//dokimi:budget Store.Get allocs=0` on the subject |
| `cryptotest` | The suites of `AEAD` and `Keeper` move from hand-written functions to checks. The other interfaces move as in the components table |
| `telemetrytest` | The recorder of the kit is the subject of the suites of `Reporter`, `Tracer`, `Span`, `Counter`, `Gauge` and `Histogram`. The tracer and the span are unexported types, so the test of the recorder applies each suite through a factory that returns the interface. The checks of the release of an instrument (RFC-0046) move from `release.go` to the suites of the instruments |
| `epochtest` | `AssertFencedWriter` checks a generic `FencedSystem[H]` and no interface. Its assertions move to assert, and its exported API does not change |
| `tsptest` | Its tests move to assert, and its sentinels to `//dokimi:sentinel` |

The suites of `blob.Store` and `cas.Store` keep the laws of RFC-0038. A store
returns each object after a reopen, and a `Put` that a crash interrupts leaves the
object absent or whole.

### Applications with options

The directive `subject` takes `new` and `cases`, and its test passes only the
excuses. A subject whose checks need `conform.Reference` or `conform.Restartable`
needs a parameter that passes the options of its application.
dokimasia/dokimi#31 requests it. The hashes, the MACs and the stores move after
the release of that parameter, so no test of an implementation is written twice.

### Toolchain

- `go.mod` moves `go.dokimi.dev/assert` from v0.3.0 to v0.9.0, adds
  `go.dokimi.dev/conform` v0.6.0, `go.dokimi.dev/stub` v0.1.1 and
  `go.dokimi.dev/fault` v0.2.0, and drops `go.thesmos.sh/testkit` in the last
  commit.
- `.dokimi.yaml` at the root sets `version: 1`, `strict: true` and the policy
  `golang.width` of `"120"`, under the SPDX header.
- `go.generate.commands` of `.ergon.yaml` runs a released dokimi before kanon:
  `[go, run, go.dokimi.dev/dokimi/cmd/dokimi@<release>, run]`.
  `verify-generate-go` then checks every generated file of dokimi on each change.
  A change of the release regenerates every file in the same commit.
- The `depguard` rule of tests in `.ergon/local/.golangci.yml` allows conform,
  stub and fault, and drops testkit and `testkit/model`. The exclusion of the
  callbacks of testkit's suites from `thelper` is deleted.
- A guide to dokimi in core replaces `docs/guides/testkit.md`, and
  `docs/guides/testing.md` names dokimi.
- The ADR of this RFC records the test-code allow-list. ADR-0015 states that a
  change of that list takes an ADR.

### Doubles of the kits

The doubles of dokimi keep the names of the doubles of testkit: `New<X>Stub`, the
fields `On<Method>` with `Returns` and `Func`, the setters `With<X><Method>`, and
the options `<X>StubStrict`, `<X>StubDelegateTo` and `<X>StubWithRandSource`.

- `New<X>Stub` takes an `assert.TB`, which a `*testing.T` satisfies.
- `<X>StubWithClock` takes an `assert.Clock` in place of a `clock.Clock`.
- The `fault` directives of dokimi and `<X>StubWithLatency` replace testkit's
  `Faults`, `FaultsFor`, `Latency` and `Times`.

A test of a consumer that uses those parts changes, so the changeset of each kit
is a breaking change.

### Order of the change

1. Move assert to v0.9.0, and add conform, stub and fault.
2. Add `.dokimi.yaml` and the command of dokimi in `go.generate.commands`.
3. Move the 31 sentinel packages.
4. Move `idtest`, `randtest`, `clocktest` with its builders, and `telemetrytest`
   with its recorder, one kit for each commit.
5. Move the suites of `cryptotest` without a reference: `Stream`, `XOF`,
   `XOFStream`, `AEAD`, `Keeper`, `Signer`, `Verifier`, `SignStream` and
   `VerifyStream`.
6. Move `Hasher`, `MAC`, `blobtest` and `castest` once dokimasia/dokimi#31 is
   released. The crash checks of the stores also need dokimasia/dokimi-go#15.
7. Move `epochtest` and `tsptest`.
8. Drop testkit from `go.mod`, from the lint rules and from the guides, and
   write the ADR.

### Invariants

- Every check of a kit fails on its planted defect at the assertion of its
  statement, and every other check of its suite passes on that defect.
- Every law of today's bundles, models and hand-written suites becomes a check or
  a test of an implementation. A deleted law is named in the commit that deletes
  it, with its reason.
- Every generated file equals the output of the pinned release of dokimi for its
  directives.
- The import path of every kit does not change.
- Every allocation ceiling of today keeps its value.

### Bounds

| Quantity | Value |
|---|---|
| Declarations with a directive of dokimi | 25 interfaces and 2 builders |
| Package clauses with `//dokimi:sentinel` | 31 |
| Generated files of the kits | 70 in the probe: 17 doubles, 17 suites, 1 file of builders, and the spec of each |
| Hand-written files that change | 61: 22 bundle files of the kits, 11 files of the hand-written kits and 28 tests of implementations |

### Failure handling

| Condition | Result |
|---|---|
| A generated file differs from the output of its directives | `verify-generate-go` fails |
| A check passes on its planted defect | The test of the suite fails |
| A planted defect fails another check | The test of the suite fails and lists the other check |
| A subject breaks a law of its suite without an excuse | The conformance test of the subject fails |
| A check calls `Handle.Restart` or `Handle.Reference` without its option | The check ends with a fault that wraps `conform.ErrOption` |
| An excuse names no check of the suite | dokimi refuses the directive with `DOKIMISUITE-0006` |

### Probe

A probe ran on a copy of core at 9371549 with a local build of dokimi at a92d631,
assert v0.9.0, conform v0.6.0, stub v0.1.1 and fault v0.2.0:

- dokimi generated the doubles and the suites of the 25 interfaces and the 2
  builders without a finding. The generated tests of every kit pass once the
  hand-written files that still call testkit are set aside.
- `id.Generator` moved whole. Its three checks pass on `id/ulid`, and
  `id/constant` passes two of them and excuses the third. The test of the suite
  falsifies each check.
- `//dokimi:sentinel` on `note` wrote `errors_sentinel_test.go`, whose four tests
  pass.
- Against assert v0.9.0, every test binary of core compiles apart from the three
  implementations of `id.Generator` that the probe did not move. The probe did not
  run those suites.
- `ergon license check` skips the generated files.

### Tests

| # | Guarantee |
|---|---|
| 1 | The test of each suite falsifies every check on its planted defect |
| 2 | The conformance test of each implementation passes every check that it does not excuse |
| 3 | `Test<Impl>ConformsTo<X>Allocs` and the benchmarks state each allocation ceiling of today |
| 4 | `TestErr<Name>` passes for each sentinel of the 31 packages |
| 5 | `verify-generate-go` passes after `dokimi run` on a clean tree |
| 6 | The tests of the implementations pass under the race detector, and dokimi-mutate-go detects every mutant of the production packages |

## Alternatives considered

### A. Keep testkit

Core would keep testkit for the kits and assert for its hand-written tests.

**Why not:** two test toolchains, generated files that no gate checks, a
dependency of every module that imports a kit, and bundle entries that no test
falsifies.

### B. Hand-written suites on assert alone

Each kit would contain a hand-written double and a hand-written suite of each
interface on assert.

**Why not:** 25 interfaces need a double, a suite, a benchmark of each method and
a fuzz target of each method. dokimi generates them from the declarations and
keeps them equal to the declarations in CI.

### C. Kits beside their packages

The kits would move to the default place of dokimi, such as `clock/clocktest`.

**Why not:** the import path of every kit would change for every consumer, and
`out` writes into `coretest` already.

### D. Checks of determinism from the mixin `pure`

The shape catalog of eidos stamps a mixin `pure` on a callable. dokimi would
generate the check of determinism from it.

**Why not:** dokimi does not generate a check from `pure`. It reads the mixins
`nil-safe`, `concurrent` and `concurrent-readers` for inputs and benchmarks.
A hand-written check states the law of each method in one function.

### E. Hand-written applications of the subjects with options

The hashes, the MACs and the stores would call `Run<X>Suite` with their options
by hand before dokimasia/dokimi#31.

**Why not:** each of those tests would be written twice: by hand, and again with
the directive once the parameter exists.

## Drawbacks

- The doc comments of 25 interfaces, 2 structs and 31 package clauses contain
  directives of dokimi.
- Each law needs a planted defect. The 184 entries of the bundles become new test
  code, apart from the repeated entries and the facts of one implementation.
- A consumer whose tests configure the doubles of a kit with the parts that
  change adapts its calls.
- Step 6 depends on two requests to dokimi and dokimi-go. The identity of a check
  with a plural initialism changes when dokimasia/eidos#92 is released.
- assert moves from v0.3.0 to v0.9.0 across nine releases. Every test binary of
  core compiles against v0.9.0, and the suites of core have not run against it.

## Open questions

None.

## Unresolved / future work

None.

## References

- ADR-0015, the test-code allow-list.
- RFC-0029, the `sample` directives of the tagged hashes.
- RFC-0038, the conformance of durable adapters.
- RFC-0046, released instruments.
- `docs/guides/testkit.md` and `docs/guides/testing.md`.
- dokimasia/dokimi, the Go guide,
  <https://github.com/dokimasia/dokimi/blob/main/docs/guides/go.md>.
- dokimasia/eidos, RFC-0016, Layout and routing,
  <https://github.com/dokimasia/eidos/blob/main/docs/rfc/0016-layout-and-routing.md>.
- dokimasia/eidos#92, <https://github.com/dokimasia/eidos/issues/92>.
- dokimasia/dokimi-go#15, <https://github.com/dokimasia/dokimi-go/issues/15>.
- dokimasia/dokimi#31, <https://github.com/dokimasia/dokimi/issues/31>.
