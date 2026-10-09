<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# Contributing to core

Every other thesmos library depends on core, so a change to core keeps
three constraints:

1. **Dependencies.** Production code imports the Go standard library,
   core itself, `golang.org/x/sync` and the runtime packages of kanon.
   Test code can also import a closed list of test libraries. The
   depguard rules of `.golangci.yml` list both sets, and a change to
   either set needs an ADR. See [ADR-0015][adr-0015] and
   [ADR-0035][adr-0035].
2. **Exported API.** A change that removes or changes an exported
   identifier is a breaking change, and the changelog marks it as one.
3. **License.** A pull request asserts that its author can license the
   contribution under the Apache License 2.0.

## Set up a clone

core requires Go 1.27.2 or later. The Makefile runs its tools through
[ergon](https://github.com/dokimasia/ergon), which installs each tool
at the version that `.ergon.yaml` pins. Install ergon 0.6.0 or later,
then run:

```bash
brew install --cask dokimasia/tap/ergon
git clone git@github.com:thesm-os/core.git
cd core
pre-commit install
make check
```

The hooks run `make lint` and `make test` before each commit,
`make check` before each push, and commitlint on each commit message.

## Managed files

ergon writes the Makefile, `.golangci.yml`, the workflows and every
other file whose first line starts with `Managed by ergon init`. Do not
edit these files. Put a setting of core into the file of the same path
under `.ergon/local/`, run `ergon init sync`, and commit both files. CI
runs `ergon init check`, which fails when a managed file differs from
the file that ergon writes.

## Make a change

For documentation, a fix or a small improvement:

1. Branch from `main`.
2. Make the change, with its tests.
3. Add a changeset, as the next section describes.
4. Run `make check`.
5. Open a pull request against `main`.

For a new interface, a new package or a breaking signature:

1. Open an issue to confirm that the change belongs in core and not in
   a library that depends on it.
2. If two or more designs deserve a comparison, write an [RFC][rfc] from
   [`docs/templates/RFC.md`][rfc-template].
3. If the change makes one decision that a later reader will question,
   record it in an [ADR][adr] from [`docs/templates/ADR.md`][adr-template].
4. Cite the RFC or the ADR in the pull request.

## Changesets

A pull request that changes what a release of core contains adds a
changeset to `.changeset/`. The changeset contains the bump of
`go.thesmos.sh/core` and a summary, which becomes the entry of the
change in the changelog:

```bash
ergon release add --bump go.thesmos.sh/core=minor -m "Add the rename of a key to blob.Store."
```

Before v1.0.0, a breaking change is a minor bump, and its summary
starts with **Breaking:**. A fix is a patch. A change that releases
nothing, such as a change to the documentation alone, adds a changeset
without a bump with `ergon release add --empty`. CI opens a version pull
request from the changesets on `main`, and its merge releases core.

## Code standards

- Library code returns an error instead of a panic, and `forbidigo`
  rejects a call to `panic`. A test can panic on a guard that it cannot
  reach.
- Library code logs through `log/slog`, and `forbidigo` rejects
  `fmt.Print`, `fmt.Printf`, `fmt.Println`, `print` and `println`. A
  package that logs takes its `*slog.Logger` from the caller.
- Every `//nolint` names its linter and states its reason.
- Give each exported declaration a doc comment that states its
  contract: the error modes and their classes, the rules for concurrent
  use, and the allocations of a method on a hot path.
- Give each sentinel error a class of the `errs` package, or state in
  its doc comment why it has none. See [ADR-0033][adr-0033].

## Tests

The [testing guide](docs/guides/testing.md) states how core's tests are
written, named and measured. The [testkit guide](docs/guides/testkit.md)
describes the generated conformance suites in `coretest`.

| Command | Runs |
|---|---|
| `make test` | The unit tests |
| `make race-go` | The unit tests under the race detector |
| `make bench-go` | The benchmarks and their allocation ceilings, in the format of benchstat |
| `make fuzz-go` | Each fuzz target for 30 seconds |
| `make mutate-go` | Mutation testing with dokimi-mutate-go, which runs for a long time |
| `make lint` | golangci-lint with its format check, and ergon-go-vet, which checks the prefixes of error texts and the dates of skipped tests |
| `make audit` | govulncheck |
| `make generate` | The generators of kanon |
| `make check-numbers` | The check that a change keeps every field number that `origin/main` records |
| `make end-to-end` | The end-to-end tests behind the build tag `e2e` |
| `make check` | The gate: lint, the unit tests, the race detector, the generated code of kanon and govulncheck |
| `ergon license fix` | The license header of every file |

## Commit messages

Commit messages follow [Conventional Commits][cc]:

```
feat(crypto): seal and open a message in independent chunks
fix(btree): replace the separator of a removed key with the next key
test(rand): replace testkit with go.dokimi.dev/assert
docs: accept checkpoint witnesses
```

The accepted types are `feat`, `fix`, `docs`, `refactor`, `perf`,
`test`, `build`, `ci`, `chore` and `revert`. A subject has at most 72
characters, and a line of the body at most 100. The commit-msg hook and
CI check both with commitlint and the rules of `.commitlint.yaml`.

## Sign commits

Sign your commits, so that GitHub shows them as **Verified**. To sign
with an SSH key:

```bash
git config --global gpg.format ssh
git config --global user.signingkey ~/.ssh/id_ed25519.pub
git config --global commit.gpgsign true
```

GitHub's [guide to signing commits][sign] covers GPG and sigstore.

## Review

- Each pull request needs an approving review from a
  [code owner](.github/CODEOWNERS) of the paths that it changes.
- CI must pass: lint, the unit tests and the race tests on Linux, macOS
  and Windows, the vulnerability scan, the generated code of kanon, the
  field numbers that the base branch records, the commit messages, the
  changesets, the license headers, the Markdown files and the managed
  files.
- A change to an exported type or a method signature also needs the
  approval of a core maintainer.

[adr]: docs/adr/
[rfc]: docs/rfc/
[adr-template]: docs/templates/ADR.md
[rfc-template]: docs/templates/RFC.md
[adr-0015]: docs/adr/0015-dependency-free-x-modules-in-production.md
[adr-0033]: docs/adr/0033-every-sentinel-has-a-class.md
[adr-0035]: docs/adr/0035-core-imports-the-kanon-runtime.md
[cc]: https://www.conventionalcommits.org/
[sign]: https://docs.github.com/en/authentication/managing-commit-signature-verification/about-commit-signature-verification
