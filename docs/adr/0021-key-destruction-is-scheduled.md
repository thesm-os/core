---
adr: 0021
title: Key Destruction Is Scheduled
status: Accepted
date: 2026-09-24
supersedes: none
superseded-by: none
---

# ADR-0021: Key Destruction Is Scheduled

## Status

Accepted

## Context

`crypto.Destroyer` is the optional capability of a key custodian that
can destroy a wrapping key. Destroying the wrapping key erases every
data key wrapped under it, and so every payload those data keys
encrypt. The seam's `Destroy` method returned only an error and
promised to destroy the key irreversibly before it returned.

Hosted custodians destroy a key in two steps:

- AWS KMS `ScheduleKeyDeletion` waits 7 to 30 days, 30 by default.
  From the call on, the key is `PendingDeletion` and "can't be used in
  any cryptographic operations". `CancelKeyDeletion` restores it until
  the wait ends. The response contains the `DeletionDate`, except for a
  multi-Region primary key with replicas, whose date is not known until
  its last replica is deleted.
- Cloud KMS keeps a destroyed key version scheduled for destruction for
  30 days by default. In that state it "can't be used for cryptographic
  operations", and it can be restored.

An implementation over either service could not honour "irreversibly"
when `Destroy` returned. A caller that signs an attestation of erasure
needs the time at which the destruction becomes irreversible. NIST SP
800-88 calls this technique cryptographic erase.

## Decision

We will make `Destroy` schedule the destruction of a wrapping key and
return the time at which it becomes irreversible, because hosted
custodians destroy in two steps and an attestation of erasure has to
record when the second step completes.

`Unwrap` under the key fails from the moment `Destroy` returns. The
zero time means the custodian cannot yet report when. `Destroy` is
idempotent. A key the custodian does not have returns
`crypto.ErrKeyID`.

## Alternatives Considered

### Keep the signature and document scheduling

`Destroy` would keep returning only an error. Its documentation would
state that destruction may be scheduled.

Rejected. An implementation over a hosted service would have the
irreversibility time and no way to return it. An attestation would
have no correct time to record.

### A second capability

A new `ScheduledDestroyer` interface would return the time, and
`Destroyer` would keep its contract.

Rejected. `Destroyer`'s contract would remain untrue for every hosted
custodian. Every caller would assert for two interfaces. Core had one
implementation of `Destroyer`, and changing the interface cost less
than keeping two.

### One wrapping key per erasure unit

`Destroyer` would gain `Create`. Each stream, subject or period would
have its own wrapping key in the custodian.

Rejected. AWS KMS allows 100,000 customer managed keys per account and
Region by default and bills per key, so a unit finer than a tenant
exceeds the quota at modest scale. A unit finer than a tenant has its
own data key instead, and deleting the wrapped data key erases the
unit without the custodian.

## Consequences

**Positive:**

- An implementation over AWS KMS or Cloud KMS meets the contract.
- An attestation can record both times: when the key stopped
  unwrapping and when its destruction became irreversible.
- A caller can retry `Destroy` after a failed call without losing the
  time.

**Negative:**

- The signature change breaks every implementation of `Destroyer`
  outside core. `crypto/localkey.New` now takes a `clock.Clock`, which
  supplies the time its `Destroy` returns.
- A caller handles two times for one erasure, and an attestation that
  records only one of them is either early or late.
- Erasure by deleting a wrapped data key is complete only when every
  backup that contains the wrapped key has expired, so the caller has
  to bound its backup retention.

**Neutral:**

- The seam still has no `Create`. Creating a wrapping key is a
  provisioning task for the place where the custodian's keys and
  permissions are managed.

## References

- RFC-0034, scheduled key destruction.
- RFC-0018 and ADR-0008, key custody and contracts that describe IO.
- AWS KMS API reference, `ScheduleKeyDeletion`,
  <https://docs.aws.amazon.com/kms/latest/APIReference/API_ScheduleKeyDeletion.html>.
- AWS KMS developer guide, resource quotas,
  <https://docs.aws.amazon.com/kms/latest/developerguide/resource-limits.html>.
- Google Cloud KMS, key version states,
  <https://docs.cloud.google.com/kms/docs/key-states>.
- NIST SP 800-88 Rev. 2, "Guidelines for Media Sanitization".
