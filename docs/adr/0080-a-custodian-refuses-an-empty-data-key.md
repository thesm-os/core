---
adr: 0080
title: A Custodian Refuses an Empty Data Key
status: Accepted
date: 2026-11-07
supersedes: none
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0080: A Custodian Refuses an Empty Data Key

## Status

Accepted

## Context

`crypto.Keeper.Wrap` and `crypto.AADKeeper.WrapAAD` state no rule for an
empty data key. The suite of `coretest/cryptotest` required a Keeper to
wrap an empty data key and to unwrap it to an empty key, and its planted
defect was a Wrap that returned `crypto.ErrKeySize`.

Key-wrap standards and hosted custodians refuse an empty key:

- AES Key Wrap with Padding wraps "between one and 2^32 octets" (RFC 5649,
  section 4.1). AES Key Wrap takes two or more blocks of 64 bits (RFC
  3394).
- AWS KMS refuses a plaintext of fewer than 1 byte in Encrypt.
- Cloud KMS documents the plaintext of Encrypt as required, and AIP-203
  defines a required field as present and not empty.

A Keeper over AWS KMS passed the suite only with a format of its own, which
encrypts one byte before the data key. core itself never produces an empty
data key: `GenerateKey` and `GenerateKeyAAD` return `ErrKeySize` for a size
that is not positive.

## Decision

We will make `Wrap` and `WrapAAD` return `crypto.ErrKeySize` for an empty
data key, because no key-wrap algorithm and no hosted custodian accepts
one.

- `localkey.Keeper` and `kek.Keeper` refuse an empty data key before they
  seal it.
- The check of an empty key in the suite of `crypto.Keeper` requires that
  Wrap of a nil and of an empty data key returns an error that wraps
  `ErrKeySize`. Its planted defect seals an empty data key, as AES-GCM
  seals an empty plaintext.
- `AssertAADKeeperContract` requires the same of WrapAAD.

## Alternatives Considered

### The round trip of an empty key

The suite keeps its check that an empty data key round-trips.

Rejected. A Keeper over AWS KMS or over AES Key Wrap needs a format of its
own to pass it, and the formats of Tink and of gocloud.dev send the data
key to AWS KMS unchanged. Each application would otherwise excuse the
check.

### No rule for an empty key

The suite drops the check, and each custodian decides.

Rejected. Code that wraps an empty key would then work on `localkey` and
fail on AWS KMS, and the contract would not state which result to expect.

## Consequences

**Positive:**

- A Keeper over AWS KMS, Cloud KMS or AES Key Wrap passes the suite and
  sends the data key unchanged.
- `localkey`, `kek` and each custodian that passes the suite return
  `ErrKeySize` for an empty data key, which classifies as `errs.Invalid`.

**Negative:**

- A caller that wraps an empty data key receives `ErrKeySize` from every
  custodian.
- Every Keeper outside core adds the refusal, or it fails the suite.

**Neutral:**

- `Unwrap` does not change. Material that a custodian wrapped from an
  empty key unwraps as before.

## References

- Issue 123, <https://github.com/thesm-os/core/issues/123>.
- RFC 5649, "Advanced Encryption Standard (AES) Key Wrap with Padding
  Algorithm", section 4.1, <https://www.rfc-editor.org/rfc/rfc5649#section-4.1>.
- RFC 3394, "Advanced Encryption Standard (AES) Key Wrap Algorithm",
  <https://www.rfc-editor.org/rfc/rfc3394>.
- AWS KMS API reference, Encrypt, the length constraints of `Plaintext`,
  <https://docs.aws.amazon.com/kms/latest/APIReference/API_Encrypt.html>.
- AIP-203, field behavior documentation, <https://google.aip.dev/203>.
