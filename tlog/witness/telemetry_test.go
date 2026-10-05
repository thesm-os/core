// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness_test

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/telemetry"
	"go.thesmos.sh/core/telemetry/noop"
	"go.thesmos.sh/core/tlog/checkpoint"
	"go.thesmos.sh/core/tlog/witness"
)

// The instruments of a witness.
const (
	durationName = "tlog.witness.commit.duration"
	callsName    = "tlog.witness.commit.calls"
	updatesName  = "tlog.witness.updates"
	originsName  = "tlog.witness.origins"
)

// meter is a telemetry.Reporter that keeps every value of its instruments,
// by the name of the instrument and the attributes of its series.
type meter struct {
	noop.Reporter

	values map[string][]float64
	mu     sync.Mutex
}

// series is one attribute set of an instrument of a meter.
type series struct {
	m   *meter
	key string
}

// counter, gauge and histogram are the instruments of a meter.
type (
	counter   struct{ series }
	gauge     struct{ series }
	histogram struct{ series }
)

// oddClassError is an error whose class is outside the classes of errs.
type oddClassError struct{}

func TestTelemetry(t *testing.T) {
	t.Parallel()

	t.Run("Advance", func(t *testing.T) {
		t.Parallel()

		l := newTestLog(t, logName)

		t.Run("records the duration and the calls of a commit", func(t *testing.T) {
			t.Parallel()
			m, s, _ := metered(t, l)
			advance(t, s, l, l.update(t, 0, 5))

			testkit.Len(t, m.get(durationName), 1, "the commit must record its duration")
			testkit.Equal(t, m.get(callsName), []float64{1}, "the commit must record its calls")
		})

		t.Run("records the class of the error of a failed commit", func(t *testing.T) {
			t.Parallel()
			m, s, f := metered(t, l)
			f.clock.SetUTCError(0, false)

			_, _, err := s.Advance(t.Context(), l.notes[5], []witness.Update{l.update(t, 0, 5)}, nil)
			testkit.ErrorIs(t, err, checkpoint.ErrClock, "the commit must fail")
			testkit.Len(t, m.get(durationName+" error.type=Transient"), 1, "the commit must record its class")
		})

		t.Run("records the error of every class of errs under the name of its class", func(t *testing.T) {
			t.Parallel()

			for c := range errs.Integrity + 1 {
				m, s, f := metered(t, l)
				f.store.intercept(hook{op: opPut, prefix: "records/", before: func(context.Context, string) error {
					return errs.WithClass(errors.New("the store failed"), c)
				}})

				_, _, err := s.Advance(t.Context(), l.notes[5], []witness.Update{l.update(t, 0, 5)}, nil)
				testkit.Error(t, err, "the commit must fail")
				testkit.Len(t, m.get(durationName+" error.type="+c.String()), 1, "the commit must record "+c.String())
			}
		})

		t.Run("records the error of a class outside errs as Unspecified", func(t *testing.T) {
			t.Parallel()
			m, s, f := metered(t, l)
			f.store.intercept(hook{op: opPut, prefix: "records/", before: func(context.Context, string) error {
				return oddClassError{}
			}})

			_, _, err := s.Advance(t.Context(), l.notes[5], []witness.Update{l.update(t, 0, 5)}, nil)
			testkit.Error(t, err, "the commit must fail")
			testkit.Len(t, m.get(durationName+" error.type=Unspecified"), 1, "the commit must record Unspecified")
		})

		t.Run("counts the updates of each outcome", func(t *testing.T) {
			t.Parallel()
			m, s, f := metered(t, l)
			advance(t, s, l, l.update(t, 0, 5))

			_, _, err := s.Advance(t.Context(), l.notes[6], []witness.Update{l.update(t, 0, 6)}, nil)
			testkit.NoError(t, err, "the conflict must be a failure")

			proof := l.update(t, 5, 9).Proof
			proof[0] = l.leaves[0]
			u := l.update(t, 5, 9)
			u.Proof = proof
			_, _, err = s.Advance(t.Context(), l.notes[9], []witness.Update{u}, nil)
			testkit.NoError(t, err, "the inconsistency must be a failure")

			c := &flaky{Cosigner: f.cosigners[0]}
			c.fail.Store(true)
			f.cosigners = []checkpoint.Cosigner{c}
			cfg := f.config()
			cfg.Reporter = m
			_, _, err = newServer(t, cfg).Advance(t.Context(), l.notes[6], []witness.Update{l.update(t, 5, 6)}, nil)
			testkit.Error(t, err, "the signature must fail")

			for _, want := range []string{"committed", "conflict", "inconsistent", "uncosigned"} {
				testkit.Equal(t, m.get(updatesName+" outcome="+want), []float64{1}, "the meter must count one "+want)
			}

			testkit.Len(t, f.logger.messages(slog.LevelError), 1, "the server must log the inconsistent checkpoint")
		})

		t.Run("reports the number of origins after a commit", func(t *testing.T) {
			t.Parallel()
			other := newTestLog(t, "example.com/other")
			m, s, _ := metered(t, l, other)
			advance(t, s, l, l.update(t, 0, 5))
			advance(t, s, other, other.update(t, 0, 5))

			got := m.get(originsName)
			testkit.Equal(t, got[len(got)-1], float64(2), "the gauge must report both origins")
		})
	})
}

