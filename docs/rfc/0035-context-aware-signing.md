---
rfc: 0035
title: Context-Aware Signing
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-09-24
updated: 2026-09-24
discussion: none
supersedes: none
superseded-by: none
produces-adr: none
---

# RFC-0035: Context-Aware Signing

## Summary

We propose an optional capability `sign.ContextSigner`, for signers
whose signing crosses a process boundary, and a function
`sign.SignContext` that uses it when a signer has it. A caller can then
bound the wait for a hosted key service or a hardware module with a
`context.Context`. `sign.Signer` does not change.

## Motivation

`sign.Signer.Sign(message)` takes no context. That fits Ed25519 and
ECDSA P-384 in process: they finish in microseconds and have nothing to
wait for. It does not fit a signer backed by a hosted key service or by
a hardware module behind a pool of sessions:

- A call to a hosted service can wait on the network for as long as
  the connection allows, and the caller has no deadline to give it.
- A call to a hardware module can wait for a free session, and a
  shutting-down process cannot abandon the wait.
- `crypto.Keeper`, core's other seam that crosses a process boundary,
  already takes a context on every method.
- The client that an implementation calls takes one too. The AWS SDK
  for Go v2 signs through `func (c *Client) Sign(ctx context.Context,
  params *SignInput, optFns ...func(*Options)) (*SignOutput, error)`.
  An implementation of `sign.Signer` over it has no context to pass,
  and calls it with `context.Background()`.

## Detailed design

```go
// ContextSigner is the optional capability for a Signer whose signing
// crosses a process boundary, such as a hosted key service or a
// hardware module behind a pool of sessions.
//
// An implementation documents what ctx stops: the wait for a session,
// the network round trip, or both. An operation already inside a
// hardware module may run to completion after SignContext returns.
type ContextSigner interface {
    Signer

    // SignContext returns a signature over message, or an error
    // wrapping context.Cause(ctx) when ctx ends before the signature
    // is available. Sign on the same value behaves as SignContext
    // with a context that never ends.
    SignContext(ctx context.Context, message []byte) ([]byte, error)
}

// SignContext signs message with s.
//
// When s implements ContextSigner, SignContext calls it. Otherwise it
// returns context.Cause(ctx) if ctx has already ended, and calls
// s.Sign if it has not. An in-process signer needs no context, so the
// check before the call is the only one.
func SignContext(ctx context.Context, s Signer, message []byte) ([]byte, error)
```

A caller that signs with any `Signer` calls `sign.SignContext`, and
gains a deadline wherever the signer can honour one:

```go
ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
defer cancel()

sig, err := sign.SignContext(ctx, signer, root.Bytes())
if err != nil {
    return err
}
```

Verification needs no context. A `Verifier` has the public key in
process and never crosses a boundary.

### Tests

- `SignContext` calls `SignContext` on a signer that implements it, and
  `Sign` on one that does not.
- A context that has already ended returns its cause without calling
  `Sign`.
- The `Signer` conformance suite gains an assertion for
  `ContextSigner`: a context that ends during the call returns an
  error wrapping its cause.

## Alternatives considered

### A. Add a context to `sign.Signer.Sign`

Every signer would take a context, as every `crypto.Keeper` method
does.

**Why not:** it breaks every signer to serve the ones that cross a
boundary, and in-process signers would take a context they cannot use.
The standard library solved the same problem additively:
`database/sql` falls back from the optional `ExecerContext` to
`Execer`, and Go 1.25's `crypto.SignMessage` uses the optional
`crypto.MessageSigner` when a signer has it.

### B. Cancel from the caller's side

The caller would run `Sign` in a goroutine and stop waiting when its
context ends.

**Why not:** the goroutine and the remote operation keep running, and
a signer with a pool of sessions keeps its session busy. Only the
signer can release the session the wait occupies.

## Drawbacks

- A caller must call `sign.SignContext` instead of `Sign` to get the
  deadline. A call to `Sign` on a `ContextSigner` still waits without
  a bound.
- What a context stops depends on the implementation, so each
  implementation has to document it.

## Open questions

None.

## Unresolved / future work

- A context for `StreamingSigner`'s finalisation, if a streaming
  signer that crosses a boundary appears.

## References

- Go: `crypto.MessageSigner` and `crypto.SignMessage` (Go 1.25), and
  `database/sql/driver.ExecerContext`.
- AWS SDK for Go v2, `service/kms`, `Client.Sign`,
  <https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/service/kms>.
- RFC-0013, the signing seam.
- RFC-0018, key custody.
