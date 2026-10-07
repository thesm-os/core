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

core requires Go 1.27 or later. The Makefile runs each target through
[ergon](https://github.com/thesm-os/ergon). Install the ergon release
that `.github/actions/setup-ergon/action.yml` pins, then run:

```bash
git clone git@github.com:thesm-os/core.git
cd core
make bootstrap
pre-commit install --hook-type pre-commit --hook-type pre-push --hook-type commit-msg
ergon check --skip mutation
```

`make bootstrap` installs the linters and tools that the gate runs. The
pre-commit and pre-push hooks run `ergon check --skip mutation`, and the
commit-msg hook runs `ergon check commit-msg`.

## Make a change

For documentation, a fix or a small improvement:

1. Branch from `main`.
2. Make the change, with its tests.
3. Run `ergon check --skip mutation`.
4. Open a pull request against `main`.

For a new interface, a new package or a breaking signature:

1. Open an issue to confirm that the change belongs in core and not in
   a library that depends on it.
2. If two or more designs deserve a comparison, write an [RFC][rfc] from
   [`docs/templates/RFC.md`][rfc-template].
3. If the change makes one decision that a later reader will question,
   record it in an [ADR][adr] from [`docs/templates/ADR.md`][adr-template].
4. Cite the RFC or the ADR in the pull request.

## Code standards

- Library code returns an error instead of a panic, and `forbidigo`
  rejects a call to `panic`. A test can panic on a guard that it cannot
  reach.
- Library code logs through `log/slog`, and `forbidigo` rejects
  `fmt.Print` and `fmt.Fprint` and their variants. A package that logs
  takes its `*slog.Logger` from the caller.
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
| `make test` | The unit tests with coverage |
| `make test-race` | The unit tests under the race detector |
| `make test-bench` | The benchmarks and their allocation ceilings |
| `make test-fuzz` | Each fuzz target for the configured duration |
| `make check-coverage` | The statement coverage gate of 100% |
| `make check-mutation` | The mutation testing gate, which runs for a long time |
| `make lint` | go vet, golangci-lint, markdownlint, govulncheck, and the checks of license headers, the dates of skipped tests and the prefixes of error texts |

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
characters, and a line of the body at most 100. The commit-msg hook
checks both with `ergon check commit-msg`, and CI checks them with
commitlint.

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
- CI must pass: vet, build, lint, the race tests on Linux, macOS and
  Windows, coverage, mutation, tidy modules, current generated code, and
  the field numbers that the base branch records.
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