// metered returns a meter, and a server of a fixture of logs that records
// into it.
func metered(tb testing.TB, logs ...*testLog) (*meter, *witness.Server, *fixture) {
	tb.Helper()

	m := &meter{values: map[string][]float64{}}
	f := newFixture(tb, logs...)
	cfg := f.config()
	cfg.Reporter = m

	return m, newServer(tb, cfg), f
}

// get returns the values of the series of key.
func (m *meter) get(key string) []float64 {
	m.mu.Lock()
	defer m.mu.Unlock()

	return slices.Clone(m.values[key])
}

// record keeps v in the series of key.
func (m *meter) record(key string, v float64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.values[key] = append(m.values[key], v)
}

// Counter returns the counter of spec.
func (m *meter) Counter(spec telemetry.InstrumentSpec) telemetry.Counter {
	return counter{series{m: m, key: string(spec.Name)}}
}

// Gauge returns the gauge of spec.
func (m *meter) Gauge(spec telemetry.InstrumentSpec) telemetry.Gauge {
	return gauge{series{m: m, key: string(spec.Name)}}
}

// Histogram returns the histogram of spec.
func (m *meter) Histogram(spec telemetry.InstrumentSpec) telemetry.Histogram {
	return histogram{series{m: m, key: string(spec.Name)}}
}

// bind returns the series of s with attrs: the key of s, and a space and
// key=value for each attribute.
func (s series) bind(attrs []telemetry.Attr) series {
	var key strings.Builder

	key.WriteString(s.key)

	for _, a := range attrs {
		key.WriteString(" " + a.Key + "=" + a.SlogAttr().Value.String())
	}

	return series{m: s.m, key: key.String()}
}

// Add records value.
func (c counter) Add(_ context.Context, value int64) {
	c.m.record(c.key, float64(value))
}

// With returns the counter of attrs.
func (c counter) With(attrs []telemetry.Attr) telemetry.Counter {
	return counter{c.bind(attrs)}
}

// Release does nothing.
func (counter) Release() {}

// Set records value.
func (g gauge) Set(_ context.Context, value float64) {
	g.m.record(g.key, value)
}

// Add records delta.
func (g gauge) Add(_ context.Context, delta float64) {
	g.m.record(g.key, delta)
}

// With returns the gauge of attrs.
func (g gauge) With(attrs []telemetry.Attr) telemetry.Gauge {
	return gauge{g.bind(attrs)}
}

// Release does nothing.
func (gauge) Release() {}

// Record records value.
func (h histogram) Record(_ context.Context, value float64) {
	h.m.record(h.key, value)
}

// With returns the histogram of attrs.
func (h histogram) With(attrs []telemetry.Attr) telemetry.Histogram {
	return histogram{h.bind(attrs)}
}

// Release does nothing.
func (histogram) Release() {}

// Error returns the text of the error.
func (oddClassError) Error() string {
	return "an error of an odd class"
}

// Class returns a class outside the classes of errs.
func (oddClassError) Class() errs.Class {
	return errs.Class(200)
}
