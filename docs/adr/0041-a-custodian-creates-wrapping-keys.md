---
adr: 0041
title: A Custodian Creates Wrapping Keys
status: Accepted
date: 2026-09-30
supersedes: none
superseded-by: none
---

# ADR-0041: A Custodian Creates Wrapping Keys

## Status

Accepted

## Context

`crypto.Keeper` wraps data keys under one named wrapping key.
`crypto.Destroyer` schedules the destruction of a named key, and
`crypto.KeyGenerator` generates data keys. A caller stores the name of
the wrapping key with every wrapped key, so that it can unwrap after a
rotation. No call creates a wrapping key at the custodian, and no call
returns the Keeper of another named key.

The seam leaves creating a wrapping key to provisioning. One key per
stream, subject or period exceeds hosted quotas at modest scale, and
deleting a wrapped data key erases such a unit without the custodian.

Wei et al. recovered data from solid-state drives after every
single-file sanitization technique that they tested, because the flash
translation layer keeps copies that the host cannot address. Free
filesystem blocks keep further copies. A deleted wrapped data key can
still be read from such copies, and it decrypts for as long as its
wrapping key exists. A caller that bounds how long a deleted byte can be
decrypted has to destroy the wrapping key at the custodian.

A storage engine does so by rotation. At the start of each period it
creates a wrapping key and rewraps every data key that it still uses
under the new key. It then destroys the previous period's key. A period
of half the bound, 12 hours for a bound of 24, needs two keys a day for
each engine.

The custodians differ in how they create, name and destroy a key:

| Custodian | Creates | Names the new key | Destroys |
|---|---|---|---|
| AWS KMS | `CreateKey`, which takes no request token | The service assigns the key ID | `ScheduleKeyDeletion`, 7 to 30 days later, and up to 24 hours more |
| Cloud KMS | `cryptoKeyVersions.create`. `encrypt` accepts a version | The service assigns the next version number | `destroy`, after 24 hours to 120 days, fixed when the CryptoKey is created |
| Azure Key Vault | `create` under a new key name. An existing name gains a version | The caller names the key | Delete and purge of the whole key. A single version cannot be deleted. Purge protection delays the purge to the vault's retention of 7 to 90 days |
| PKCS#11 3.1 | `C_GenerateKey` | The token assigns `CKA_UNIQUE_ID` | `C_DestroyObject`, at once, for a key whose `CKA_DESTROYABLE` is true |

AWS KMS bills $1 a month for a key, and does not bill a key that is
pending deletion. A key pending deletion counts toward the quota of
100,000 keys per account and Region. With one key per 12 hours and a
deletion window of 7 to 30 days, an engine counts 16 to 64 keys toward
the quota and is billed for 2.

## Decision

We will add `crypto.KeyCreator`, the optional capability of a custodian
that creates wrapping keys and opens them by name, because a caller that
bounds how long deleted bytes on its device can be decrypted has to
create a new wrapping key every period:

- `CreateKey(ctx)` creates a wrapping key at the custodian and returns
  its Keeper. The Keeper's `KeyID` names the new key in the custodian's
  form, and no other key has that name. The creator's configuration sets
  the key's type, access policy and destruction delay.
- `OpenKey(ctx, keyID)` returns the Keeper of a wrapping key in the
  creator's scope, which is the set of keys that its configuration can
  create. A name outside the scope, or one the custodian does not have,
  returns `ErrKeyID`. The Keeper of a destroyed key fails `Wrap` and
  `Unwrap` with `ErrKeyDestroyed`.
- A Keeper that `CreateKey` or `OpenKey` returns implements every
  optional capability that the creator implements.
- A decorator that implements `KeyCreator` returns Keepers wrapped in
  itself. `AsKeyCreator` finds a decorator before the Keeper it wraps, as
  `AsDestroyer` does.
- `coretest/cryptotest` checks these rules for every implementation.
  `crypto/localkey` implements the capability with a table of the keys
  it created, which the creator shares with the Keepers it returns.

## Alternatives Considered

### Rotation by the operator

The caller would receive each wrapping key from provisioning, as the
seam requires now.

Rejected. At two keys a day per caller, the bound on deleted bytes would
depend on a process outside the caller that the caller cannot check.

### Rotation in place

A `Rotate` method would switch `Wrap` to a new key. `Unwrap` would keep
accepting material wrapped under earlier keys.

Rejected. A rotation in place would change `KeyID`, which the Keeper
conformance suite requires to be stable. `Unwrap` would have to find the
key from the wrapped bytes. That changes the format of every wrapped
key. AWS KMS also keeps rotated key material until the whole key is
deleted. A rotation there destroys nothing.

