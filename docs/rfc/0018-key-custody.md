---
rfc: 0018
title: Key Custody
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Draft
created: 2026-08-03
updated: 2026-08-03
discussion: none
supersedes: none
superseded-by: none
produces-adr: ADR-0008
---

# RFC-0018: Key Custody

## Summary

`crypto/keywrap` — a key-custody seam that wraps and unwraps data
encryption keys and never sees a payload. The contract is
`Wrap`/`Unwrap`, never `Get`/`Put`, because the custodians that matter
cannot export their root key material and a seam that required them to
would exclude exactly the implementations worth having. Destruction and
in-custodian key generation are optional capabilities. Ships an
in-module implementation over `crypto.AEAD` so the conformance suite
has a subject.

## Motivation

Envelope encryption is the standard shape for protecting data at rest:
a per-object data key encrypts the payload, and a root key held by a
custodian encrypts the data key. The property that makes it worth doing
is that the root key never leaves its custodian.

A seam that hands out raw key material cannot be implemented by the
custodians that matter — a hardware security module, a hosted key
service — because non-exportability is precisely their value. Any
interface with a `Get(keyID) ([]byte, error)` method has already
excluded them, and it has done so silently: the interface compiles, and
the exclusion only surfaces when someone tries to implement it against
real hardware.

Getting this shape wrong is expensive to correct, because the wrong
shape leaks into how keys are stored. A system built on exportable keys
stores them somewhere, and moving to a custodian later means
re-wrapping every data key that was ever written.

## Detailed design

```go
// Package keywrap is the key-custody seam: it wraps and unwraps data
// keys and never sees a payload.
package keywrap

// Keeper wraps and unwraps data encryption keys.
//
// The data key crosses this boundary; the key protecting it does not.
// That is what allows a hardware module or a hosted service whose root
// key is non-exportable by design to implement the seam at all. A
// Keeper that returned root key material could not be.
//
// # Concurrency
//
// Implementations must be safe for concurrent use.
type Keeper interface {
    // KeyID names the wrapping key in whatever form the custodian
    // uses. Persist it with each wrapped key so material wrapped
    // before a rotation can still be unwrapped after one.
    KeyID() string

    // Wrap encrypts a data key.
    Wrap(ctx context.Context, dek []byte) ([]byte, error)

    // Unwrap decrypts one. Material wrapped under a different key, or
    // corrupted, is an error rather than a wrong answer.
    Unwrap(ctx context.Context, wrapped []byte) ([]byte, error)
}
```

### The package is named for the operation, not for storage

`keywrap` rather than `keystore`. The seam's central decision is that
it is not storage — there is no `Get`, no `Put`, and no path by which
key material is retrieved by name. A package called `keystore` would
advertise the shape the RFC exists to reject, and the first question
every reader asked would be why the store has no read method.

### `KeyID` is a string, not a `crypto.ID`

`crypto.ID` identifies an implementation and `crypto/sign.KeyID`
identifies a key by a canonical derivation from its public part. A
wrapping key has no public part and its name is assigned by the
custodian — an opaque resource identifier, a slot number, a label.
`core` cannot derive it and must not reshape it, so the type is the
custodian's own string.

Persisting it alongside every wrapped key is what makes rotation
survivable: after a rotation, material wrapped under the previous key
is still identified by the name it was wrapped under.

### Capabilities

```go
// Destroyer is the optional capability for custodians that can
// irreversibly destroy a wrapping key — the primitive underneath
// erasure of encrypted-at-rest data.
//
// Callers requiring erasure assert for Destroyer at wiring time and
// fail fast when the assertion does not hold. A custodian that cannot
// destroy cannot provide erasure, and that must surface during
// configuration rather than during a deletion request.
type Destroyer interface {
    Keeper
    Destroy(ctx context.Context, keyID string) error
}

// Generator is the optional capability for custodians that can mint a
// data key internally and return it wrapped.
//
// This is how hosted custodians prefer to be used, and it is the
// stronger shape: the plaintext data key is generated inside the
// custodian's entropy boundary rather than the caller's, and a caller
// that only ever encrypts can discard the plaintext immediately and
// never hold key material it does not need.
type Generator interface {
    Keeper

    // GenerateKey returns a fresh data key of size bytes, both in the
    // clear and wrapped under this Keeper's key. Callers that only
    // encrypt should zero plaintext as soon as the payload is sealed.
    GenerateKey(ctx context.Context, size int) (plaintext, wrapped []byte, err error)
}
```

