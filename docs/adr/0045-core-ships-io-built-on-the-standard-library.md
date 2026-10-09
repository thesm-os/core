---
adr: 0045
title: Core Ships IO Built on the Standard Library
status: Accepted
date: 2026-10-04
supersedes: ADR-0008
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0045: Core Ships IO Built on the Standard Library

## Status

Accepted

## Context

Core defines contracts that describe IO, such as the blob store, key
custody and the telemetry reporter. Until now it contained no
implementation that performs IO. The rule excluded transports, drivers
and network code, and anything that opens a socket or a file, to keep
the module tree of a driver, such as a database client or a cloud SDK,
out of every build that imports core.

The rule also kept out code whose only dependency is the standard
library. `net/http` is part of the Go platform, so an HTTP server or
client built on it adds no module to a consumer's build. The standard
library itself contains `net/http` beside `io.Reader` and `fs.FS`.

Consumers that call HTTP dependencies build their own servers and
clients, and do not use core's mechanisms for the same jobs:

- A consumer's outbound HTTP client has its own circuit breaker and
  retry budget beside core's `resilience` package. It counts a call
  whose caller cancelled as a failure of the dependency, which core's
  breaker does not count.
- The same consumer builds its own `http.Server`, and sets one of the
  server's timeouts. `net/http` leaves the others unbounded at their
  zero values.
- The clients of the protocols that core implements stay outside core:
  the time-stamp client of RFC 3161, and the client and server of
  signed-checkpoint witnesses.

Each of these needs core's vocabulary: the error classes of `errs`, the
`telemetry` seams and their W3C propagation, `clock`, and the
mechanisms of `resilience`.

## Decision

We will let core ship IO implementations built only on the standard
library and the golang.org/x modules that core admits, such as an HTTP
server and client, because they add no module to a consumer's build and
every consumer otherwise builds its own without core's resilience,
telemetry and error classification.

## Alternatives Considered

### Keep IO implementations out of core

Each consumer builds the transports that it needs, as before.

Rejected. The reason for the rule, the module tree of a driver, does not
arise for an implementation on `net/http`. The transports that consumers
build duplicate core's mechanisms and diverge from them: the breaker of
one consumer's client counts a cancelled call as a failure, the case
that core's breaker excludes so that many callers cancelling at once do
not open a circuit against a healthy dependency.

## Consequences

**Positive:**

- One HTTP server and one HTTP client, built on core's `resilience`,
  `telemetry`, `errs` and `clock`, replace the ones that each consumer
  builds.
- The clients of the protocols that core implements, such as RFC 3161
  time stamps and checkpoint witnesses, can live beside their formats.
- The test of what core may import is unchanged: the dependency guard
  lists each allowed module by name, and an implementation that needs
  another module fails it.

**Negative:**

- Core takes on network code: timeouts, connection pools, TLS settings
  and shutdown sequences. Its tests run over loopback sockets, which are
  slower than the in-memory tests of core's other packages, and
  `net/http` measures its timeouts in real time, which a fake clock
  cannot drive.
- A default that is wrong, such as a timeout too short for a streaming
  response, reaches every consumer that does not override it. The
  defaults become part of core's compatibility promise.
- Core answers for the security of its defaults. A consumer still gets
  the fixes of `net/http` through Go releases, and gets the defaults
  that core chooses on top of them.
- "Core describes, consumers implement" no longer settles by itself
  whether an implementation belongs in core. What the implementation
  imports now decides it too.

**Neutral:**

- The contracts that describe IO stay in core, as before, with the
  in-memory implementations that give their conformance suites a
  subject.
- Drivers that need another module, such as database clients, cloud
  SDKs, OpenTelemetry exporters and RPC frameworks, stay in consumer
  modules.
- A contract that takes a `context.Context` still states what the
  context governs: cancellation, a deadline, or the correlation of
  telemetry.
