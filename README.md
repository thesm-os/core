# core

Foundational interfaces for the [thesmos][thesmos] ecosystem.

`core` is a [stdlib-only][adr-0001] Go module that defines the contract
seams every other thesmos library and framework depends on:

- **Clock** — abstracts `time.Now`, `time.Sleep`, and timers so libraries
  remain deterministic under simulation and test.
- **Rand** — `io.Reader`-shaped randomness seam, swappable between
  `crypto/rand` and seeded determinism.
- **Reporter** — OTEL-shaped telemetry seam (counters, histograms,
  spans) with preregistered instruments for zero-allocation hot paths.

These interfaces — and the others added over time — share three
properties:

1. **Stdlib-only.** `core` has zero non-stdlib imports. The dependency
   guard fails CI on any new import outside `$gostd` and the module
   itself. ([ADR-0001][adr-0001])
2. **Single module.** One `go.mod`. Submodules are not needed because
   there are no heavy deps to isolate. ([ADR-0002][adr-0002])
3. **Apache 2.0.** Unencumbered for production and downstream
   redistribution. ([ADR-0003][adr-0003])

## Status

Pre-1.0. Interfaces are added incrementally as their shape stabilises in
consumer libraries. Breaking changes are possible until `v1.0.0`; once
tagged, the standard Go module versioning rules apply.

## Install

```bash
go get go.thesmos.sh/core
```

Module path: `go.thesmos.sh/core` · Repo: `github.com/thesmos-ai/core`

## Documentation

- **[ADRs][adr]** — accepted architectural decisions
- **[RFCs][rfc]** — proposals under discussion or accepted as direction
- **[Contributing][contrib]** — local setup, conventions, PR flow
- **[Security][sec]** — vulnerability disclosure policy

## License

Apache 2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).

[thesmos]: https://thesmos.sh
[adr]: docs/adr/
[rfc]: docs/rfc/
[adr-0001]: docs/adr/0001-stdlib-only-dependencies.md
[adr-0002]: docs/adr/0002-single-module-layout.md
[adr-0003]: docs/adr/0003-apache-2-0-with-spdx-headers.md
[contrib]: CONTRIBUTING.md
[sec]: SECURITY.md
