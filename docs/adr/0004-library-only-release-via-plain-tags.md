---
adr: 0004
title: Library-Only Release via Plain Tags
status: Accepted
date: 2026-05-06
supersedes: none
superseded-by: none
---

# ADR-0004: Library-Only Release via Plain Tags

## Status

Accepted

## Context

`core` ships no binaries — it is a Go library imported via `go get
go.thesmos.sh/core@vX.Y.Z`. The Go module proxy resolves versions
from git tags directly; consumers do not download a release artifact.

Several teams in adjacent projects use `goreleaser`, signed releases
with `cosign`, SBOM generation, and SLSA provenance attestation.
These are valuable for distributed binaries but produce essentially
no additional integrity beyond `git tag` for a pure Go library:
`go install` and `go get` already verify module integrity via the
checksum database (`sum.golang.org`), and there is no compiled artefact
to sign.

Adding `goreleaser` to a library-only repo is cargo-culting: it
produces release artefacts (tarballs, zips) that nobody downloads,
adds a release-time tool dependency, and creates the impression that
the GitHub release page is the source of truth — when in fact
`pkg.go.dev` is.

## Decision

Releases are made by tagging a commit on `main`:

```sh
git tag v0.1.0
git push --tags
```

The `release.yml` workflow:

1. Verifies the tag is on `main`.
2. Re-runs the full CI gate (`make check`) against the tagged commit.
3. Creates a GitHub release whose body is the `CHANGELOG.md` section
   for that version (extracted with `awk`, no third-party action).

No binaries, no tarballs, no signatures, no SBOM, no provenance
attestation. The Go checksum database is the integrity authority for
this module.

`CHANGELOG.md` follows Keep-a-Changelog and is hand-edited per
release. Conventional Commits feed CHANGELOG entries by hand;
automation (`git-cliff`, etc.) is deferred until release volume
justifies it.

## Alternatives Considered

### `goreleaser` parity with consumer repos

Rejected. `goreleaser`'s value is binary distribution + signing;
neither applies to a Go library. The setup cost is real (config
file, archive layouts, release notes templating) and the output is
unused by Go consumers, who pull from the module proxy.

### Signed releases (`cosign` + SLSA provenance)

Rejected. The Go module proxy already verifies module integrity via
the checksum database. Signing the GitHub release artefact (which is
not what `go get` downloads) provides no additional protection
against tampering.

### Automated CHANGELOG generation (`git-cliff`)

Deferred. At low release volume, hand-editing the CHANGELOG is
faster than configuring and maintaining the generator. Reconsider if
release cadence exceeds ~1/month.

### Release-please / semantic-release bots

Rejected. They add automation to drive releases from commit history;
the corresponding cost is a release-PR-per-batch model that fights
the Go module proxy's "tags are releases" model.

## Consequences

**Positive:**

- Smallest possible release process: `git tag && git push --tags`.
- No release-time dependencies (`goreleaser`, `cosign`, etc.).
- The `release.yml` workflow re-runs the CI gate, providing a fresh
  green-build signal for every published version.
- GitHub release page mirrors `CHANGELOG.md`; the source of truth is
  the file in the repo.

**Negative:**

- No SBOM artefacts. Consumers who need an SBOM generate it from
  their own application image. Acceptable for an upstream library.
- Manual CHANGELOG editing means the maintainer must remember to add
  an entry per release. Mitigated by a PR template checklist item.

**Neutral:**

- Pre-1.0 releases (`v0.x.y`) are unstable by Go module convention;
  a future ADR will codify the path to `v1.0.0`.
