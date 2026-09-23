---
adr: 0016
title: Persisted Encodings Are Frozen Before 1.0
status: Accepted
date: 2026-09-24
supersedes: none
superseded-by: none
---

# ADR-0016: Persisted Encodings Are Frozen Before 1.0

## Status

Accepted

## Context

Core defines byte layouts that its consumers persist and sign. A
consumer that signs one of these values today needs a verifier years
later to rebuild the same bytes.

| Layout | Bytes |
|---|---|
| `crypto.Digest` binary form | the active digest bytes, with no length header |
| `clock.Instant` binary form | 16 bytes: wall `int64`, logical `uint32`, node `uint32`, big-endian |
| `epoch.Epoch` binary form | 8 bytes, big-endian |
| `fixed.Fixed64` binary form | 8 bytes, big-endian two's complement |
| `id.ID` bytes | the identifier's bytes, with the size given by their length |
| `crypto.Framer` | domain name with a `uint64` length prefix, `uint16` version, `uint64`-prefixed variable fields, verbatim fixed fields, big-endian integers |
| `crypto.HashDomain` | domain bytes, then each part after a big-endian `uint64` length |
| Tagged hashing | `H(role ‖ data)` and `H(role ‖ left ‖ right)`, with the role's high bit giving its arity |
| Sealed AEAD envelope | version `1`, algorithm length, algorithm name, nonce, ciphertext and tag, with the associated data framed under the domain `thesmos.crypto.aead` version 1 |
| `crypto.Algorithm` names | the constant strings, such as `sha-256` and `aes-256-gcm` |
| `sign.KeyID` derivations | the first 16 bytes of SHA-256 over the public key's encoding |

Only three of these layouts state that they are stable, and each
promises stability "within a major version": `Instant`
(`clock/instant.go:85`), `Epoch` (`epoch/binary.go:14`) and
`Fixed64` (`fixed/binary.go:13`). The design record for the digest
and instant encodings uses the same words.

Core's releases are `v0.5.0` and `v0.6.1`. Rule 4 of semantic
versioning states for major version zero: "Anything MAY change at any
time." A promise of stability within a major version is therefore no
promise at all before 1.0, and the other layouts in the table make
none.

Every layout in the table already has a test that compares it with
recorded bytes, apart from the sealed envelope. This decision adds
that test.

## Decision

We will treat every byte layout in the preceding table as frozen from
the release that first contains it, independent of core's major version,
because a value a consumer signs or persists must verify later and
semantic versioning promises nothing before 1.0.

## Alternatives Considered

### Wait for 1.0

Semantic versioning would then make the layouts stable within version
1, with no separate decision.

Rejected. Core has not set a date for 1.0. A consumer persists and
signs these values now, and a layout that changes before 1.0 breaks
every signature over it.

### Keep the per-type statements

Each layout would keep or gain a sentence promising stability within a
major version, as `Instant`, `Epoch` and `Fixed64` have.

Rejected. The statement promises nothing at version 0. It is written
in the documentation of each type, so nothing enumerates the set a
change has to be checked against.

## Consequences

**Positive:**

- Consumers can persist and sign core values before 1.0.
- The frozen set is enumerated in one place. Each layout has a test
  that compares it with recorded bytes, so a change fails the gate.

**Negative:**

- A mistake in a frozen layout cannot be corrected in place. The
  correction is a new version: an envelope version 2, a new domain
  version, a new role, or a new function. Readers of the old version
  remain in core.
- A new persisted layout is frozen from its first release, so its
  design has to fix its bytes and record them in a test before its
  first release.
- A contributor changing a file that implements a listed layout must
  check the change against the table.

**Neutral:**

- The Go API around the layouts can still change before 1.0. Only the
  bytes are frozen.
- `crypto.ID` is a build-local identifier and is not persisted, so it
  is not in the table.

## References

- Semantic Versioning 2.0.0, rule 4,
  <https://semver.org/spec/v2.0.0.html>.
- RFC-0014, binary encoding for core value types.
- RFC-0016, framed domain separation.
- RFC-0017, authenticated encryption.
- RFC-0029, domain-separated tree hashing.
