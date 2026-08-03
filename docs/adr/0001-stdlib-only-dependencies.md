---
adr: 0001
title: Stdlib-Only Dependencies
status: Superseded
date: 2026-05-06
supersedes: none
superseded-by: ADR-0006
---

# ADR-0001: Stdlib-Only Dependencies

## Status

Superseded by
[ADR-0006: Stdlib-Only Scope: Test Dependencies](0006-stdlib-only-scope-test-dependencies.md).

The production constraint recorded below is unchanged and remains in
force. ADR-0006 revises only its scope over test code, where module
graph pruning keeps a test-only requirement out of a consumer's build.

## Context

`core` defines the contract seams that every other thesmos library imports.
Whatever `core` depends on, every consumer transitively depends on.

A foundation library that drags in non-stdlib packages locks every
downstream consumer into:

- whatever Go-version floor those packages mandate;
- their CVE exposure surface;
- their breaking-change cadence;
- their licensing terms.

For interface-only contract code, this trade is rarely worth making.
Implementations that need OpenTelemetry, Prometheus, UUID libraries,
crypto frameworks, or HTTP clients live in consumer repos, where the
dependency cost is local and opt-in.

## Decision

`core` imports nothing outside the Go standard library and itself.
Enforcement is mechanical: `golangci-lint` runs `depguard` in strict
mode with a single allow-list:

```yaml
depguard:
  rules:
    main:
      list-mode: strict
      files:
        - "$all"
      allow:
        - $gostd
        - go.thesmos.sh/core
```

Any import outside `$gostd` or `go.thesmos.sh/core` fails CI
immediately, including in test files.

## Alternatives Considered

### Allow a small curated set of well-known deps (e.g. `google/uuid`, `pkg/errors`)

Rejected. There is no principled stopping point — every "small" allow-
list grows. Once one exception exists, every author feels entitled to
their own. The hard constraint ("only stdlib") is the cheapest line to
defend.

### Move the constraint to documentation only

Rejected. Documentation drift is real; CI enforcement is not. Without
the depguard gate, contributors well-meaningly add a transitive
dependency that takes another PR cycle to remove.

### Allow non-stdlib only in test files

Rejected. Test code in `core` is part of the published module surface
(consumers may write their own tests against `core` interfaces and
cargo-cult our patterns). The constraint is uniform.

## Consequences

**Positive:**

- Zero supply-chain risk introduced by `core` itself.
- Every consumer can safely vendor `core` without inheriting other
  dependencies.
- `go.sum` stays empty (or close to it) — the module is auditable at
  a glance.
- Forces interface-first design: implementations with non-stdlib
  needs land in consumer repos where they belong.

**Negative:**

- Some interfaces will be slightly less ergonomic than they could be
  with a helper library. Acceptable trade.
- Generated test scaffolding cannot use `go-cmp` or `testify`
  directly inside `core`. Consumers can; `core`'s own tests use
  `reflect.DeepEqual` / hand-rolled comparison.
- `Reporter` cannot reference OpenTelemetry types — it defines its
  own minimal value types (`AttrSet`, `AttrPair`). Adapters in
  consumer repos translate to/from OTel.

**Neutral:**

- `dependabot.yml` only watches `github-actions`; there are no Go
  modules to update.
