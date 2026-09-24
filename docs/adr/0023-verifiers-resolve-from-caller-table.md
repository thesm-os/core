---
adr: 0023
title: Verifiers Resolve From a Table the Caller Writes
status: Accepted
date: 2026-09-24
supersedes: none
superseded-by: none
---

# ADR-0023: Verifiers Resolve From a Table the Caller Writes

## Status

Accepted

## Context

A signature that must verify years later is stored with the name of its
algorithm and its key. An offline verifier reads the name and builds
the matching `sign.Verifier`. The name comes from storage, and an
attacker who can write to that storage chooses it.

JOSE libraries show how algorithm selection fails:

- node-jose before 0.11.0 trusted a public key embedded in a token's
  header, so an attacker could sign a token with a key of their own
  (CVE-2018-0114).
- jsonwebtoken before 4.2.2 accepted a token signed with a symmetric
  algorithm where it expected an asymmetric one (CVE-2015-9235).

RFC 8725, section 3.1, states: "Libraries MUST enable the caller to
specify a supported set of algorithms and MUST NOT use any other
algorithms when performing cryptographic operations."

Go's `database/sql` and `image` packages use the other common design.
A driver or format registers itself in `init`, and every imported one
is available everywhere in the process.

## Decision

We will build a verifier from a stored algorithm name only through a
`sign.Resolver` that the caller writes from the algorithms it trusts,
with no global registry and no default entries, because the set of
accepted algorithms is a security decision that belongs at the call
site.

A name missing from the table, or an entry that builds a verifier of
another algorithm, returns `sign.ErrUnknownAlgorithm`, which classifies
as `errs.Unsupported`. A `Resolver` is applied only to key material the
caller trusts, never to a key embedded in the artifact being verified.

## Alternatives Considered

### A global registry

Algorithm packages would register their constructors in `init`, as
`database/sql` drivers and `image` formats do.

Rejected. A registry makes every imported algorithm acceptable
everywhere in the process, including to a verifier that should accept
one. The accepted set would be decided by the import graph instead of
by the code that verifies.

### Leave the mapping to each caller

Core would ship the constructors and no `Resolver`, and each caller
would write its own map from names to constructors.

Rejected. Each caller would also decide what an unknown name means,
and a caller that treats an unknown name as a pass accepts a signature
from an algorithm it does not know.

## Consequences

**Positive:**

- An attacker who controls a stored name selects only among the
  algorithms the caller listed.
- An unknown name fails with a classified error, never with a valid
  result.
- Each algorithm package provides its entry: `ed25519.Resolve`,
  `ecdsap384.Resolve` and `mldsa.Resolver`. A `Resolver` is one map
  literal.

**Negative:**

- A caller must list every algorithm its stored signatures use. A
  missing entry fails verification of old signatures, by design.
- `mldsa.Resolver` binds a parameter set and a context string, so one
  `Resolver` verifies ML-DSA signatures under one context per algorithm
  name. A caller with two contexts builds two `Resolver` values.

**Neutral:**

- A `Resolver` is a map, and it is safe for concurrent use once built,
  as any map that is only read.

## References

- RFC-0039, signature policies and verifier resolution.
- RFC-0013, the signing seam.
- RFC 8725, "JSON Web Token Best Current Practices", section 3.1,
  <https://www.rfc-editor.org/rfc/rfc8725.html>.
- CVE-2018-0114, node-jose.
- CVE-2015-9235, jsonwebtoken.
