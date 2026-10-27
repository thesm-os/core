---
adr: 0075
title: Test Code Imports the Runtime of dokimi
status: Accepted
date: 2026-10-27
supersedes: none
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0075: Test Code Imports the Runtime of dokimi

## Status

Accepted

## Context

The test code of core and the kits of `coretest` import a closed list of
modules. The `depguard` rule `test` of `.golangci.yml` names each module:

- the standard library, core and `golang.org/x/sync`
- `go.thesmos.sh/testkit`, its package `model` and `pgregory.net/rapid`
- `go.thesmos.sh/kanon` and `go.dokimi.dev/assert`

ADR-0015 states that adding a module to either list takes a new ADR.

RFC-0080 moves the generated tests of core from testkit to dokimi. The code
that dokimi generates imports three modules of its runtime:

- `go.dokimi.dev/conform` runs the suites, the budgets, the benchmarks and the
  fuzz targets. A test that applies a suite by hand passes the options of
  conform, such as `conform.Second` for a second AEAD of the same key.
- `go.dokimi.dev/stub` records the calls of the doubles of the kits.
- `go.dokimi.dev/fault` arms the faults of the doubles.

Dokimasia B.V. licenses each module under MIT. Each module imports the
standard library and `go.dokimi.dev/assert`, and stub also imports fault.
conform v0.8.0 requires assert v0.10.0. The modules stub v0.1.1 and fault v0.2.0
require assert v0.6.0. Core requires assert v0.10.0.

No test of core imports rapid. rapid reaches the module graph of core through
`testkit/model` alone.

## Decision

We will let the test code of core and the kits of `coretest` import
`go.dokimi.dev/conform`, `go.dokimi.dev/stub` and `go.dokimi.dev/fault`. The
`depguard` rule `test` names each module. The rule drops
`go.thesmos.sh/testkit`, `go.thesmos.sh/testkit/model` and
`pgregory.net/rapid` in the change that removes the last import of testkit.

## Alternatives Considered

### Suites and doubles on assert alone

The kits would declare their suites and doubles without conform and stub. The
test code would import assert alone.

Rejected. RFC-0080 rejects this design. 29 interfaces need a double, and 22 of
them need a suite, a benchmark of each method and a fuzz target of each method.
dokimi generates them from the declarations, and CI checks them against the
declarations.

### testkit beside dokimi

The kits that RFC-0080 has not moved yet would keep testkit, and the list would
keep its entries.

Rejected. RFC-0080 rejects two test toolchains in one module. The list keeps
testkit only until the last kit moves.

## Consequences

**Positive:**

- The generated suites and doubles of the kits compile under the rule `test`.
- A test applies a suite with the options of conform, such as a second
  subject, a reference or a restart.
- The rule `test` loses testkit, its package `model` and rapid.

**Negative:**

- A module that imports a kit of `coretest` builds conform, stub and fault.
- A release of dokimi can change the API that its generated files call. Core
  then regenerates its files with the release and requires its runtime in the
  same change.

**Neutral:**

- Production code imports none of the three modules. The rule `main` does not
  change.
- The rules of ADR-0015 for the golang.org/x modules and of ADR-0035 for kanon
  do not change.

## References

- ADR-0015, dependency-free golang.org/x modules in production code.
- ADR-0035, core imports the kanon runtime.
- RFC-0080, test generation with dokimi.
- `go.dokimi.dev/conform` v0.8.0, `go.dokimi.dev/stub` v0.1.1 and
  `go.dokimi.dev/fault` v0.2.0: `go.mod` and `LICENSE`.
- `.golangci.yml`, the `depguard` rules `main` and `test`.
