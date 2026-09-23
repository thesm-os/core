---
adr: 0015
title: Dependency-Free golang.org/x Modules in Production Code
status: Accepted
date: 2026-09-23
supersedes: ADR-0006
superseded-by: none
---

# ADR-0015: Dependency-Free golang.org/x Modules in Production Code

## Status

Accepted

## Context

Production code in core imports only the Go standard library and core
itself. `depguard` enforces the rule in strict mode. Test code may
import a closed list: the module's testing toolkit and a
property-testing library.

The rule treats every module outside the standard library alike. The
design of structured concurrency showed what that costs.
`golang.org/x/sync/errgroup` is the prior art for a group of goroutines
with a first error and a limit, and x/sync also carries `semaphore`
and `singleflight`. Under the rule, core could read these packages
but not import them.

The golang.org/x modules differ from third-party modules in who
maintains them:

- The Go project develops them outside the main Go tree. They are
  "developed under looser compatibility requirements than the Go core"
  and "will support the previous two releases and tip".
- Changes go through the Go project's Gerrit review, and issues go to
  the golang/go tracker.
- x/sync uses the same BSD-style licence as Go, copyright The Go
  Authors.

x/sync has no module requirements. Its `go.mod` at v0.23.0 declares
the module path and `go 1.26.0` and nothing else, so importing it adds
one module to a consumer's build and no transitive ones. Core's module
graph already contains x/sync v0.20.0, which testkit v0.10.0 requires,
and `go.sum` has no x/sync entry.

The stdlib-only rule rejected a curated list of exceptions because
such a list has no stopping point. A module's `go.mod` either has a
`require` directive or it does not. "A golang.org/x module without
requirements" is a line that a reviewer can check in one file.

## Decision

We will let production code import a golang.org/x module whose
`go.mod` has no `require` directive, once the module is listed by name
in both `depguard` allow-lists, because the Go project maintains these
modules and they do not add a transitive dependency to a consumer's
build.

## Alternatives Considered

### Keep production code stdlib-only

Production code would write its own version of what it needs from
x/sync, as core already writes its own pool, bulkhead and batch
loader.

Rejected. The stdlib-only rule names four costs of a dependency: its
Go-version floor, its vulnerability exposure, its breaking-change
cadence and its licence. A module that the Go project maintains, under
the Go licence and without requirements, has the same licence and the
same maintainers as the standard library, and it does not bring other
modules with it. The Go-version floor and the looser compatibility
remain, and core controls both by choosing when to upgrade. We
treat the Go project's own modules as part of the platform, not as
third-party code.

## Consequences

**Positive:**

- Core can import `errgroup`, `semaphore` and `singleflight` where a
  design needs them, without copying their code.
- The exception is mechanical to check. Every allowed module appears
  by name in both `depguard` allow-lists, and its `go.mod` has no
  `require` directive.

**Negative:**

- Once a core package imports x/sync, every consumer of that package
  builds x/sync. Minimal version selection can raise the x/sync
  version that a consumer builds with.
- x/sync is developed under looser compatibility requirements than the
  standard library. A release can change an API that core uses, so
  core upgrades x/sync deliberately and runs its full gate on the
  upgrade.
- x/sync raises its `go` directive with Go releases: `go 1.18` at
  v0.11.0, `go 1.25.0` at v0.20.0 and `go 1.26.0` at v0.23.0. An
  upgrade can raise the minimum Go version of core's consumers.
- "Stdlib-only" no longer describes core's production imports.
  Contributors must know the named exceptions, and a reviewer must read
  the `go.mod` of any module proposed for the list.
- Core's `go.sum` gains the x/sync entries once a package imports it.

**Neutral:**

- When this decision was taken, x/sync reached core only through
  testkit, a test dependency. The structured-concurrency design does
  not need it.
- The test-code allow-list keeps its other entries. Adding a module to
  either list takes a new ADR.
- `depguard` stays in strict mode.

## References

- The Go project, "X Repositories",
  <https://go.dev/wiki/X-Repositories>.
- `golang.org/x/sync` at v0.11.0, v0.20.0 and v0.23.0: `go.mod`,
  `LICENSE` and `README.md`.
- ADR-0001, the original stdlib-only rule.
- ADR-0006, the rule this decision supersedes.
- RFC-0030, the structured-concurrency design that raised the
  question.
