---
adr: 0077
title: The Checks of an Expiry Take Their Timing from the Store
status: Accepted
date: 2026-11-04
supersedes: RFC-0082
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0077: The Checks of an Expiry Take Their Timing from the Store

## Status

Accepted

Supersedes the lifetime and the wait of the checks of an expiry in
[RFC-0082](../rfc/0082-signed-requests-on-named-object-storage.md), and adds
a method to its `Backend`. The rest of RFC-0082 remains in force.

## Context

A check of an expiry of `blobtest` expects the backend to refuse a signed
request once the request has expired. The check takes four steps:

1. It reads the time of the store through `Backend.Time`.
2. It signs an expiry one second after that time.
3. It lets two seconds elapse.
4. It sends the request.

The store reads its own clock inside `SignGet` and `SignCreate`, after the
check has read it. The store refuses an expiry less than one second after
its own reading. So a store whose clock moves between the two reads refuses
the lifetime of the check. A probe on Linux with Go 1.27.3 ran the store of
`blob/memory` over the production clock of core:

- The store refused 200,000 of 200,000 calls with an expiry one second
  after the reading of the check.
- The store read its clock at least 40 ns after the check, with a median of
  50 ns. The longest delay was 11.49 µs in one run of 200,000 calls and
  129.82 µs in another, so the longest delay depends on the load of the
  machine.
- Both checks of an expiry failed at the signature. Every other check of
  the two suites passed.

The fixed wait of two seconds assumes how a store rounds its expiry and how
the clock of the backend follows the clock of the test. The probe measured
both against MinIO RELEASE.2025-04-22T22-12-26Z, with the presigner of
aws-sdk-go-v2 service/s3 v1.114.6:

- The presigner writes the lifetime in whole seconds, rounded down. A
  lifetime of 1.5 s or 1.999 s gives `X-Amz-Expires=1`, and 999 ms gives
  `X-Amz-Expires=0`.
- The probe sent a request every 5 ms. In each of 30 runs MinIO served the
  last request at most 0.44 ms before `X-Amz-Date` plus `X-Amz-Expires`, and
  it refused the first request at most 5.38 ms after that time.

A `SignedRequest` reports its `Expiry` as the time from which the backend
refuses it, and the store of `blob/memory` refuses a request from that
time. The delay of the clock of a store depends on the store and on the
clock of its platform. Core has measured only its own store, on Linux.

## Decision

We will take the timing of the checks of an expiry from the store under
test, because no lifetime and no wait that the suite fixes fits every
store. The new method `Backend.SignDelay` states the longest time from a
call of `Backend.Time` to the read of the clock of the store in a call of
`SignGet` or `SignCreate`. A check of an expiry takes these steps:

1. It signs the shortest lifetime of one second plus `SignDelay`.
2. It requires an `Expiry` at or before the expiry of its call.
3. It waits until that `Expiry` on the clock of the backend.
4. It sends the request and expects a refusal.

## Alternatives Considered

### One second and a wait of two seconds

The check signs the shortest lifetime and lets two seconds elapse.

Rejected. A store whose clock moves between the two reads refuses the
lifetime. The probe measured 200,000 refusals in 200,000 calls.

### A lifetime of 1.5 seconds and a wait of two seconds

The check signs 1.5 seconds and lets two seconds elapse. Half a second
covers the delay of the store, and the other half covers the lag of the
clock of the backend.

Rejected. No measurement of either quantity supports the split.

### A lifetime of two seconds and a wait until the Expiry

The check signs one second over the shortest lifetime and waits until the
`Expiry` of its request.

Rejected. The suite chooses the margin of one second, and no property of
the store under test sets it. A run took up to 1.393 s on the store of
`blob/memory` over the production clock.

### A lifetime of one second and one millisecond, and a wait until the Expiry

The check signs a margin of 1 ms, about 8 times the longest delay that the
probe measured, and waits until the `Expiry` of its request.

Rejected. The margin covers the measured store on Linux alone. A presign
call of aws-sdk-go-v2 took up to 4.57 ms in the probe, and the delays of
other stores and of other platforms are unmeasured. The design passed 20 of
20 runs on the store of `blob/memory` over the production clock, with runs
of up to 996 ms.

## Consequences

**Positive:**

- The checks of an expiry will pass every store whose Backend states the
  delay of its clock.
- A store over a fake clock states a delay of 0, because its clock does not
  move between the two reads. Its checks keep the shortest lifetime of one
  second.
- The wait tests the `Expiry` that the store reports, whatever rounding the
  store applies.
- A check over a real backend waits at most the lifetime that it signed.
- On the production clock of core, with the delay that the probe measured
  before the runs, both suites passed 40 of 40 runs.

**Negative:**

- `Backend` gains a fourth method, and the test of every store states a
  delay. A store over a real clock has to measure its delay. A delay below
  the real one fails the check at the signature.
- The checks wait for the `Expiry` that the store reports. A store that
  reports an `Expiry` after the expiry of the call fails at once.
- The planted defect of each check of an expiry becomes a store that
  reports an earlier `Expiry` than the one that it signs.

**Neutral:**

- The checks of the invalid lifetimes keep their expiries of 999 ms and of
  `MaxSignedLifetime` plus one second after the time of the store. The
  longest measured delay of 129.82 µs leaves both on their side of the
  bounds.

## References

- Issue 109, <https://github.com/thesm-os/core/issues/109>.
- RFC-0082, signed requests on named object storage, the checks of
  `GetSigner` and `CreateSigner` and the `Backend` of a store under test.
- MinIO RELEASE.2025-04-22T22-12-26Z, the image `quay.io/minio/minio`.
- aws-sdk-go-v2 v1.47.5 and service/s3 v1.114.6, `PresignClient.PresignGetObject`.
