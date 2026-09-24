---
adr: 0020
title: Core Requires Go 1.27
status: Accepted
date: 2026-09-24
supersedes: none
superseded-by: none
---

# ADR-0020: Core Requires Go 1.27

## Status

Accepted

## Context

Go 1.27.0, released on 19 August 2026, adds `crypto/mldsa`, the ML-DSA
signature scheme of FIPS 204. Core's `crypto/sign/mldsa` package wraps
it.

Core is one module, and a module's `go` directive sets the minimum Go
version for every package in it. Before this decision the directive
was `go 1.26.6`.

Every CI workflow reads its toolchain from `go.mod` through
`go-version-file`, so CI builds and tests the declared minimum.

The Go project supports each major release "until there are two newer
major releases". Go 1.26 is supported until Go 1.28 is released.

## Decision

We will declare `go 1.27.0` as core's minimum Go version, because
`crypto/mldsa` exists from that release and a single module cannot give
one package a higher minimum than the others.

The directive names 1.27.0 rather than 1.27.1. Go 1.27.1 contains no
security fixes.

## Alternatives Considered

### Keep Go 1.26 and put build constraints on the ML-DSA package

Every file of `crypto/sign/mldsa` would start with `//go:build go1.27`.
The rest of core would keep its Go 1.26 minimum.

Rejected. The constraint has to be on every file of the package,
generated tests included, and testkit's sentinel generator writes its
file without one, so the package's tests would fail to build on Go
1.26. A caller on Go 1.26 who imports the package gets a build error
with or without the constraint.

## Consequences

**Positive:**

- Every package in core can use the Go 1.27 standard library.
- CI tests the declared minimum, because it takes the toolchain from
  the directive.

**Negative:**

- Every consumer of core needs Go 1.27, including one that never signs
  with ML-DSA, while Go 1.26 is still supported upstream.
- The raise is a breaking change, and the changelog records it as one.

**Neutral:**

- Every raise of the minimum follows the same rule: the minimum is the
  lowest release that has every standard-library API core uses.

## References

- RFC-0033, ML-DSA signatures.
- NIST FIPS 204, "Module-Lattice-Based Digital Signature Standard".
- The Go project, release history and policy,
  <https://go.dev/doc/devel/release>.
- `.github/workflows/ci.yml`, `release.yml` and `govulncheck.yml`.
- `go.thesmos.sh/testkit` v0.10.0, `gen/render.go`.
