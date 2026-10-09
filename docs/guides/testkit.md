<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# How to generate the conformance suites

[testkit][testkit] generates the conformance suites, test doubles,
benchmarks and models of `coretest`, and the sentinel tests of core's
packages. Its [documentation][testkit-docs] describes each generator in
full. This guide covers how core lays out and uses the generated files.

Each generated file has `.gen` in its name. Do not edit one. Change the
`//go:generate` directive that writes it, and run `go generate ./...`
with testkit on your `PATH`. `make generate` runs the directives of
kanon alone, whose output CI checks.

## Place the generated files

- The files of an interface of package `<pkg>` go in
  `coretest/<pkg>test`. An interface of a subpackage goes in the
  directory of its parent package, so the files of `crypto/sign` are in
  `coretest/cryptotest`.
- The directives are in `coretest/<pkg>test/doc.go`. Their `-p` flag
  names the package of the interface:

  ```go
  //go:generate testkit suite -p go.thesmos.sh/core/crypto -o hasher_spec.gen.go Hasher
  ```

- A sentinel test goes beside the errors that it tests. The directive
  `//go:generate testkit sentinel -o errors.gen_test.go` is in the file
  that declares the sentinels, such as `cache/errors.go`.
- Hand-written assertion bundles, conformance suites and fixtures go
  beside the generated files, without `.gen` in their names, such as
  `coretest/cryptotest/hasher_spec.go` and
  `coretest/cryptotest/keeper_spec.go`.

## Generators that core uses

| Generator | Writes | Interfaces in core |
|---|---|---|
| `stub` | A test double whose methods a test configures one by one | `Clock`, `Timer`, `AEAD`, `Hasher`, `MAC`, `Stream`, `XOF`, `XOFStream`, `Keeper`, `KeyGenerator`, `Destroyer`, `Signer`, `Verifier`, `SignStream`, `VerifyStream`, `Generator`, `Rand` and the six telemetry interfaces |
| `suite` | A conformance suite with a plug-in point for each method | `Clock`, `Timer`, `Hasher`, `MAC`, `Stream`, `XOF`, `XOFStream`, `Signer`, `Verifier`, `SignStream`, `VerifyStream`, `Generator`, `Rand` and the six telemetry interfaces |
| `bench` | A benchmark of each method, with plug-ins for allocation ceilings | `Clock`, `AEAD`, `Hasher`, `MAC`, `XOF`, `Signer`, `Verifier`, `SignStream`, `VerifyStream`, `Generator`, `Rand` and the six telemetry interfaces |
| `model` | A property test that drives an implementation and a reference with the same random calls | `Clock`, `Hasher`, `MAC`, `Generator` and `Rand` |
| `builder` | Builders of struct fixtures | `clock.Instant` and `clock.InstantRange` |
| `sentinel` | Tests of the prefix, the uniqueness and the wrapping of the sentinel errors of one file | The files that declare sentinel errors, such as `cache/errors.go` |

The conformance suites of `blob.Store`, `cas.Store`, `crypto.AEAD`,
`crypto.Keeper` and the epoch fences are hand-written, in `blobtest`,
`castest`, `cryptotest` and `epochtest`.

## Run a conformance suite

Each suite takes a constructor of the implementation and a list of
assertions. `cryptotest.HasherContractAssertions()` and the other
bundles list the standard assertions, and a caller adds the facts of
its implementation, as `crypto/sha256/sha256_test.go` does:

```go
func TestSHA256HasherContract(t *testing.T) {
    t.Parallel()
    cryptotest.AssertHasherContract(t, func() crypto.Hasher { return cryptosha256.New() },
        append(cryptotest.HasherContractAssertions(),
            cryptotest.HasherIDAssertion(sha256ID),
            cryptotest.HasherAlgorithmAssertion(crypto.AlgSHA256),
            cryptotest.HasherCrossStdlibAssertion(stdlibSpec.Sum),
        )...,
    )
}
```

A benchmark contract measures each method of an implementation, and
its plug-ins set the allocation ceilings:

```go
func BenchmarkSHA256Hasher(b *testing.B) {
    cryptotest.BenchmarkHasherContract(b, func() crypto.Hasher { return cryptosha256.New() },
        cryptotest.HasherBenchOnHash(bench.PureAllocsWithin[crypto.Hasher, crypto.Digest](0)),
    )
}
```

The `bench` of this example is `go.thesmos.sh/testkit/bench`, whose
plug-ins the generated contracts take. A hand-written benchmark uses the
contract of `go.dokimi.dev/assert/bench`, as the
[testing guide](testing.md) shows.

## Configure a test double

`New<Interface>Stub(tb, options...)` returns a double with a field
`On<Method>` for each method:

- `Returns` fixes the result of the method, and `Func` computes it.
- `Faults` returns an error on every nth call, `FaultsFor` returns one
  for a duration, and `Latency` delays each call.
- `Times` sets the number of calls that the test expects.
- The option `<Interface>StubStrict()` fails the test on a call of a
  method that the test did not configure, and
  `<Interface>StubDelegateTo(impl)` passes each call to a real
  implementation.

## Annotate an interface

A directive on a method of an interface changes what the generators
write for that method:

| Directive | Effect |
|---|---|
| `//testkit:nondeterministic` | The model checks no determinism law for the method, such as for `Clock.Now`, and calls it without comparing results. |
| `//testkit:mutator` | A method without a return value counts as a mutator, so the suite and the model call it to change state. |
| `//testkit:sample F...` | The suite and the benchmark pass `F(impl)` instead of the zero value for each parameter, for a method that refuses the zero value. |

[testkit]: https://github.com/thesm-os/testkit
[testkit-docs]: https://github.com/thesm-os/testkit/tree/main/docs/testkit
