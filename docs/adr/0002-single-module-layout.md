---
adr: 0002
title: Single Module Layout
status: Accepted
date: 2026-05-06
supersedes: none
superseded-by: none
---

# ADR-0002: Single Module Layout

## Status

Accepted

## Context

Per [ADR-0001](0001-stdlib-only-dependencies.md), nothing in `core`
depends on anything outside the standard library, so there is nothing
to isolate. Splitting `core` into submodules would add tooling overhead
(per-module `go.mod`, foreach-module Make targets, separate `tidy` runs,
version coupling between modules) for no benefit.

## Decision

`core` is a single Go module rooted at `go.thesmos.sh/core`. There is
exactly one `go.mod` at the repository root. Future packages are added as
top-level subdirectories under that single module.

## Alternatives Considered

### Mirror testkit's multi-module layout

One submodule per logical package. Rejected: multi-module is a tool
for dependency isolation, and `core` has no dependencies to isolate.
The tax (separate `go.mod` files, per-module tidy, cross-module
version drift, foreach loops in the Makefile) is real and produces no
corresponding benefit.

### Single module + a `cmd/` submodule for future tooling

Rejected for now. `core` does not ship binaries; if a future codegen
or lint tool is needed, splitting `cmd/` out is a small additive
change (one new `go.mod`). Reserving the slot today means writing the
multi-module Makefile plumbing to never use it.

### Per-package internal modules under `internal/`

Rejected: `internal/` is for unexported code. `core`'s value is its
exported interfaces; everything user-facing must be importable. There
is little internal code worth hiding in a contract library.

## Consequences

**Positive:**

- One `go.mod`, one `go.sum`, one `go mod tidy` — the simplest
  shape Go supports.
- Consumers do `go get go.thesmos.sh/core` and pull the entire
  module — no surprise sub-module versions to coordinate.
- The Makefile is short: no `MODULES` list, no `foreach_module` loop.

**Negative:**

- If a future contributor needs to add a non-stdlib dependency for a
  single subpackage, they cannot cordon it off in a sibling module.
  In that case, the subpackage probably doesn't belong in `core`
  (see ADR-0001) — push it to a consumer repo instead.

**Neutral:**

- All packages share the same Go directive (`go 1.26.2` initially);
  bumping it affects every consumer simultaneously. This is
  desirable: `core` is the contract floor.
