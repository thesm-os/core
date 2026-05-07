package clocktest

// Clock
//go:generate testkit builder -p go.thesmos.sh/core/clock -o clock_fixtures.gen.go Instant InstantRange
//go:generate testkit stub -p go.thesmos.sh/core/clock -o clock_stub.gen.go Clock
//go:generate testkit suite -p go.thesmos.sh/core/clock -o clock_spec.gen.go Clock
//go:generate testkit bench -p go.thesmos.sh/core/clock -o clock_bench.gen.go Clock
//go:generate testkit model -p go.thesmos.sh/core/clock -o clock_model.gen.go Clock

// Timer
//go:generate testkit stub -p go.thesmos.sh/core/clock -o timer_stub.gen.go Timer
