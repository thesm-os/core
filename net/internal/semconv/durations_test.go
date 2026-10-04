// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package semconv_test

import (
	"sync"
	"testing"
	"time"

	"go.dokimi.dev/assert/bench"
	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/coretest/telemetrytest"
	"go.thesmos.sh/core/net/internal/semconv"
	"go.thesmos.sh/core/telemetry"
	"go.thesmos.sh/core/telemetry/noop"
)

// boundSets is the number of attribute sets that a Durations keeps bound,
// which its documentation states.
const boundSets = 2048

// description is the description of the histogram of the cases.
const description = "Duration of HTTP server requests."

// origin is the time of the fake clock of the cases.
var origin = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

// series is the key of the attribute sets of the cases.
type series struct {
	route  string
	status int
}

// histograms is a histogram stub whose With returns a new histogram stub
// for every call and keeps the stubs that it returned, in order.
type histograms struct {
	*telemetrytest.HistogramStub

	mu    sync.Mutex
	bound []*telemetrytest.HistogramStub
}

// newHistograms returns a histograms whose stubs record their calls.
func newHistograms(tb testing.TB) *histograms {
	tb.Helper()

	h := &histograms{HistogramStub: telemetrytest.NewHistogramStub(tb)}
	h.OnWith.Func(func([]telemetry.Attr) telemetry.Histogram {
		set := telemetrytest.NewHistogramStub(nil)

		h.mu.Lock()
		defer h.mu.Unlock()

		h.bound = append(h.bound, set)

		return set
	})

	return h
}

// sets returns the stubs that With returned, in order.
func (h *histograms) sets() []*telemetrytest.HistogramStub {
	h.mu.Lock()
	defer h.mu.Unlock()

	return append([]*telemetrytest.HistogramStub(nil), h.bound...)
}

// releases returns the number of Release calls on every stub that With
// returned.
func (h *histograms) releases() int {
	n := 0
	for _, set := range h.sets() {
		n += set.OnRelease.CallCount()
	}

	return n
}

func TestDurations(t *testing.T) {
	t.Parallel()

	t.Run("NewDurations", func(t *testing.T) {
		t.Parallel()

		t.Run("constructs the histogram of the name in seconds with the advised buckets", func(t *testing.T) {
			t.Parallel()

			r := telemetrytest.NewReporterStub(t)
			r.OnHistogram.Returns(noop.Reporter{}.Histogram(telemetry.InstrumentSpec{}))
			semconv.NewDurations(r, semconv.ServerDuration, description, fake.New(origin), attrsOf)

			call := r.OnHistogram.AssertCalledOnce(t, "NewDurations must construct one histogram")
			testkit.Equal(t, call.Spec, telemetry.InstrumentSpec{
				Name:        semconv.ServerDuration,
				Description: description,
				Unit:        "s",
				Bounds:      []float64{0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10},
			}, "the instrument")
		})
	})

	t.Run("Record", func(t *testing.T) {
		t.Parallel()

		t.Run("records the duration in seconds into the set of the key", func(t *testing.T) {
			t.Parallel()

			h, d := newDurations(t)
			key := series{route: "/items/{id}", status: 200}
			d.Record(t.Context(), key, 1500*time.Millisecond)

			sets := h.sets()
			testkit.Len(t, sets, 1, "Record must bind one set")
			with := h.OnWith.AssertCalledOnce(t, "Record must bind the set once")
			testkit.Equal(t, with.Attrs, attrsOf(key), "the attributes of the set")
			testkit.Equal(t, sets[0].OnRecord.AssertCalledOnce(t, "Record must record once").Value, 1.5, "the value")
		})

		t.Run("binds the set of a key once for every record", func(t *testing.T) {
			t.Parallel()

			h, d := newDurations(t)
			key := series{route: "/", status: 200}
			d.Record(t.Context(), key, time.Second)
			d.Record(t.Context(), key, 2*time.Second)

			h.OnWith.AssertCalledOnce(t, "Record must bind the set of a key once")
			calls := h.sets()[0].OnRecord.AssertCalledN(t, 2, "the bound set must receive both records")
			testkit.Equal(t, calls[1].Value, 2.0, "the second value")
		})

		t.Run("binds a set for each key", func(t *testing.T) {
			t.Parallel()

			h, d := newDurations(t)
			second := series{route: "/", status: 404}
			d.Record(t.Context(), series{route: "/", status: 200}, time.Second)
			d.Record(t.Context(), second, time.Second)

			calls := h.OnWith.AssertCalledN(t, 2, "Record must bind a set for each key")
			testkit.Equal(t, calls[1].Attrs, attrsOf(second), "the attributes of the second set")
		})

		t.Run("releases the set that the cache evicts", func(t *testing.T) {
			t.Parallel()

			h, d := newDurations(t)
			for i := range boundSets + 1 {
				d.Record(t.Context(), series{route: "/", status: i}, time.Second)
			}

			testkit.Equal(t, h.releases(), 1, "the cache must evict and release one set beyond its bound")
		})

		t.Run("binds an evicted set again at the next record of its key", func(t *testing.T) {
			t.Parallel()

			h, d := newDurations(t)
			for i := range boundSets + 1 {
				d.Record(t.Context(), series{route: "/", status: i}, time.Second)
			}

			evicted := -1
			for i, set := range h.sets() {
				if set.OnRelease.CallCount() > 0 {
					evicted = i
				}
			}
			testkit.True(t, evicted >= 0, "one set must have been evicted")

			d.Record(t.Context(), series{route: "/", status: evicted}, time.Second)
			testkit.Equal(t, h.OnWith.CallCount(), boundSets+2, "Record must bind the evicted set again")
		})

		t.Run("records every value when goroutines bind a new set at once", func(t *testing.T) {
			t.Parallel()

			h, d := newDurations(t)
			key := series{route: "/", status: 200}

			var wg sync.WaitGroup
			for range 8 {
				wg.Go(func() { d.Record(t.Context(), key, time.Second) })
			}
			wg.Wait()

			records := 0
			for _, set := range h.sets() {
				records += set.OnRecord.CallCount()
			}
			testkit.Equal(t, records, 8, "a binding of the set must receive every record")
			testkit.Equal(t, h.releases(), len(h.sets())-1, "the cache must release every binding but the one it keeps")
		})
	})
}

func BenchmarkDurations(b *testing.B) {
	b.Run("Record of a bound set", func(b *testing.B) {
		d := semconv.NewDurations(noop.Reporter{}, semconv.ServerDuration, description, fake.New(origin), attrsOf)
		key := series{route: "/items/{id}", status: 200}
		d.Record(b.Context(), key, time.Second)

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			d.Record(b.Context(), key, time.Second)
		}
	})
}

// newDurations returns a Durations over a histograms stub, and the stub.
func newDurations(tb testing.TB) (*histograms, *semconv.Durations[series]) {
	tb.Helper()

	h := newHistograms(tb)
	r := telemetrytest.NewReporterStub(tb)
	r.OnHistogram.Returns(h)

	return h, semconv.NewDurations(r, semconv.ServerDuration, description, fake.New(origin), attrsOf)
}

// attrsOf returns the attributes of the set of s.
func attrsOf(s series) []telemetry.Attr {
	return []telemetry.Attr{
		telemetry.AttrString(semconv.Route, s.route),
		telemetry.AttrInt(semconv.ResponseStatusCode, int64(s.status)),
	}
}
