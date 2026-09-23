---
rfc: 0034
title: Scheduled Key Destruction
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Draft
created: 2026-09-24
updated: 2026-09-24
discussion: none
supersedes: none
superseded-by: none
produces-adr: tbd
---

# RFC-0034: Scheduled Key Destruction

## Summary

We propose to change `crypto.Destroyer` so that its contract matches
what key management services do. `Destroy` schedules the destruction of
a wrapping key and returns the time at which it becomes irreversible.
From the moment `Destroy` returns, the custodian refuses to unwrap
under that key. `Destroy` is idempotent: a key already scheduled or
destroyed returns the time it was given. The RFC also records how to
erase data at the grain of a stream or a tenant with this seam, which
needs no further interface.

## Motivation

### The contract does not describe any hosted custodian

`crypto.Destroyer.Destroy` "irreversibly destroys the named wrapping
key", after which "material wrapped under it no longer unwraps, by this
or any other instance" (`crypto/keeper.go`). Hosted services destroy a
key in two steps:

- AWS KMS `ScheduleKeyDeletion` waits between 7 and 30 days, 30 by
  default. From the call on, the key is `PendingDeletion` and "can't be
  used in any cryptographic operations". `CancelKeyDeletion` restores
  it until the wait ends, and the response includes the `DeletionDate`.
- Google Cloud KMS keeps a destroyed key version in the
  `DESTROY_SCHEDULED` state for 30 days by default. In that state "it
  can't be used for cryptographic operations", and it can be restored.
  After the period, "any ciphertext encrypted with this version is not
  recoverable".

An implementation of `Destroyer` over either service cannot meet the
word "irreversibly" when `Destroy` returns. It can meet "no longer
unwraps", and it knows the time at which the destruction becomes
irreversible. A caller that signs an attestation of erasure needs that
time. An attestation stamped with the time of the call claims an
erasure that an administrator can still cancel.

### Erasure at a fine grain does not need one key per unit

A caller that must erase one stream's data, or one tenant's, can map
each erasure unit to its own wrapping key. Hosted services limit keys:
AWS KMS allows 100,000 customer managed keys per account and region by
default. A key per stream exceeds that at modest scale, and a key per
tenant per day exceeds it within months at a thousand tenants. The
envelope encryption `crypto.Keeper` already models supports a finer
grain without more keys, and core's documentation does not say how.

## Detailed design

### The seam

```go
// Destroyer is the optional capability for custodians that can
// destroy a wrapping key: the primitive underneath erasure of
// encrypted-at-rest data. The payload remains in place and becomes
// unreadable, which makes erasure tractable for data that is
// replicated, backed up, or on tape.
//
// Callers requiring erasure assert for Destroyer at wiring time and
// fail fast when the assertion fails.
type Destroyer interface {
    Keeper

    // Destroy schedules the named wrapping key for destruction and
    // returns the time at which the destruction becomes irreversible.
    //
    // From the moment Destroy returns, Unwrap under keyID fails with
    // ErrKeyDestroyed, on this and every other instance. Before the
    // returned time an administrator of the custodian may still be
    // able to cancel the destruction. A custodian that destroys at
    // once returns the time of the call.
    //
    // The zero time means the destruction is scheduled and the
    // custodian cannot yet say when it becomes irreversible. AWS KMS
    // returns no deletion date for a multi-Region primary key while
    // its replicas remain. The caller calls Destroy again later.
    //
    // Destroy is idempotent. For a key already scheduled or
    // destroyed, it returns the time already set, or the zero time
    // while that is still unknown, and a nil error. A caller can retry
    // after a failed call without losing the time.
    //
    // Destroying a key the custodian does not have returns ErrKeyID:
    // succeeding would leave a caller believing data was erased when
    // it was not.
    Destroy(ctx context.Context, keyID string) (time.Time, error)
}
```

The change breaks every implementation of `Destroyer`. Core has one,
`crypto/localkey`, plus the generated stubs in `coretest/cryptotest`.
`localkey` destroys at once. It records the time of the first call and
returns it on every later call, where a second call returns
`ErrKeyDestroyed` today.

A caller that attests to an erasure records the returned time as the
moment the erasure is complete, and treats the erasure as pending
until then, or until it learns the time when `Destroy` returned zero.
The attestation can name both times: when the key stopped unwrapping
and when the destruction became irreversible. NIST SP 800-88 names the
technique cryptographic erase: sanitisation by erasing the keys that
encrypt the data, not the media that stores it. Revision 2 of
September 2025 keeps the term.

