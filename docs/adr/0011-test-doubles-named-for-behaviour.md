---
adr: 0011
title: Test Doubles Are Named for Their Behaviour
status: Accepted
date: 2026-08-04
supersedes: none
superseded-by: none
---

# ADR-0011: Test Doubles Are Named for Their Behaviour

## Status

Accepted

## Context

ADR-0010 requires `id/fixed` and `rand/fixed` to give up the name
`fixed`, which RFC-0025 needs for fixed-point decimal arithmetic. That
settles what they cannot be called; it does not settle what they should
be called.

`core` already has an answer, applied consistently and never written
down. Every implementation package in the module is named for what it
does, not for who uses it:

| package | named for |
|---|---|
| `clock/fake` | virtual, programmable time |
| `clock/hlc` | hybrid logical clock |
| `rand/seeded` | deterministic seeded stream |
| `rand/pcg` | the PCG algorithm |
| `telemetry/noop` | discards every signal |
| `crypto/localkey` | keys held in process |

Not one is called `mock`, `stub`, `test`, or `testing`, including the
several whose only realistic consumer is a test. The convention holds
because these are real implementations of a production seam: `clock/fake`
drives a simulation, `crypto/localkey` is documented for local
development, and a package named `stub` would be lying about what it is
allowed to do.

Both packages being renamed already describe themselves accurately, in
prose, one line below the name that does not. `rand/fixed` opens
"provides a constant-output `rand.Rand`". `id/fixed` opens "returns the
same `id.ID` on every call". The word was found when the docs were
written; it just never reached the import path.

## Decision

`core/id/fixed` becomes `core/id/constant` and `core/rand/fixed`
becomes `core/rand/constant`. Implementation packages in `core` are
named for their behaviour, never for the fact that tests are their
principal consumer.

## Alternatives Considered

### `fake`

Rejected, and it is the alternative that would do real damage. `fake`
is taken by `clock/fake`, which is a *programmable* double — `Advance`,
`Set`, `Update`. A constant-output generator is not that, and reusing
the name for both would make `fake` denote two different degrees of
control across the module: exactly the overload ADR-0010 was written to
remove, reintroduced in the act of removing it.

### `stub`, `mock`

Rejected. Both name the role a consumer casts the package in, not what
the package does, against the convention every other implementation in
`core` follows. They also overclaim the restriction: neither package is
test-only, and `crypto/localkey` is the standing precedent for a double
that is legitimate outside tests.

### `static`

Rejected on precision. It is the nearest synonym, but in a Go context
"static" reads as compile-time or linkage — `static` linking, `static`
analysis — where the property being named is that the *value* never
varies. `constant` says that and nothing else.

### `fixture`

Rejected. It names the test artefact the package helps build rather
than the package, and `id/fixed`'s doc already uses "fixtures" for that
distinct meaning. Taking the word for the generator would collide with
the sense the docs give it.

### Move both under a `testing/` subtree

Rejected. It would put a production-linkable implementation of a
production seam behind a path that says do not ship this, which is
false for both and false for `clock/fake` and `crypto/localkey`
alongside them. It also solves a problem nobody has: the packages are
easy to find.

### Keep `fixed` and rename the decimal package instead

Rejected under ADR-0010, which weighs that trade in full. Recorded here
so this ADR is not read as having assumed its conclusion.

## Consequences

**Positive:**

- Both packages are named the thing their own doc comments already call
  them, so the import path stops disagreeing with the first line of
  documentation.
- The convention every other implementation package follows is now
  written down and can be cited in review instead of re-derived.
- `fixed` is freed for RFC-0025 with no aliasing anywhere.

**Negative:**

- Two published import paths change. Anyone who has vendored a pre-tag
  commit breaks, and the breakage is a compile error at the import,
  which is the best available form but is still breakage.
- `constant` describes the output, not the constructor: `constant.New`
  takes the value to return, so a `Generator` is constant per instance
  rather than a single fixed value module-wide. The name is right about
  the property that matters and slightly oversells the one that does
  not.
- Two vocabularies now exist for doubles — `fake` for programmable,
  `constant` for invariant. That is a real distinction and a real thing
  to learn, and someone will pick the wrong one.

**Neutral:**

- Nothing about the behaviour, API, or test coverage of either package
  changes. This is a rename and only a rename; the type names
  (`Generator`, `Rand`) and constructors keep their spellings.
