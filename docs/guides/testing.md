# How to test core

Write each hand-written test of core with [go.dokimi.dev/assert][assert].
The generated conformance suites and sentinel tests come from testkit,
as the [testkit guide](testkit.md) describes. Do not edit a file whose
name contains `.gen`. Change its `//go:generate` directive instead, and
run `make generate`.

## Put each test beside its source file

- Give each source file one test file of the same name: `cache.go` has
  `cache_test.go`.
- Test through the exported API, from the package `<pkg>_test`. When a
  case needs unexported state, put it in `<file>_internal_test.go`, in
  the package itself.
- Keep fixtures, test types and helpers in the test file of the code
  that they serve. A helper exists only for logic of two or more steps
  that two or more cases call.

## Name each case as a sentence

Write one `Test<Unit>` function for each source file. Give each method
a level of its own under it, and each case a level under its method:

```
TestCache/Get/returns the value that Set stored under the key
TestCache/Get/reports false for a key that the cache evicted
```

- Write the case name as a sentence with a verb in the third person,
  the result and the condition. Use the names that the code declares,
  such as `returns ErrNotFound` and `reports false`.
- Check one behaviour per case, so that no name contains "and".
- Nest each level in a `t.Run` of its own, and never join two levels
  with a dot.
- Call `t.Parallel()` at every level, except in the allocation tests.
- Put cases that differ only in their inputs in a table named `tests`,
  whose rows have a `name` field with the whole case sentence and `give`
  and `want` fields.

## Check one property per assertion

- Use `assert` by default. It stops the case at the first failure, which
  a precondition needs, such as `Length` before an index or `ErrorIs`
  before `err.Error()`.
- Use `expect` when one case checks two or more independent properties
  of one result. `expect.That(t, v)` chains the checks of one value and
  reports every failure.
- Use the dedicated assertion of a property, such as `Nil`, `Length`,
  `ErrorIs`, `ErrorAs`, `Permutation` or `NoDuplicates`. To compare two
  pointers or two slices by identity, pass `assert.ByIdentity()` to
  `Equal`.
- Check a rule over a domain of inputs with `prop.ForAll` or with the
  property form of an assertion, such as `prop.RoundTrip`.
- Run concurrent calls with `history.Concurrently`, and check their
  results with `history.Linearizable` against a sequential
  `history.Spec`. Check a type with state through `stateful.Steps`.
- Check a call under a context that ended with `HonoursCancellation`
  and `HonoursDeadline`.

## State each allocation ceiling twice

A method on a hot path states its allocations under the
`# Allocation contract` heading of its doc comment. Check that number in
a test and in a benchmark, as `tlog/witness/protocol_test.go` does for
`AppendRequest`:

```go
func TestProtocolAllocs(t *testing.T) {
    t.Run("AppendRequest", func(t *testing.T) {
        proof := []crypto.Digest{digest(t, exampleProof1), digest(t, exampleProof2)}
        note := []byte(exampleNote)
        buf := make([]byte, 0, len(exampleRequest))

        var got []byte
        expect.MaxAllocs(t, func() { got = witness.AppendRequest(buf[:0], 20852014, proof, note) }, 0,
            "AppendRequest must not allocate into a buffer with room")
        assert.Equal(t, string(got), exampleRequest, "the test must measure the example")
    })
}

func BenchmarkProtocol(b *testing.B) {
    b.Run("AppendRequest", func(b *testing.B) {
        proof := []crypto.Digest{digest(b, exampleProof1), digest(b, exampleProof2)}
        note := []byte(exampleNote)
        buf := make([]byte, 0, len(exampleRequest))

        c := bench.Start(b).MaxAllocs(0)
        defer c.End()

        var got []byte
        for c.Loop() {
            got = witness.AppendRequest(buf[:0], 20852014, proof, note)
        }

        assert.Equal(b, string(got), exampleRequest, "the benchmark must measure the example")
    })
}
```

The excerpt leaves out the other levels of both functions.

- Name the test `Test<Unit>Allocs`, also when every ceiling in it is 0.
  It does not call `t.Parallel`, because the count covers the whole
  process. `go test` runs it, so CI checks the ceiling on every change.
- `MaxAllocs` counts 100 calls with `GOMAXPROCS` at 1, and rounds the
  mean to the nearest whole number. It checks no ceiling under the race
  detector.
- Build the inputs before the measured function, and assert on its
  result afterwards. The assertion keeps the call from being optimised
  away, and fails a measurement of an error path.
- Before you measure a path through a `sync.Pool`, call `runtime.GC()`
  twice, so the count does not depend on what earlier tests left in the
  pool.
- When the first calls allocate runtime structures, such as the
  goroutines of a worker or the waiters of a pipe, warm the benchmark up
  with `bench.Start(b).Warmup(n)`.

## Fuzz a parser with prop.Fuzz

Declare a fuzz target `Fuzz<Unit>` with `prop.Fuzz`, which
`go test -fuzz` runs. Plain `go test` replays only its stored cases and
its seed corpus. Declare the same property in a test with
`prop.ForAll`, so `go test` checks it on generated inputs as well.

## Cover every statement and detect every mutant

- CI requires 100% statement coverage of every package outside
  `coretest`. `make check-coverage` runs the gate, and
  `make check-uncovered` lists each uncovered line.
- CI runs mutation testing against the thresholds of `.ergon.yaml`.
  `make check-mutation` runs the same gate. Each mutant that the tests
  do not detect needs a case that detects it.
- A mutant that no test can detect, because it does not change the
  behaviour of the code, gets an annotation on the line before it:
  `//dokimi:mutate-skip <kinds>: <reason>`. The reason states why the
  mutant is equivalent.

[assert]: https://pkg.go.dev/go.dokimi.dev/assert
