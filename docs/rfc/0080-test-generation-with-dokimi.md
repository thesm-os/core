---
rfc: 0080
title: Test Generation with dokimi
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-10-27
updated: 2026-10-27
discussion: none
supersedes: none
superseded-by: none
produces-adr: ADR-0075
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
`nondeterministic`, 7 `mutator` and 2 `sample`. A parameter whose drawn values an
implementation refuses takes `//dokimi:sample` on the parameter, and every call
that the suite generates for its method takes the sample. The roles of
`HashTagged` and `CombineTagged`, which the `sample` directives of RFC-0029 cover,
take 0x01 and 0x81. A type that the suite cannot build for one implementation,
such as a `crypto.Digest` of the width of one hash, gets its values through
`conform.Using` in the application.

### Checks

Each entry of a bundle becomes a function of its kit whose doc comment opens with
"<Name> checks that". Its directive is `//dokimi:check X.Method
defect=<defect>`. Two suites of core declare an interface named `Store`, so the
checks of `blob.Store` and `cas.Store` name the package too, as in
`//dokimi:check blob.Store.Put defect=<defect>`. The defect is a function of the
kit. It returns the double of the interface with the calls that break the law:

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
  `id/constant` excuses `Generator.Generate/returns-distinct-ids`, because a
  constant Generator returns one ID.
- A check that calls `Handle.Restart`, `Handle.Crash`, `Handle.Reference` or
  `Handle.Second` gives its planted defect the same door: `restart=`, `crash=`,
  `reference=` or `second=` beside `defect`, each a function of the kit.

### Models

testkit's models make random calls and check two laws. A method without
`//testkit:nondeterministic` returns the same result for the same input, and the
`Verify` of a MAC accepts the result of its `Sign`. The tests of the hashes and the
MACs also pass the hash of the standard library as a reference.

- The models of `Clock`, `Generator` and `Rand` pass no reference. Every method of
  `Generator` and `Rand` is nondeterministic, and every method of `Clock` apart
  from `NewTimer`. The budgets of the subjects make random calls of each callable
  that a model called, and the suite of `Timer` checks the timers of `NewTimer`.
  These models are deleted.
- The models of `Hasher` and `MAC` become property checks of their suites. A
  check compares `Hash` or `Sign` of drawn data with the same call of
  `Handle.Reference`, and the application of each hash or MAC passes the hash or
  the HMAC of the standard library through `conform.Reference`. Property checks
  of the tagged layouts compare `HashTagged` and `CombineTagged` of drawn roles
  and data with `Hash` of the subject. The law of determinism becomes a property
  check that passes `Hash`, `HashTagged`, `CombineTagged` or `Sign` and one drawn
  input to `assert.Deterministic`. The law of a MAC becomes a check that `Verify`
  accepts the result of `Sign`.

### Budgets

The bench plug-ins of core set 79 allocation ceilings in 21 tests, each of 0:
`PureAllocsWithin`, `PredicateAllocsWithin`, `MutatorAllocsWithin` and the
`ReleaseAllocsWithin` of `telemetrytest`. Each becomes `//dokimi:budget X.Method
allocs=0` on the subject whose test passed it. A budget on an interface would also
apply to an implementation without a plug-in, such as the counter of
`telemetrytest.Recorder`. Its Add appends to a series and allocates. A subject of
an unexported type, such as an instrument of `telemetry/noop`, takes no `subject`
directive, so its ceilings stay in its `Test<Unit>Allocs`. No budget states a
latency, because a latency ceiling fails on a slower machine.

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
| `blobtest` | A suite of `blob.Store` and a suite of `blob.RangeReader`. `WithReopen` becomes `conform.Restartable`, and a check reopens its subject with `Handle.Restart`. `WithCrash` becomes `conform.Crashable`, and a check crashes its subject during a call with `Handle.Crash`. A store that reads ranges takes a second `subject` directive in place of `WithRangeReader` |
| `castest` | A suite of `cas.Store`. `WithReopen` and `WithCrash` move as in `blobtest`. `WithZeroAllocGet` is deleted. A budget measures the generated calls of `Get`, which pass the zero digest to an empty store, so `TestStoreAllocs` of `cas/memory` keeps the ceiling of a Get of a stored value |
| `cryptotest` | The suites of `AEAD` and `Keeper` move from hand-written functions to checks. The suites of the signature streams move to `StreamingSigner` and `StreamingVerifier`, whose checks call `NewSignStream` and `NewVerifyStream`. `XOFStream`, `SignStream`, `VerifyStream`, `Destroyer` and `KeyGenerator` keep doubles alone. The assertions of the optional capabilities of a Keeper and a Signer become functions on assert, such as `AssertDestroyerContract` |
| `telemetrytest` | The recorder of the kit is the subject of the suites of `Reporter`, `Tracer`, `Span`, `Counter`, `Gauge` and `Histogram`. The tracer and the span are unexported types, so the test of the recorder applies each suite through a factory that returns the interface. The checks of the release of an instrument (RFC-0046) move from `release.go` to the suites of the instruments |
| `epochtest` | `AssertFencedWriter` checks a generic `FencedSystem[H]` and no interface. Its assertions move to assert, and its exported API does not change |
| `tsptest` | Its tests move to assert, and its sentinels to `//dokimi:sentinel` |

