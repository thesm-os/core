// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package telemetrytest

import (
	"sync"
	"testing"

	"go.thesmos.sh/core/telemetry"
)

// Parameters of the Release assertions of [CounterContractAssertions],
// [GaugeContractAssertions] and [HistogramContractAssertions], and of
// [ReleaseAllocsWithin].
const (
	// releaseEmitters is the number of goroutines that emit through one
	// instrument while another goroutine releases it.
	releaseEmitters = 4

	// releaseEmits is the number of emits of each of those goroutines.
	releaseEmits = 100

	// releaseRuns is the number of calls over which [ReleaseAllocsWithin]
	// averages the allocations of Release.
	releaseRuns = 100
)

// releaseAttrs is the attribute set that the Release assertions bind.
// The assertions only read it, so the parallel assertions share it.
var releaseAttrs = []telemetry.Attr{telemetry.AttrString("release", "bound")}

// ReleaseAllocsWithin returns a plug-in for the Release benchmarks of
// [BenchmarkCounterContract], [BenchmarkGaugeContract] and
// [BenchmarkHistogramContract]. It binds a new instrument with bind for
// every call that it measures, so each call is the first Release of its
// instrument. It fails the benchmark when Release allocates more than
// maxAllocs times per call on average:
//
//	telemetrytest.CounterBenchOnRelease(telemetrytest.ReleaseAllocsWithin(
//	    func(c telemetry.Counter) telemetry.Counter { return c.With(nil) }, 0))
func ReleaseAllocsWithin[T interface{ Release() }](bind func(T) T, maxAllocs int) func(*testing.B, T) {
	return func(b *testing.B, instrument T) {
		b.Helper()
		// testing.AllocsPerRun makes one call as a warm-up before the
		// calls that it measures.
		bound := make([]T, releaseRuns+1)
		for i := range bound {
			bound[i] = bind(instrument)
		}
		next := 0
		allocs := testing.AllocsPerRun(releaseRuns, func() {
			bound[next].Release()
			next++
		})
		if int(allocs) > maxAllocs {
			b.Fatalf("Release allocates %v times per call, above the budget of %d", allocs, maxAllocs)
		}
	}
}

// releaseWhileEmitting calls release on one goroutine while
// releaseEmitters goroutines each call emit with the numbers 0 to
// releaseEmits-1. It returns after every goroutine has returned. Under
// the race detector, a data race between release and emit fails the
// test.
func releaseWhileEmitting(release func(), emit func(i int)) {
	var wg sync.WaitGroup
	for range releaseEmitters {
		wg.Go(func() {
			for i := range releaseEmits {
				emit(i)
			}
		})
	}
	wg.Go(release)
	wg.Wait()
}
