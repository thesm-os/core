---
adr: 0022
title: Decorators Expose What They Wrap
status: Accepted
date: 2026-09-24
supersedes: none
superseded-by: none
---

# ADR-0022: Decorators Expose What They Wrap

## Status

Accepted

## Context

Core's seams discover optional capabilities by type assertion:

- `cas.PutStream` and `cas.GetStream` look for `cas.Streamer`.
- `crypto.GenerateKey` looks for `crypto.KeyGenerator`.
- `sign.SignContext` looks for `sign.ContextSigner`.

A decorator that adds tracing or metrics implements the mandatory
interface of the value it wraps and hides that value's optional
capabilities. The caller then takes the fallback path without any
error: a buffered copy instead of a stream, or a data key generated
outside the custodian instead of inside it.

A decorator cannot implement an interface conditionally. One that
declares `Streamer` claims the capability even when the wrapped store
lacks it.

The standard library meets the same problem twice. `errors.As` follows
each error's `Unwrap` method. `http.NewResponseController` expects the
original `ResponseWriter` "or have an Unwrap method returning the
original ResponseWriter".

## Decision

We will have a decorator of a seam with optional capabilities implement
a method that returns the value it wraps, and have each seam find a
capability through an `As` function that follows that method, because a
decorator cannot implement an interface conditionally.

| Seam | Decorator method | `As` functions |
|---|---|---|
| `cas.Store` | `Unwrap() cas.Store` | `cas.AsStreamer` |
| `crypto.Keeper` | `UnwrapKeeper() crypto.Keeper` | `crypto.AsDestroyer`, `crypto.AsKeyGenerator` |
| `sign.Signer`, `sign.Verifier` | `Unwrap() sign.Signer`, `Unwrap() sign.Verifier` | `sign.AsStreamingSigner`, `sign.AsStreamingVerifier`, `sign.AsContextSigner` |

A `Keeper` decorator implements `UnwrapKeeper`, because
`crypto.Keeper` already has an `Unwrap` method that unwraps a data key.

## Alternatives Considered

### Capability flags

A seam would report its capabilities through a method such as
`Capabilities() Set`.

Rejected. The flag and the methods it describes are two sources of
truth that can disagree, and a decorator would still have to forward
every capability method.

### Decorators forward every capability

A decorator would implement every optional interface of the value it
wraps.

Rejected. A decorator cannot implement an interface conditionally, so
forwarding claims capabilities the wrapped value may lack. The standard
library came to the same conclusion for `http.ResponseWriter`.

## Consequences

**Positive:**

- A tracing or metrics decorator no longer disables streaming,
  key generation inside the custodian, or a signing deadline.
- Core's own helpers use the `As` functions, so a caller of
  `cas.PutStream`, `crypto.GenerateKey` or `sign.SignContext` gets the
  wrapped value's capability without changing its call.
- Each `As` function allocates nothing.

**Negative:**

- Every decorator author has to know the convention. A decorator
  without the method still hides capabilities, and neither the
  compiler nor a conformance suite detects it.
- A decorator that implements a capability itself is found before the
  value it wraps and hides that value's implementation, as a wrapping
  error with its own `As` method does.
- `Keeper` decorators use a different method name from the other
  seams.
- A decorator whose method returns a value earlier in its own chain
  makes the `As` function loop, as `errors.As` does on a cyclic chain.

**Neutral:**

- The convention adds six exported functions across three packages.

## References

- RFC-0038, conformance for durable adapters and decorators.
- Go: `errors.As`, `net/http.NewResponseController`.