The suites of `blob.Store` and `cas.Store` keep the laws of RFC-0038. A store
returns each object after a reopen, and a `Put` that a crash interrupts leaves the
object absent or whole.

### Applications with options

The parameter `options` of `subject` names a function of the test package of the
subject. The function returns the options of the application, and the generated
conformance test, each benchmark and each fuzz target pass them to the suite:

```go
//dokimi:subject cas.Store new=newStore options=storeOptions
type Store struct{ /* unexported */ }
```

```go
func storeOptions() []conform.Option[cas.Store] {
	return []conform.Option[cas.Store]{
		conform.Restartable(func(_ *conform.Handle[cas.Store], s cas.Store) cas.Store { return s }),
		conform.Crashable(func(_ *conform.Handle[cas.Store], s cas.Store, call func()) cas.Store {
			call()

			return s
		}),
	}
}
```

The hashes and the MACs pass `conform.Reference` with the hash of the standard
library. The generated calls of `CombineTagged` take two digests, so each hash
also passes `conform.Using` with a digest of its width. The stores pass
`conform.Restartable` and `conform.Crashable`. A store in
memory keeps its objects across a restart and a crash, as RFC-0038 states for the
in-memory references.

The factory of `subject` takes no handle, so it has no seat to report an error to.
A subject whose constructor returns an error applies its suite in a hand-written
test through `Run<X>SuiteFromHandle`. The signers of `crypto/sign`, the Keepers of
`crypto/kek` and `crypto/localkey`, and the AEADs of `crypto/aesgcm` are such
subjects. Each passes `conform.Second`: a second AEAD or Keeper of the same key, or
the Signer of the key of a verifier. A subject of an unexported type, such as the
XOF of `crypto/shake`, applies its suite in a hand-written test too.

### Toolchain

- `go.mod` moves `go.dokimi.dev/assert` from v0.3.0 to v0.10.0, adds
  `go.dokimi.dev/conform` v0.8.0, `go.dokimi.dev/stub` v0.1.1 and
  `go.dokimi.dev/fault` v0.2.0, and drops `go.thesmos.sh/testkit` in the last
  commit.
- `.dokimi.yaml` at the root sets `version: 1`, `strict: true` and the policy
  `golang.width` of `"120"`, under the SPDX header.
- `go.generate.commands` of `.ergon.yaml` runs a pinned version of dokimi before
  kanon: `[go, run, go.dokimi.dev/dokimi/cmd/dokimi@<version>, run]`.
  `verify-generate-go` then checks every generated file of dokimi on each change.
  A change of the version regenerates every file in the same commit. The version
  has the parameter `options` of `subject`, the word splitter of eidos-lang
  v0.4.0, which spells `IDs` as `ids`, and the samples of parameters in the
  generated calls. v0.2.2 has none of the three, and v0.3.0 lacks the samples of
  parameters, so core pins the commit a673413 of the main branch of dokimi.
- The `depguard` rule of tests in `.ergon/local/.golangci.yml` allows conform,
  stub and fault, and drops testkit, `testkit/model` and rapid. The exclusion of
  the callbacks of testkit's suites from `thelper` is deleted.
- A guide to dokimi in core replaces `docs/guides/testkit.md`, and
  `docs/guides/testing.md` names dokimi.
- ADR-0075 records the test-code allow-list. ADR-0015 states that a change of
  that list takes an ADR.

### Doubles of the kits

The doubles of dokimi keep the names of the doubles of testkit: `New<X>Stub`, the
fields `On<Method>` with `Returns`, `Func`, `Faults`, `FaultsFor`, `Latency` and
`Times`, the setters `With<X><Method>`, and the options `<X>StubStrict`,
`<X>StubDelegateTo` and `<X>StubWithRandSource`.

- `New<X>Stub` takes an `assert.TB`, which a `*testing.T` satisfies.
- `<X>StubWithClock` takes an `assert.Clock` in place of a `clock.Clock`.
- `SetFault` of a field `On<Method>` takes a `fault.Fault` of
  `go.dokimi.dev/fault` in place of a `testkit.Fault`.

A test of a consumer that uses those parts changes, so the changeset of each kit
is a breaking change.

### Order of the change

1. Move assert to v0.10.0. conform, stub and fault join `go.mod` in the commit
   that first imports them, because `go mod tidy` drops a module that no package
   imports.
2. Add `.dokimi.yaml` and the command of dokimi in `go.generate.commands`.
3. Move the 31 sentinel packages.
4. Move `idtest`, `randtest`, `clocktest` with its builders, and `telemetrytest`
   with its recorder, one kit for each commit.