Both are genuinely absent from some correct custodians, so requiring
either on `Keeper` would make the contract unsatisfiable for them. As
capability interfaces the gap surfaces at wiring time, which is the
only time it can be handled — discovering at deletion time that the
configured custodian cannot destroy leaves a caller with a request it
cannot honour and data it has already promised to erase.

### The in-module implementation

`crypto/keywrap/local` implements all three interfaces over a
`crypto.AEAD` (RFC-0017) with a root key held in process memory.

It exists so the conformance suite has a subject, and so a caller can
exercise an envelope-encryption path without provisioning a custodian.
Its doc comment states plainly that a root key in process memory is not
custody, and that the package is for tests and local development.

`Destroy` zeroes the root key and marks the keeper destroyed;
subsequent `Unwrap` calls fail. That is a faithful model of the
observable contract even though the underlying guarantee is much weaker
than a hardware custodian's.

### No algorithm on the seam

`Keeper` does not expose the algorithm it wraps with, unlike every
other `core` crypto seam.

The reason is that the custodian may not know it and may change it
without notice — a hosted service is entitled to re-wrap its own
material under a stronger algorithm during a maintenance window, and a
caller that persisted the old name would then hold a value that is
wrong and looks right. `KeyID` is the durable identifier here, and it
is the custodian's job to know what a key of that name was wrapped
with.

The cost is that a wrapped key is not self-describing, which is stated
in the drawbacks rather than hidden.

### Conformance

`coretest/keywraptest` asserts the properties that make the seam
meaningful:

- `Unwrap(Wrap(dek))` returns `dek` exactly.
- `Wrap` of the same data key twice does not produce identical bytes,
  which catches an implementation that wraps deterministically and
  leaks equality of data keys.
- `Unwrap` of corrupted input is an error, not a wrong answer.
- `Unwrap` of material wrapped under a different key is an error.
- `KeyID` is stable across calls.
- For a `Destroyer`, `Unwrap` after `Destroy` fails.
- For a `Generator`, `Unwrap(wrapped)` equals the returned plaintext,
  and two calls return different keys.

### Allocation contract

Unspecified, deliberately. Every method crosses a process boundary in
any implementation that matters, so allocation is not the cost that
governs, and specifying a contract the in-module implementation could
meet but no real custodian could would be a contract in name only.

## Alternatives considered

### A. `Get`/`Put` over raw key material

**Why not:** it excludes every custodian whose value is
non-exportability, which is every custodian worth using. The exclusion
is silent at compile time and total at implementation time.

### B. Fold key custody into the AEAD seam

Have `crypto.AEAD` own key retrieval.

**Why not:** they are separate concerns with separate implementations.
An AEAD is a local computation; a custodian is a remote service. Fusing
them would make the AEAD seam context-taking and fallible for every
caller, including the overwhelming majority who hold their own key.

### C. Require `Destroy` on `Keeper`

**Why not:** custodians that cannot destroy are correct custodians. A
contract some correct implementations cannot satisfy is the wrong
contract.

### D. Model the data key as a typed value rather than `[]byte`

**Why not:** a data key is opaque bytes whose length is the AEAD's
business, and a type would imply `core` knows which algorithm it is
for. The seam deliberately does not.

## Drawbacks

- A three-method interface plus two capabilities is a small surface for
  a whole package, and a caller could reasonably declare it at the
  point of use instead. What the package buys is that everyone declares
  the same one, and that the conformance suite exists.
- No in-module implementation is real custody, so the suite proves the
  contract is satisfiable but proves nothing about the implementations
  that matter.
- `KeyID() string` returns an unvalidated, unstructured value. Two
  custodians will use incompatible naming schemes and `core` has
  nothing to say about it.
- A wrapped key is not self-describing, because the seam exposes no
  algorithm. Recovering material therefore requires the custodian that
  wrapped it to still exist and still know the key.
- `Wrap` and `Unwrap` are per-call remote operations with no batching,
  so a caller unwrapping many data keys pays a round trip each.
- `Generator.GenerateKey` returns plaintext key material as a slice
  the caller must zero, which is a discipline the type system does not
  enforce.

## Open questions

None. Both questions this RFC previously carried are resolved above:
the seam exposes no wrapping algorithm, for the reason given in that
section, and in-custodian key generation is added as the `Generator`
capability.

## Unresolved / future work

- A caching wrapper that holds unwrapped data keys for a bounded time,
  which every caller will otherwise write. It needs a policy for the
  bound, which is why it is not here.
- Batch `Unwrap`, if a custodian protocol emerges that supports it.
- Asymmetric custody, where the custodian holds a private key and
  wrapping is done with the public half offline.