### Erasure at the grain of a unit

Core's documentation of `crypto.Keeper` gains a section on erasure. It
describes two layers, and neither needs a new interface:

1. **Data keys.** Each erasure unit, such as a stream, a subject or a
   period, has its own data key, from `crypto.GenerateKey`. The data
   key encrypts the unit's payloads through `crypto.AEAD`, and its
   wrapped form is stored as one object, for example in a
   `blob.Store`. Erasing the unit deletes that object with
   `blob.Store.Delete`. Copies of the object in backups remain
   readable until the backups expire, so the erasure is complete when
   the last backup that contains the object has expired.
2. **Wrapping keys.** A tenant, or another boundary of trust, has one
   long-lived wrapping key in the custodian, which the custodian
   rotates. Destroying it with `Destroy` erases every data key wrapped
   under it, backups included, once the destruction is irreversible.
   Destroying a wrapping key is the tool for removing a whole tenant,
   and a bound on how long a deleted data key can survive in a
   backup.

Creating a wrapping key is a provisioning task, done where the
custodian's keys and permissions are managed. The seam therefore has no
`Create`.

### Isolation between tenants

A caller that serves several tenants from one process builds one
`Keeper` per tenant, each bound to that tenant's wrapping key, and
passes each component only the `Keeper` of its tenant, when the
component is built. The component then has no way to name another
tenant's key. The custodian's access policy enforces the same
separation for the credentials the process has.

A `Keeper` that prefixes key identifiers gives no isolation of its
own: `Wrap` and `Unwrap` name no key, and a process that has
credentials for every key can build any prefix.

### Tests

- The `Destroyer` conformance suite asserts that `Unwrap` fails with
  `ErrKeyDestroyed` after `Destroy` returns, that a second `Destroy`
  returns the first call's time, and that an unknown key returns
  `ErrKeyID`.
- A fake custodian in the tests schedules destruction with a delay. It
  returns the zero time first, then a time in the future, and
  `localkey` returns the time of the call, so the suite covers all
  three.

## Alternatives considered

### A. Keep the signature and document scheduling

`Destroy` would keep returning only an error, and its documentation
would say that destruction may be scheduled.

**Why not:** the caller would still not know when the erasure becomes
irreversible, and an attestation would have no correct time to record.
An implementation over a hosted service has that time and would have
no way to return it.

### B. A second capability

A new `ScheduledDestroyer` interface would return the time.
`Destroyer` would keep its contract.

**Why not:** `Destroyer`'s contract would remain untrue for every
hosted custodian. Every caller would also type-assert for both
interfaces. Core has one implementation of `Destroyer`, so changing the
seam costs less than keeping two.

### C. One wrapping key per erasure unit

The seam would gain `Create`, and every stream or period would have its
own wrapping key in the custodian.

**Why not:** hosted services limit the number of keys, 100,000 per
account and region by default on AWS KMS, and bill per key. A unit
finer than a tenant exceeds the limit at modest scale. Deleting the
wrapped data key erases a unit at any grain without the custodian.

## Drawbacks

- The signature change breaks every implementation of `Destroyer`
  outside core.
- Erasure by deleting a wrapped data key is complete only when every
  backup that contains it has expired, so a caller has to bound its
  backup retention and state that bound.
- A caller now handles two times for one erasure, and an attestation
  that records only one of them is either early or late.

## Open questions

None.

## Unresolved / future work

- Batch `Unwrap`, if a custodian protocol supports it.
- A caching wrapper for unwrapped data keys. Its bound is a policy
  choice, which is why core leaves it to callers.

## References

- AWS KMS API reference, `ScheduleKeyDeletion`,
  <https://docs.aws.amazon.com/kms/latest/APIReference/API_ScheduleKeyDeletion.html>.
- AWS KMS developer guide, resource quotas,
  <https://docs.aws.amazon.com/kms/latest/developerguide/resource-limits.html>.
- Google Cloud KMS, key version states,
  <https://docs.cloud.google.com/kms/docs/key-states>, and destroying
  and restoring key versions,
  <https://docs.cloud.google.com/kms/docs/destroy-restore>.
- NIST SP 800-88 Rev. 2, "Guidelines for Media Sanitization",
  <https://csrc.nist.gov/pubs/sp/800/88/r2/final>, cryptographic
  erase.
- RFC-0018, key custody.
- ADR-0012, storage is per kind.