5. Move the suites of `cryptotest` without a reference: `XOF`, `AEAD`, `Keeper`,
   `Signer`, `Verifier`, `StreamingSigner` and `StreamingVerifier`, with the
   doubles of `XOFStream`, `Destroyer`, `KeyGenerator`, `SignStream` and
   `VerifyStream`. The `depguard` rule of tests allows conform, stub and fault
   from this step, because a hand-written application passes the options of
   conform, and ADR-0075 records the list.
6. Move `Hasher`, `MAC` and `Stream` with `conform.Reference`, and `blobtest` and
   `castest` with `conform.Restartable` and `conform.Crashable`.
7. Move `epochtest` and `tsptest`.
8. Drop testkit from `go.mod`, from the lint rules and from the guides.

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
| Declarations with a directive of dokimi | 29 interfaces, 22 of them with a suite, and 2 builders |
| Package clauses with `//dokimi:sentinel` | 31 |
| Checks of the suites | 209 |
| Subjects | 28, with 76 budgets and 2 excuses |
| Generated files | 122. The kits have 71 of them: 17 files of doubles, 17 of suites, 1 of builders, the spec of each, and the sentinel test of `tsptest` |
| Hand-written files that change | 236: 92 files of the kits, 41 tests of implementations and 103 production files whose doc comments change |

### Failure handling

| Condition | Result |
|---|---|
| A generated file differs from the output of its directives | `verify-generate-go` fails |
| A check passes on its planted defect | The test of the suite fails |
| A planted defect fails another check | The test of the suite fails and lists the other check |
| A subject breaks a law of its suite without an excuse | The conformance test of the subject fails |
| A check calls `Handle.Restart`, `Handle.Crash` or `Handle.Reference` without its option | The check ends with a fault that wraps `conform.ErrOption` |
| An excuse names no check of the suite | dokimi refuses the directive with `DOKIMISUITE-0006` |

### Probe

A probe ran on a copy of core at 9371549 with assert v0.9.0, stub v0.1.1 and
fault v0.2.0:

- A build of dokimi at a92d631 with conform v0.6.0 generated the doubles and the
  suites of the 25 interfaces and the 2 builders without a finding. The generated
  tests of every kit pass once the hand-written files that still call testkit are
  set aside.
- A build of dokimi at 638dce0 with conform v0.7.0 moved `id.Generator` whole.
  Its three checks pass on `id/ulid`, and `id/constant` passes two of them and
  excuses `Generator.Generate/returns-distinct-ids`. The test of the suite
  falsifies each check.
- The same build bound `cas/memory` to the suite of `cas.Store` with
  `options=storeOptions`. Its generated conformance test, its benchmark and its
  fuzz targets pass `conform.Restartable` and `conform.Crashable` to the suite.
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
| 3 | `Test<Impl>ConformsTo<X>Allocs`, the benchmarks and the `Test<Unit>Allocs` of an unexported subject state each allocation ceiling of today |
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

**Why not:** 29 interfaces need a double, and 22 of them need a suite, a
benchmark of each method and a fuzz target of each method. dokimi generates them
from the declarations and keeps them equal to the declarations in CI.

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
by hand.

**Why not:** the parameter `options` of `subject` passes the same options to the
generated conformance test, the benchmarks and the fuzz targets. A hand-written
application writes each benchmark and each fuzz target again.

## Drawbacks

- The doc comments of 29 interfaces, 2 builders, 28 subjects and 31 package
  clauses contain directives of dokimi.
- Each law needs a planted defect. The 184 entries of the bundles become new test
  code, apart from the repeated entries and the facts of one implementation.
- A consumer whose tests configure the doubles of a kit with the parts that
  change adapts its calls.
- assert moves from v0.3.0 to v0.10.0 across ten releases. A module that requires
  core then builds against assert v0.10.0 or a later release.

## Open questions

None.

## Unresolved / future work

None.

## References

- ADR-0015, the test-code allow-list.
- ADR-0075, the runtime of dokimi in the test-code allow-list.
- RFC-0029, the `sample` directives of the tagged hashes.
- RFC-0038, the conformance of durable adapters.
- RFC-0046, released instruments.
- `docs/guides/testkit.md` and `docs/guides/testing.md`.
- dokimasia/dokimi, the Go guide,
  <https://github.com/dokimasia/dokimi/blob/main/docs/guides/go.md>.
- dokimasia/dokimi, RFC-0008, the doors of a subject and of a planted defect,
  <https://github.com/dokimasia/dokimi/blob/main/docs/rfc/0008-the-doors-of-a-subject-and-of-a-planted-defect.md>.
- dokimasia/dokimi, RFC-0009, the options of the property run of a check,
  <https://github.com/dokimasia/dokimi/blob/main/docs/rfc/0009-the-options-of-the-property-run-of-a-check.md>.
- dokimasia/eidos, RFC-0016, Layout and routing,
  <https://github.com/dokimasia/eidos/blob/main/docs/rfc/0016-layout-and-routing.md>.
- `go.dokimi.dev/conform` v0.8.0, `Restartable`, `Crashable`, `Reference` and
  `Second`, <https://pkg.go.dev/go.dokimi.dev/conform@v0.8.0>.
