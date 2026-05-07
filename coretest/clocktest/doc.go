package clocktest

// Clock
//go:generate testkit builder -p go.thesmos.sh/core/clock -o clock_fixtures.gen.go Instant InstantRange
//go:generate testkit stub -p go.thesmos.sh/core/clock -o clock_stub.gen.go Clock
//go:generate testkit suite -p go.thesmos.sh/core/clock -o clock_spec.gen.go Clock
//go:generate testkit bench -p go.thesmos.sh/core/clock -o clock_bench.gen.go Clock

// Timer
// testkit stub on Timer is currently blocked: the generator
// emits its own Reset() helper (clears call history) which
// collides with Timer.Reset(time.Duration) bool. Workarounds:
// rename the generator's helper (e.g. ClearCalls), or support a
// per-method skip directive. Until that lands, Timer is exercised
// via the Clock stub's NewTimer return value.

// testkit model intentionally NOT generated for Clock: action.Pure
// compares SUT output against a reference implementation via
// cmp.Diff, which fails-by-construction for non-deterministic
// methods (Now advances HLC logical, Time reads wall clock).
// Porcupine concurrent linearizability also isn't auto-emitted for
// Pure-shape interfaces. Concurrent safety is verified by
// TestConcurrentNow + race-mode runs; HLC monotonicity is verified
// by ClockContractAssertions.
