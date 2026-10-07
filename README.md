# core

[![CI](https://github.com/thesm-os/core/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/thesm-os/core/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/go.thesmos.sh/core.svg)](https://pkg.go.dev/go.thesmos.sh/core)
[![Release](https://img.shields.io/github/v/release/thesm-os/core)](https://github.com/thesm-os/core/releases/latest)
[![Go version](https://img.shields.io/github/go-mod/go-version/thesm-os/core)](go.mod)
[![License](https://img.shields.io/badge/license-Apache_2.0-blue.svg)](LICENSE)

core defines the interfaces through which a Go service reads time and
randomness, protects and signs data, stores objects, records telemetry
and calls other services. Each interface comes with implementations
built on the Go standard library, and every [thesmos][thesmos] library
builds on them.

## Install

```sh
go get go.thesmos.sh/core@latest
```

core requires Go 1.27 or later.

## Quick start

An implementation that reads time or randomness takes a clock and a
source of randomness as arguments. This program generates a UUIDv7 from
the system clock. It then generates one from a virtual clock and a
seeded source, as a test does, and that ID is the same on every run:

```go
package main

import (
    "fmt"
    "time"

    "go.thesmos.sh/core/clock/fake"
    "go.thesmos.sh/core/clock/hlc"
    "go.thesmos.sh/core/id/uuidv7"
    "go.thesmos.sh/core/rand/crypto"
    "go.thesmos.sh/core/rand/seeded"
)

func main() {
    // In production, the IDs read the system clock and crypto/rand.
    ids := uuidv7.New(hlc.New(1), crypto.New())
    fmt.Println(uuidv7.Format(ids.Generate()))

    // In a test, a virtual clock and a seeded source return the same IDs on every run.
    clk := fake.New(time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC))
    ids = uuidv7.New(clk, seeded.New(42))
    fmt.Println(uuidv7.Format(ids.Generate())) // 019b76da-a800-7000-8ee6-a2507be62ce9
}
```

## Packages

Each package states its contract, the classes of its errors, its rules
for concurrent use and its allocations in its
[reference documentation][reference].

| Area | Packages | Provides |
|---|---|---|
| Time | [`clock`][clock], [`clock/hlc`][clock/hlc], [`clock/fake`][clock/fake], [`clock/kernel`][clock/kernel] | The `Clock` interface with Hybrid Logical Clock instants, a production clock, a virtual clock for tests, and UTC readings whose error bound the Linux kernel reports. |
| Randomness | [`rand`][rand], [`rand/crypto`][rand/crypto], [`rand/pcg`][rand/pcg], [`rand/seeded`][rand/seeded], [`rand/constant`][rand/constant] | The `Rand` interface over `crypto/rand`, PCG, a seeded HMAC-SHA-256 generator for reproducible runs, and a constant source for tests. |
| Identifiers | [`id`][id], [`id/uuidv7`][id/uuidv7], [`id/uuidv4`][id/uuidv4], [`id/ulid`][id/ulid], [`id/ksuid`][id/ksuid], [`id/constant`][id/constant] | One comparable `ID` type of 128, 160 or 256 bits, and generators of UUIDv7, UUIDv4, ULID and KSUID with their text forms. |
| Values | [`epoch`][epoch], [`version`][version], [`tag`][tag], [`fixed`][fixed], [`page`][page] | Epochs that fence out a stale writer, opaque versions for compare-and-swap, immutable tags, decimals of eight places that refuse to overflow, and cursor pagination. |
| Errors | [`errs`][errs] | Eight error classes, such as `Transient` and `Invalid`, and `Classify`, which also recognises the sentinels of the standard library. |
| Concurrency | [`task`][task], [`fsm`][fsm], [`batch`][batch] | Structured concurrency, finite state machines that are validated when they are built, and batched loads of concurrent callers. |
| Resilience | [`resilience`][resilience] | Circuit breakers, bulkheads, retries within a budget with jittered backoff, token-bucket rate limits, and failover between redundant targets. |
| Hashing and encryption | [`crypto`][crypto], [`crypto/sha256`][crypto/sha256], [`crypto/sha512`][crypto/sha512], [`crypto/sha3`][crypto/sha3], [`crypto/hmac/sha256`][crypto/hmac/sha256], [`crypto/hmac/sha512`][crypto/hmac/sha512], [`crypto/hmac/sha3`][crypto/hmac/sha3], [`crypto/aesgcm`][crypto/aesgcm], [`crypto/shake`][crypto/shake], [`crypto/kek`][crypto/kek], [`crypto/localkey`][crypto/localkey] | The `Hasher`, `MAC`, `AEAD`, `XOF` and `Keeper` interfaces with SHA-2, SHA-3, HMAC, AES-GCM and SHAKE, data keys wrapped under a key-encryption key, and domain-separated framing of hashed input. |
| Signatures | [`crypto/sign`][crypto/sign], [`crypto/sign/ed25519`][crypto/sign/ed25519], [`crypto/sign/ecdsap384`][crypto/sign/ecdsap384], [`crypto/sign/mldsa`][crypto/sign/mldsa], [`crypto/tsp`][crypto/tsp] | The `Signer` and `Verifier` interfaces with Ed25519, ECDSA P-384 and ML-DSA, threshold policies over sets of keys, and offline verification of RFC 3161 time stamps. |
| Transparency logs | [`tlog`][tlog], [`tlog/checkpoint`][tlog/checkpoint], [`tlog/witness`][tlog/witness], [`note`][note] | Merkle trees of RFC 9162 stored as C2SP tiles with inclusion and consistency proofs, signed notes, checkpoints, cosignatures, witness policies, and both sides of the witness protocol. |
| Storage | [`blob`][blob], [`blob/memory`][blob/memory], [`cas`][cas], [`cas/memory`][cas/memory] | Named object storage with conditional writes, content-addressed storage that checks each write against its address, and in-memory stores for tests. |
| Data structures | [`btree`][btree], [`cache`][cache], [`pool`][pool], [`arena`][arena] | B+ tree maps and sets with copy-on-write clones, a bounded S3-FIFO cache, typed pools, and arenas for hot paths. |
| Telemetry | [`telemetry`][telemetry], [`telemetry/noop`][telemetry/noop], [`telemetry/w3c`][telemetry/w3c] | Metric and trace interfaces whose bound instruments record without an allocation, and W3C Trace Context propagation. |
| HTTP | [`net/httpserver`][net/httpserver], [`net/httpclient`][net/httpclient] | A server on `net/http` with limits, telemetry, panic recovery and graceful shutdown, and a client with a circuit breaker per host and retries. |
| Testing | [`coretest`][coretest] | Conformance suites that check an implementation against its interface, generated test doubles, and a time-stamp authority for tests. |

## Design

- Production code imports the standard library, `golang.org/x/sync` and
  the runtime packages of kanon, which generates the binary encodings of
  core's types. A depguard rule fails CI on any other import.
- Code reads time through `clock.Clock` and randomness through
  `rand.Rand`, so a test controls both.
- Interfaces such as `blob.Store`, `crypto.Keeper` and `sign.Signer` have
  conformance suites in `coretest`, so an adapter for a cloud store, a
  KMS or an HSM proves its contract with one test.
- Each method on a hot path states its allocations, and a test and a
  benchmark check the count.
- `errs.Classify` maps an error to one of eight classes, so a retry loop
  or a circuit breaker can tell a transient failure from a permanent one.
- Wire formats follow their specifications: RFC 9162 and RFC 6962 for
  Merkle trees, the C2SP specifications for tiles, signed notes,
  checkpoints, cosignatures and witnesses, RFC 3161 for time stamps,
  RFC 9562 for UUIDs, and FIPS 186-5 and FIPS 204 for ECDSA and ML-DSA.
- CI runs the race detector on Linux, macOS and Windows, requires 100%
  statement coverage, and runs mutation testing.

## Documentation

- The [reference documentation][reference] on pkg.go.dev covers every
  package.
- The [guides](docs/guides/) explain how to test core and how to
  generate its conformance suites.
- The [RFCs](docs/rfc/) record each design with the alternatives that it
  rejected, and the [ADRs](docs/adr/) record each decision.
- The [changelog](CHANGELOG.md) lists the changes of each release.

## Versioning

Until v1.0.0, a minor release of core can change an exported API, which
semantic versioning allows for major version zero. The changelog marks
each such change as breaking. A byte layout that core persists or signs
keeps its encoding from the release that first contains it.

## Contributing

Read [CONTRIBUTING.md](CONTRIBUTING.md) before you open a pull request.
A new interface or package starts with an issue, and a design with
alternatives starts with an RFC.

## Security

Report a vulnerability privately, as [SECURITY.md](SECURITY.md)
describes. Do not open a public issue for it.

## License

core is licensed under the Apache License 2.0. See [LICENSE](LICENSE)
and [NOTICE](NOTICE).

[thesmos]: https://thesmos.sh
[reference]: https://pkg.go.dev/go.thesmos.sh/core
[arena]: https://pkg.go.dev/go.thesmos.sh/core/arena
[batch]: https://pkg.go.dev/go.thesmos.sh/core/batch
[blob]: https://pkg.go.dev/go.thesmos.sh/core/blob
[blob/memory]: https://pkg.go.dev/go.thesmos.sh/core/blob/memory
[btree]: https://pkg.go.dev/go.thesmos.sh/core/btree
[cache]: https://pkg.go.dev/go.thesmos.sh/core/cache
[cas]: https://pkg.go.dev/go.thesmos.sh/core/cas
[cas/memory]: https://pkg.go.dev/go.thesmos.sh/core/cas/memory
[clock]: https://pkg.go.dev/go.thesmos.sh/core/clock
[clock/fake]: https://pkg.go.dev/go.thesmos.sh/core/clock/fake
[clock/hlc]: https://pkg.go.dev/go.thesmos.sh/core/clock/hlc
[clock/kernel]: https://pkg.go.dev/go.thesmos.sh/core/clock/kernel
[coretest]: https://pkg.go.dev/go.thesmos.sh/core/coretest
[crypto]: https://pkg.go.dev/go.thesmos.sh/core/crypto
[crypto/aesgcm]: https://pkg.go.dev/go.thesmos.sh/core/crypto/aesgcm
[crypto/hmac/sha256]: https://pkg.go.dev/go.thesmos.sh/core/crypto/hmac/sha256
[crypto/hmac/sha3]: https://pkg.go.dev/go.thesmos.sh/core/crypto/hmac/sha3
[crypto/hmac/sha512]: https://pkg.go.dev/go.thesmos.sh/core/crypto/hmac/sha512
[crypto/kek]: https://pkg.go.dev/go.thesmos.sh/core/crypto/kek
[crypto/localkey]: https://pkg.go.dev/go.thesmos.sh/core/crypto/localkey
[crypto/sha256]: https://pkg.go.dev/go.thesmos.sh/core/crypto/sha256
[crypto/sha3]: https://pkg.go.dev/go.thesmos.sh/core/crypto/sha3
[crypto/sha512]: https://pkg.go.dev/go.thesmos.sh/core/crypto/sha512
[crypto/shake]: https://pkg.go.dev/go.thesmos.sh/core/crypto/shake
[crypto/sign]: https://pkg.go.dev/go.thesmos.sh/core/crypto/sign
[crypto/sign/ecdsap384]: https://pkg.go.dev/go.thesmos.sh/core/crypto/sign/ecdsap384
[crypto/sign/ed25519]: https://pkg.go.dev/go.thesmos.sh/core/crypto/sign/ed25519
[crypto/sign/mldsa]: https://pkg.go.dev/go.thesmos.sh/core/crypto/sign/mldsa
[crypto/tsp]: https://pkg.go.dev/go.thesmos.sh/core/crypto/tsp
[epoch]: https://pkg.go.dev/go.thesmos.sh/core/epoch
[errs]: https://pkg.go.dev/go.thesmos.sh/core/errs
[fixed]: https://pkg.go.dev/go.thesmos.sh/core/fixed
[fsm]: https://pkg.go.dev/go.thesmos.sh/core/fsm
[id]: https://pkg.go.dev/go.thesmos.sh/core/id
[id/constant]: https://pkg.go.dev/go.thesmos.sh/core/id/constant
[id/ksuid]: https://pkg.go.dev/go.thesmos.sh/core/id/ksuid
[id/ulid]: https://pkg.go.dev/go.thesmos.sh/core/id/ulid
[id/uuidv4]: https://pkg.go.dev/go.thesmos.sh/core/id/uuidv4
[id/uuidv7]: https://pkg.go.dev/go.thesmos.sh/core/id/uuidv7
[net/httpclient]: https://pkg.go.dev/go.thesmos.sh/core/net/httpclient
[net/httpserver]: https://pkg.go.dev/go.thesmos.sh/core/net/httpserver
[note]: https://pkg.go.dev/go.thesmos.sh/core/note
[page]: https://pkg.go.dev/go.thesmos.sh/core/page
[pool]: https://pkg.go.dev/go.thesmos.sh/core/pool
[rand]: https://pkg.go.dev/go.thesmos.sh/core/rand
[rand/constant]: https://pkg.go.dev/go.thesmos.sh/core/rand/constant
[rand/crypto]: https://pkg.go.dev/go.thesmos.sh/core/rand/crypto
[rand/pcg]: https://pkg.go.dev/go.thesmos.sh/core/rand/pcg
[rand/seeded]: https://pkg.go.dev/go.thesmos.sh/core/rand/seeded
[resilience]: https://pkg.go.dev/go.thesmos.sh/core/resilience
[tag]: https://pkg.go.dev/go.thesmos.sh/core/tag
[task]: https://pkg.go.dev/go.thesmos.sh/core/task
[telemetry]: https://pkg.go.dev/go.thesmos.sh/core/telemetry
[telemetry/noop]: https://pkg.go.dev/go.thesmos.sh/core/telemetry/noop
[telemetry/w3c]: https://pkg.go.dev/go.thesmos.sh/core/telemetry/w3c
[tlog]: https://pkg.go.dev/go.thesmos.sh/core/tlog
[tlog/checkpoint]: https://pkg.go.dev/go.thesmos.sh/core/tlog/checkpoint
[tlog/witness]: https://pkg.go.dev/go.thesmos.sh/core/tlog/witness
[version]: https://pkg.go.dev/go.thesmos.sh/core/version
