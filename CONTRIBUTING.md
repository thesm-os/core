# Contributing to core

`core` is the stdlib-only foundation that every other thesmos library
depends on. Contributions are scrutinised against three constraints:

1. **No non-stdlib imports.** Enforced by `depguard`. Adding any import
   outside `$gostd` and `go.thesmos.sh/core` itself fails CI. See
   [ADR-0001][adr-0001].
2. **Stable interface contract.** Once a type or method is exported,
   removing or breaking it triggers a major version bump. Plan
   carefully.
3. **Apache 2.0 by submission.** Opening a PR is your assertion that
   the contribution is yours to license under Apache 2.0.

## Development setup

```bash
# Clone
git clone git@github.com:thesmos-ai/core.git && cd core

# Verify toolchain
go version    # 1.26.2 or later (matches go.mod)

# Install development tools
make bootstrap

# Install pre-commit hooks
pre-commit install --hook-type pre-commit \
                   --hook-type pre-push \
                   --hook-type commit-msg

# Verify everything works
make check
```

`make bootstrap` installs `gofumpt`, `gci`, `golines`, `golangci-lint`,
`govulncheck`, `go-license`, `benchstat`, and `markdownlint-cli2`.

## Making changes

### Documentation, fixes, small improvements

1. Branch from `main`.
2. Make the change.
3. `make check` locally (lint + test + tidy + coverage).
4. Open a PR against `main`.

### New interfaces, breaking signatures, new packages

1. Open an issue first to confirm the interface belongs in `core` (vs.
   a consumer library).
2. If multiple plausible alternatives deserve side-by-side comparison,
   write an [RFC][rfc] following [`docs/templates/RFC.md`][rfc-template].
3. If the decision is crisp and load-bearing (someone will later ask
   "why?"), write an [ADR][adr] following [`docs/templates/ADR.md`][adr-template].
4. Implementation PR cites the RFC/ADR.

If the change is obvious and uncontroversial, neither is required —
just open the PR.

## Code standards

- **No panics in library code.** `forbidigo` enforces this. Test files
  may panic on unreachable guards.
- **No `fmt.Print*`.** Use structured logging if logging is needed at
  all (most `core` packages should not log).
- **Every `//nolint`** must name the linter and cite a specific reason.
- **Zero allocations on documented hot paths.** When an interface method
  is documented as "0-alloc", the corresponding default implementation
  must satisfy that contract; consumer test suites verify it via
  `testing.B.AllocsPerOp`.

## Testing

```bash
make test          # unit tests with coverage
make test-race     # race detector
make test-bench    # benchmarks
make check-vuln    # govulncheck
```

## Commit messages

Use [Conventional Commits](https://www.conventionalcommits.org/):

```
feat(clock): add Timer.Reset behaviour to TestClock
fix(reporter): correct AttrSet.With slice aliasing
docs(adr): ADR-0007 nil-safe AttrSet construction
refactor(rand): collapse Source/Reader into single interface
```

Types allowed: `feat`, `fix`, `docs`, `refactor`, `perf`, `test`,
`build`, `ci`, `chore`, `revert`. Subject ≤ 72 chars, body ≤ 100
chars/line. Enforced by `commitlint` in the `commit-msg` hook.

## Verified commits

Commits should be cryptographically signed (GPG, SSH, or sigstore) so
they appear as **Verified** on GitHub. Configure once with:

```bash
# SSH signing (simplest if you already have an SSH key)
git config --global gpg.format ssh
git config --global user.signingkey ~/.ssh/id_ed25519.pub
git config --global commit.gpgsign true
```

Then commits made with `git commit` are signed automatically. See
GitHub's [signing commits guide][sign] if you prefer GPG or sigstore.

## Review process

- Every PR requires at least one approving review from a
  [CODEOWNER](.github/CODEOWNERS) of the touched paths.
- CI must pass (lint + test + race + vuln + tidy).
- PRs that change exported types or method signatures additionally
  require approval from a `core` maintainer.

[adr]: docs/adr/
[rfc]: docs/rfc/
[adr-template]: docs/templates/ADR.md
[rfc-template]: docs/templates/RFC.md
[adr-0001]: docs/adr/0001-stdlib-only-dependencies.md
[sign]: https://docs.github.com/en/authentication/managing-commit-signature-verification/about-commit-signature-verification