### Creation under a name that the caller chooses

A retry would return the key that already exists, so a crash between the
creation and the caller's record would not leak a key.

Rejected. AWS KMS and Cloud KMS assign the name of a new key. AWS KMS
takes no request token. A leaked key has wrapped nothing, so the loss is
a key at the custodian, not confidentiality.

### An interface of the caller

The caller would declare the capability itself.

Rejected. Custodian adapters implement core's `Keeper`. An adapter
written for another caller would not implement the interface, and core's
`As` functions would not find it behind a decorator.

### A key-encryption key per period under a long-lived key

`crypto/kek` would generate a key-encryption key each period, and the
caller would delete its record at the end of the period.

Rejected. The record is stored on the same device, wrapped under a
parent key that is never destroyed. A leftover copy of the record
decrypts for as long as the parent exists.

## Consequences

**Positive:**

- A caller bounds how long deleted bytes can be decrypted, copies on the
  device included, to its period plus the custodian's destruction delay,
  without an operator.
- A caller opens the Keeper of every wrapping key that its records name,
  after a rotation or a restart.

**Negative:**

- The bound includes the custodian's destruction delay: at least 7 days
  at AWS KMS, at least 24 hours at Cloud KMS, and the vault's retention
  at Azure Key Vault when purge protection is on.
- An adapter over Azure Key Vault creates a new key name on each call,
  because the service cannot delete a single version.
- Keys pending deletion count toward the AWS KMS quota. A caller that
  rotates every 12 hours counts 16 to 64 keys, so the default quota
  covers about 1,560 to 6,250 such callers per account and Region.
- A crash between `CreateKey` and the caller's record leaks a key that
  wrapped nothing, and AWS KMS bills it at $1 a month until someone
  deletes it.
- `OpenKey` lets a component name every key in the creator's scope.
  Isolation between tenants in one process needs one creator per tenant,
  each with a scope of its own.
- A decorator that does not implement `KeyCreator` does not see the
  Keepers that the creator returns.

**Neutral:**

- `Destroy` keeps its contract. A caller destroys a created key by its
  name.
- Scheduled destruction and the time at which it becomes irreversible
  are unchanged.

## References

- RFC-0018, key custody.
- RFC-0034 and ADR-0021, scheduled key destruction.
- ADR-0022, decorators expose what they wrap.
- M. Wei, L. M. Grupp, F. E. Spada and S. Swanson, "Reliably Erasing
  Data From Flash-Based Solid State Drives", 9th USENIX Conference on
  File and Storage Technologies (FAST '11), 2011,
  <https://www.usenix.org/conference/fast11/reliably-erasing-data-flash-based-solid-state-drives>.
- AWS KMS API reference, `CreateKey`,
  <https://docs.aws.amazon.com/kms/latest/APIReference/API_CreateKey.html>,
  and `ScheduleKeyDeletion`,
  <https://docs.aws.amazon.com/kms/latest/APIReference/API_ScheduleKeyDeletion.html>.
- AWS KMS developer guide, key rotation,
  <https://docs.aws.amazon.com/kms/latest/developerguide/rotate-keys.html>,
  and resource quotas,
  <https://docs.aws.amazon.com/kms/latest/developerguide/resource-limits.html>.
- AWS KMS pricing, <https://aws.amazon.com/kms/pricing/>.
- Google Cloud KMS reference, `cryptoKeyVersions.create`,
  <https://docs.cloud.google.com/kms/docs/reference/rest/v1/projects.locations.keyRings.cryptoKeys.cryptoKeyVersions/create>,
  `encrypt`,
  <https://docs.cloud.google.com/kms/docs/reference/rest/v1/projects.locations.keyRings.cryptoKeys/encrypt>,
  and key states, <https://docs.cloud.google.com/kms/docs/key-states>.
- Azure Key Vault REST reference, Create Key,
  <https://learn.microsoft.com/en-us/rest/api/keyvault/keys/create-key/create-key>,
  and Delete Key,
  <https://learn.microsoft.com/en-us/rest/api/keyvault/keys/delete-key/delete-key>,
  and the soft-delete overview,
  <https://learn.microsoft.com/en-us/azure/key-vault/general/soft-delete-overview>.
- OASIS, PKCS#11 Specification Version 3.1, sections 5.7.3 and 5.18.1,
  <https://docs.oasis-open.org/pkcs11/pkcs11-spec/v3.1/os/pkcs11-spec-v3.1-os.html>.
