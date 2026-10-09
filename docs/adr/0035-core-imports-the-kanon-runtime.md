---
adr: 0035
title: Core Imports the kanon Runtime
status: Accepted
date: 2026-09-29
supersedes: none
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0035: Core Imports the kanon Runtime

## Status

Accepted

## Context

kanon is the binary codec that thesmos projects generate for their Go
struct types. The generator of each consumer encodes a field of a core
type in the consumer's records by these rules:

- A type with its own binary, gob or text methods encodes as an opaque
  byte string through those methods. `epoch.Epoch` and `fixed.Fixed64`
  have 8-byte binary forms that ADR-0016 freezes. Measured with kanon at
  cb05b4d, each costs 10 bytes in a record, where its integer costs 2 to
  6 bytes as a varint.
- A struct of another package, such as `sign.Signature`, encodes inline.
  The consumer's generated file assigns the numbers of its fields in
  declaration order and records them. When core inserts a field, a
  consumer that first generates after the change numbers the struct
  differently from one that generated before it. A record that one of
  them writes then decodes into the wrong fields, or fails to decode, in
  the other.

kanon encodes a named type other than a struct as its underlying type
when the type has the method `ValidateKanon() error`, and its `-type`
flag generates that method in the package of the type (kanon ADR-0022).
A struct type encodes through its own codec when its package generates
one. Go allows methods only in the package that declares a type, so the
fix for both problems is code in core.

Core's production code imports only the standard library, core itself
and `golang.org/x/sync`, and its test code also testkit and rapid
(ADR-0015, the `depguard` rules of `.golangci.yml`). A generated codec
imports the kanon runtime, and its generated test imports kanon's
conformance suite. ADR-0015 states that adding a module to either list
takes a new ADR.

The runtime packages `go.thesmos.sh/kanon` and `go.thesmos.sh/kanon/wire`
import only the standard library. The conformance suite
`go.thesmos.sh/kanon/kanontest` imports `go.dokimi.dev/assert` and its
package `golden`. kanon imports nothing from core. When core generates
its code with kanon cb05b4d:

- `go.mod` gains kanon as a requirement, `go.dokimi.dev/assert` as an
  indirect requirement, and a `tool` directive for
  `go.thesmos.sh/kanon/cmd/kanon`.
- `go.sum` gains four lines, two for each of the two modules.

kanon has no release yet.

## Decision

We will let core's production code import `go.thesmos.sh/kanon` and
`go.thesmos.sh/kanon/wire`, and its test code import
`go.thesmos.sh/kanon/kanontest`, `go.dokimi.dev/assert` and
`go.dokimi.dev/assert/golden`, each listed by name in the `depguard`
allow-lists. Core then generates the kanon code of its own types, so
every consumer's record encodes a core type in the form and with the
field numbers that core fixes and tests.

The exception applies while kanon's runtime packages import only the
standard library and kanon imports nothing from core. Core requires the
kanon version whose generator wrote its code files. Until kanon tags a
release, that version is a pseudo-version of a commit on kanon's main
branch.

## Alternatives Considered

### Keep production code free of kanon

Core would write `ValidateKanon` by hand on `epoch.Epoch` and
`fixed.Fixed64`, and fix the numbers of the fields of `sign.Signature`
with kanon struct tags. Neither needs an import of kanon.

Rejected. A test could check the hand-written methods with kanon's
conformance suite, but no test in core could pin the encoding of
`Signature`, because the suite checks only a struct with a codec.
ADR-0016 requires a test with recorded bytes for every persisted layout.

### Codecs in an adapter module

A module that imports both core and kanon would provide the codecs of
core's types. The generators of consumers would call them.

Rejected. kanon cannot use the codecs that another package declares for
a type. Every consumer would also have to name the adapter, a second
place that must follow every change to core's types.

### Generated code without the runtime

kanon would generate codecs that import only the standard library.

Rejected. kanon's RFC-0002 rejected generated code of that kind:
consumers could not classify decode errors, and every generated file
would repeat about 270 lines of shared functions.

## Consequences

**Positive:**

- Core generates the kanon code of its types. Its CI checks that the
  generated files are current and that a change keeps every recorded
  field number, and the golden files of the conformance suite pin the
  encodings.
- A core type encodes the same way in the records of every project.

**Negative:**

- Core's generated files compile against runtimes whose `MinVersion` and
  `MaxVersion` include the version of the generator that wrote them. A
  kanon release that drops that version breaks core's build until core
  regenerates its codecs. kanon's RFC-0002 makes such a release a
  breaking change of its module.
- kanon cannot import core, because the import would form a cycle.
- Core regenerates its code when kanon's generator changes.
- Minimal version selection builds every consumer of core with at least
  the kanon commit that core requires.
- "The standard library and golang.org/x modules" no longer describes
  core's production imports.

**Neutral:**

- kanon's runtime packages import only the standard library, so the
  production import adds the packages of no other module to a consumer's
  build.
- Core excludes the generated files from its coverage, mutation and
  licence gates, as kanon's own repository does. kanon's golden tests
  compare the generator's output with recorded files, and the generated
  tests run the conformance suite over that output.
- The rule of ADR-0015 for golang.org/x modules is unchanged: production
  code may import such a module when its `go.mod` has no `require`
  directive and both allow-lists name it.

## References

- ADR-0015, dependency-free golang.org/x modules in production code.
- ADR-0016, persisted encodings are frozen before 1.0.
- RFC-0045, the kanon forms of core types.
- kanon RFC-0002, generated Go codecs, their runtime and their public
  interface.
- kanon ADR-0009, generated code imports a runtime.
- kanon ADR-0022, a named type other than a struct with `ValidateKanon`
  encodes as its underlying type.
- `.golangci.yml`, the `depguard` rules `main` and `test`.
